package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

const (
	platformPrefix = "/_apps"
	userHeader     = "X-Forwarded-User"
	identityTTL    = 30 * time.Second
)

func init() {
	caddy.RegisterModule(Handler{})
	registerCommand()
}

// Handler is the Caddy HTTP handler in front of the apps. It serves the
// dashboard, login, the tools API and the MCP endpoint itself, and for every
// other path checks that the first path segment is a published app (and that
// the visitor may see it) before handing the request to the python handler
// that follows it in the route.
type Handler struct {
	Config
	// AllowedHosts are extra host names (besides the public URL's host and
	// localhost) the dashboard and tools may be reached under.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`

	platform *Platform
	logger   *zap.Logger
	hosts    map[string]bool
	poolKey  string

	ids *identityCache
}

// identityCache remembers recent sign-in lookups so a signed-in person's
// requests do not each hit the database.
type identityCache struct {
	mu      sync.Mutex
	entries map[string]cachedIdentity
}

type cachedIdentity struct {
	id      *Identity
	expires time.Time
}

// CaddyModule returns the Caddy module information.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.python_apps",
		New: func() caddy.Module { return new(Handler) },
	}
}

// platforms shares one running Platform per data folder across config
// reloads, so reloading Caddy does not restart the database.
var platforms = caddy.NewUsagePool()

type pooledPlatform struct{ *Platform }

func (p pooledPlatform) Destruct() error {
	p.Close()
	return nil
}

// Provision starts (or joins) the platform for this data folder.
func (h *Handler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()
	cfg := h.Config
	if err := cfg.resolve(); err != nil {
		return err
	}
	h.poolKey = cfg.DataDir
	val, _, err := platforms.LoadOrNew(h.poolKey, func() (caddy.Destructor, error) {
		startCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		p, err := Open(startCtx, cfg, h.logger)
		if err != nil {
			return nil, err
		}
		return pooledPlatform{p}, nil
	})
	if err != nil {
		return err
	}
	h.platform = val.(pooledPlatform).Platform
	if cfg.RequireLogin {
		if users, err := h.platform.Users(ctx); err == nil && len(users) == 0 {
			h.logger.Warn("login is required but nobody can sign in yet; add a person with: python-apps user add <name> --admin")
		}
	}
	h.ids = &identityCache{entries: map[string]cachedIdentity{}}

	h.hosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if u, err := url.Parse(h.platform.cfg.PublicURL); err == nil && u.Hostname() != "" {
		h.hosts[strings.ToLower(u.Hostname())] = true
	}
	for _, host := range h.AllowedHosts {
		h.hosts[strings.ToLower(host)] = true
	}
	return nil
}

// Cleanup releases the platform; it stops when the last handler lets go.
func (h *Handler) Cleanup() error {
	if h.poolKey != "" {
		_, err := platforms.Delete(h.poolKey)
		return err
	}
	return nil
}

var (
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)

func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

// allowedHost guards the dashboard and tools against DNS-rebinding: a web page
// on another site must not be able to reach them by pointing its own name at
// this server.
func (h *Handler) allowedHost(r *http.Request) bool { return h.hosts[hostOnly(r.Host)] }

// sameOrigin rejects browser requests made from another site (CSRF). Tools
// that are not browsers send no Origin header and are let through.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func bearerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if _, password, ok := r.BasicAuth(); ok {
		return password
	}
	return ""
}

// identify works out who is making the request: an access key (agents, CLI,
// git) or a browser session. It returns nil for an anonymous request.
func (h *Handler) identify(r *http.Request) *Identity {
	var kind, token string
	if t := bearerToken(r); t != "" {
		kind, token = "key", t
	} else if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		kind, token = "session", c.Value
	} else {
		return nil
	}
	cacheKey := kind + ":" + hashToken(token)
	now := time.Now()
	h.ids.mu.Lock()
	if hit, ok := h.ids.entries[cacheKey]; ok && now.Before(hit.expires) {
		h.ids.mu.Unlock()
		return hit.id
	}
	h.ids.mu.Unlock()

	var id *Identity
	if kind == "key" {
		id = h.platform.KeyIdentity(r.Context(), token)
	} else {
		id = h.platform.SessionIdentity(r.Context(), token)
	}
	h.ids.mu.Lock()
	if len(h.ids.entries) > 10000 {
		h.ids.entries = map[string]cachedIdentity{}
	}
	h.ids.entries[cacheKey] = cachedIdentity{id: id, expires: now.Add(identityTTL)}
	h.ids.mu.Unlock()
	return id
}

func (h *Handler) forgetIdentities() {
	h.ids.mu.Lock()
	h.ids.entries = map[string]cachedIdentity{}
	h.ids.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func firstSegment(path string) string {
	path = strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}

func loginURL(r *http.Request) string {
	return platformPrefix + "/login?next=" + url.QueryEscape(r.URL.RequestURI())
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	path := r.URL.Path
	if path == "/" || path == platformPrefix || strings.HasPrefix(path, platformPrefix+"/") {
		h.servePlatform(w, r)
		return nil
	}

	slug := firstSegment(path)
	exists, published := h.platform.Known(slug)
	if !exists {
		renderMessage(w, http.StatusNotFound, "Nothing here", "There is no app at this address.", "/", "See all apps")
		return nil
	}
	var id *Identity
	if h.platform.cfg.RequireLogin {
		if id = h.identify(r); id == nil {
			http.Redirect(w, r, loginURL(r), http.StatusSeeOther)
			return nil
		}
	}
	if !published {
		renderMessage(w, http.StatusServiceUnavailable, "Not ready yet", "This app has been created but no working version has been published.", "/", "See all apps")
		return nil
	}
	// Apps trust this header to know who is using them, so never let a visitor set it.
	r.Header.Del(userHeader)
	if id != nil {
		r.Header.Set(userHeader, id.Username)
	}
	h.platform.Touch(slug)
	return next.ServeHTTP(w, r)
}

func (h *Handler) servePlatform(w http.ResponseWriter, r *http.Request) {
	p := h.platform
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == platformPrefix+"/health" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !h.allowedHost(r) {
		http.Error(w, "This server is not set up to be reached at "+r.Host+". Start it with --public-url set to the address people use.", http.StatusMisdirectedRequest)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
		http.Error(w, "Request refused: it came from another website.", http.StatusForbidden)
		return
	}

	switch path {
	case platformPrefix + "/login":
		h.serveLogin(w, r)
		return
	case platformPrefix + "/logout":
		if r.Method == http.MethodPost {
			if c, err := r.Cookie(sessionCookie); err == nil {
				p.Logout(r.Context(), c.Value)
				h.forgetIdentities()
			}
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	id := h.identify(r)
	isAPI := strings.HasPrefix(path, platformPrefix+"/api") || path == platformPrefix+"/mcp"
	isGit := strings.HasPrefix(r.URL.Path, gitPrefix)
	if p.cfg.RequireLogin && id == nil {
		if isGit {
			// git asks for a username and password; the password is the access key.
			w.Header().Set("WWW-Authenticate", `Basic realm="caddy-snake apps (password: your access key)"`)
			http.Error(w, "an access key is required", http.StatusUnauthorized)
			return
		}
		if isAPI {
			w.Header().Set("WWW-Authenticate", `Bearer realm="caddy-snake apps"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "This server requires an access key. Create one with `caddysnake apps key create <username>` and send it as a Bearer token."})
			return
		}
		http.Redirect(w, r, loginURL(r), http.StatusSeeOther)
		return
	}
	who := "someone"
	if id != nil {
		who = id.Username
	}

	switch {
	case isGit:
		h.serveGit(w, r, who)
	case path == "" || path == platformPrefix:
		h.serveDashboard(w, r, id)
	case path == platformPrefix+"/connect":
		h.serveConnect(w, r, id)
	case path == platformPrefix+"/people":
		h.servePeople(w, r, id)
	case strings.HasPrefix(path, platformPrefix+"/app/"):
		h.serveAppPage(w, r, id, strings.TrimPrefix(path, platformPrefix+"/app/"))
	case strings.HasPrefix(path, platformPrefix+"/api/manage/"):
		h.serveManage(w, r, id, strings.TrimPrefix(path, platformPrefix+"/api/manage/"))
	case path == platformPrefix+"/mcp":
		p.ServeMCP(w, r, who)
	case path == platformPrefix+"/api/tools":
		writeJSON(w, http.StatusOK, map[string]any{"tools": Tools()})
	case strings.HasPrefix(path, platformPrefix+"/api/tools/"):
		h.serveTool(w, r, who, strings.TrimPrefix(path, platformPrefix+"/api/tools/"))
	default:
		renderMessage(w, http.StatusNotFound, "Nothing here", "That page does not exist.", "/", "See all apps")
	}
}

func (h *Handler) serveTool(w http.ResponseWriter, r *http.Request, who, name string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST with a JSON body"})
		return
	}
	// Requiring JSON keeps plain cross-site HTML forms from calling tools.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "send the arguments as application/json"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request too large or unreadable"})
		return
	}
	known := false
	for _, t := range Tools() {
		known = known || t.Name == name
	}
	if !known {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("there is no tool called %q", name)})
		return
	}
	out, err := h.platform.Call(r.Context(), name, who, body)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func safeNext(next string) string {
	// Only local paths: never redirect to another site after login.
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.Contains(next, "\\") {
		return next
	}
	return "/"
}

func (h *Handler) serveLogin(w http.ResponseWriter, r *http.Request) {
	p := h.platform
	next := safeNext(r.URL.Query().Get("next"))
	if !p.cfg.RequireLogin {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost {
		renderLogin(w, http.StatusOK, next, "")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderLogin(w, http.StatusBadRequest, next, "That did not work. Try again.")
		return
	}
	next = safeNext(r.PostForm.Get("next"))
	token, expires, err := p.Login(r.Context(), r.PostForm.Get("username"), r.PostForm.Get("password"))
	if err != nil {
		// Slow down guessing a little without tracking state.
		time.Sleep(400 * time.Millisecond)
		msg := "That did not work. Try again."
		if errors.Is(err, errBadLogin) {
			msg = "Wrong username or password."
		}
		renderLogin(w, http.StatusUnauthorized, next, msg)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.HasPrefix(p.cfg.PublicURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}
