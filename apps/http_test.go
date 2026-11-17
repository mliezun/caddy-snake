package apps

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

// newTestHandler wires a Handler to a test platform without running Caddy.
// The "next" handler stands in for the python handler and records what the
// gate let through.
func newTestHandler(t *testing.T, requireLogin bool) (h *Handler, serve func(*http.Request) *httptest.ResponseRecorder, passed *[]*http.Request) {
	t.Helper()
	p := newTestPlatform(t)
	p.cfg.RequireLogin = requireLogin
	h = &Handler{
		platform: p,
		logger:   zap.NewNop(),
		hosts:    map[string]bool{"apps.test": true, "localhost": true},
		ids:      &identityCache{entries: map[string]cachedIdentity{}},
	}
	seen := []*http.Request{}
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		seen = append(seen, r)
		_, _ = io.WriteString(w, "app says hi")
		return nil
	})
	serve = func(r *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		if err := h.ServeHTTP(rec, r, next); err != nil {
			t.Fatalf("ServeHTTP: %v", err)
		}
		return rec
	}
	return h, serve, &seen
}

func request(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, "http://apps.test"+target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return out
}

func TestGateAndToolsOpenMode(t *testing.T) {
	h, serve, passed := newTestHandler(t, false)
	p := h.platform
	ctx := context.Background()

	if rec := serve(request(http.MethodGet, "/nothing-here/", "")); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app = %d, want 404", rec.Code)
	}
	if len(*passed) != 0 {
		t.Fatal("a request for an unknown app reached the python handler")
	}

	// Create through the HTTP tool API, with a dependency-free app.
	app, _, err := p.CreateApp(ctx, "Notes", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	save, _ := json.Marshal(map[string]any{"app": app.Slug, "files": map[string]any{
		"main.py": strings.Replace(plainApp, "%s", "1", 1), "requirements.txt": nil, "static/style.css": nil,
	}})
	rec := serve(request(http.MethodPost, "/_apps/api/tools/save_files", string(save)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save_files = %d: %s", rec.Code, rec.Body.String())
	}
	if pub, _ := decodeBody(t, rec)["publish"].(map[string]any); pub == nil || pub["ok"] != true {
		t.Fatalf("save_files did not publish: %s", rec.Body.String())
	}

	// A published app passes the gate; a spoofed identity header does not.
	r := request(http.MethodGet, "/notes/page", "")
	r.Header.Set(userHeader, "mallory")
	if rec := serve(r); rec.Code != http.StatusOK || rec.Body.String() != "app says hi" {
		t.Fatalf("app request = %d %q", rec.Code, rec.Body.String())
	}
	if len(*passed) != 1 || (*passed)[0].Header.Get(userHeader) != "" {
		t.Fatalf("identity header reached the app: %q", (*passed)[0].Header.Get(userHeader))
	}

	// Tool API details.
	if rec := serve(request(http.MethodGet, "/_apps/api/tools", "")); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"create_app"`) {
		t.Fatalf("tool list = %d", rec.Code)
	}
	if rec := serve(request(http.MethodPost, "/_apps/api/tools/no_such_tool", "{}")); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown tool = %d", rec.Code)
	}
	if rec := serve(request(http.MethodPost, "/_apps/api/tools/get_app", `{"app":"missing"}`)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tool error = %d", rec.Code)
	}
	if rec := serve(request(http.MethodPost, "/_apps/api/tools/get_app", `{"app":"notes","surprise":1}`)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown argument = %d", rec.Code)
	}
	plain := httptest.NewRequest(http.MethodPost, "http://apps.test/_apps/api/tools/list_apps", strings.NewReader("{}"))
	plain.Header.Set("Content-Type", "text/plain")
	if rec := serve(plain); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON tool call = %d", rec.Code)
	}

	// Cross-site and wrong-host protection on platform routes.
	cross := request(http.MethodPost, "/_apps/api/tools/list_apps", "{}")
	cross.Header.Set("Origin", "http://evil.example")
	if rec := serve(cross); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", rec.Code)
	}
	same := request(http.MethodPost, "/_apps/api/tools/list_apps", "{}")
	same.Header.Set("Origin", "http://apps.test")
	if rec := serve(same); rec.Code != http.StatusOK {
		t.Fatalf("same-site POST = %d", rec.Code)
	}
	rebind := httptest.NewRequest(http.MethodGet, "http://evil.example/", nil)
	if rec := serve(rebind); rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host = %d, want 421", rec.Code)
	}

	// Dashboard and health.
	if rec := serve(request(http.MethodGet, "/", "")); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Notes") || !strings.Contains(rec.Body.String(), "/_apps/connect") {
		t.Fatalf("dashboard = %d", rec.Code)
	}
	if rec := serve(request(http.MethodGet, "/_apps/health", "")); rec.Code != 200 {
		t.Fatalf("health = %d", rec.Code)
	}
}

func mcpCall(t *testing.T, serve func(*http.Request) *httptest.ResponseRecorder, body string) map[string]any {
	t.Helper()
	rec := serve(request(http.MethodPost, "/_apps/mcp", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("mcp = %d: %s", rec.Code, rec.Body.String())
	}
	return decodeBody(t, rec)
}

func TestMCPEndpoint(t *testing.T) {
	_, serve, _ := newTestHandler(t, false)

	init := mcpCall(t, serve, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	result := init["result"].(map[string]any)
	if result["protocolVersion"] != "2025-03-26" || !strings.Contains(result["instructions"].(string), "relative links") {
		t.Fatalf("initialize result = %v", result)
	}
	// An unknown protocol version falls back to the newest one we speak.
	init = mcpCall(t, serve, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if init["result"].(map[string]any)["protocolVersion"] != mcpLatestVersion {
		t.Fatalf("fallback version = %v", init["result"])
	}

	if rec := serve(request(http.MethodPost, "/_apps/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)); rec.Code != http.StatusAccepted {
		t.Fatalf("notification = %d, want 202", rec.Code)
	}
	if rec := serve(request(http.MethodGet, "/_apps/mcp", "")); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rec.Code)
	}

	list := mcpCall(t, serve, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(Tools()) {
		t.Fatalf("listed %d tools, want %d", len(tools), len(Tools()))
	}
	first := tools[0].(map[string]any)
	if first["name"] != "guide" || first["inputSchema"].(map[string]any)["type"] != "object" {
		t.Fatalf("first tool = %v", first)
	}

	call := mcpCall(t, serve, `{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"list_apps","arguments":{}}}`)
	res := call["result"].(map[string]any)
	if res["isError"] != false || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), `"apps"`) {
		t.Fatalf("tools/call = %v", res)
	}
	// A tool failure is a readable result, not a protocol error.
	call = mcpCall(t, serve, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_app","arguments":{"app":"missing"}}}`)
	res = call["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), `no app called "missing"`) {
		t.Fatalf("failing tools/call = %v", res)
	}
	unknown := mcpCall(t, serve, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	if unknown["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method = %v", unknown)
	}

	// Batches are answered in order; notifications inside get no reply.
	rec := serve(request(http.MethodPost, "/_apps/mcp", `[{"jsonrpc":"2.0","id":7,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	var batch []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil || len(batch) != 1 || batch[0]["id"] != float64(7) {
		t.Fatalf("batch = %s", rec.Body.String())
	}
}

func TestLoginRequired(t *testing.T) {
	h, serve, passed := newTestHandler(t, true)
	p := h.platform
	ctx := context.Background()

	if err := p.AddUser(ctx, "ana", "correct horse battery", true); err != nil {
		t.Fatal(err)
	}
	for user, pw := range map[string]string{"Ana": "long enough password", "ana2": "short"} {
		if err := p.AddUser(ctx, user, pw, false); err == nil {
			t.Fatalf("AddUser(%q, %q) should have been refused", user, pw)
		}
	}
	if err := p.AddUser(ctx, "ana", "another password!", false); err == nil {
		t.Fatal("duplicate user accepted")
	}
	key, err := p.CreateAccessKey(ctx, "ana", "assistant")
	if err != nil || !strings.HasPrefix(key, accessKeyPrefix) {
		t.Fatalf("CreateAccessKey = %q, %v", key, err)
	}

	// Nothing is reachable anonymously.
	if rec := serve(request(http.MethodGet, "/", "")); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/_apps/login") {
		t.Fatalf("anonymous dashboard = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, path := range []string{"/_apps/api/tools/list_apps", "/_apps/mcp"} {
		if rec := serve(request(http.MethodPost, path, "{}")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s = %d, want 401", path, rec.Code)
		}
	}
	gitReq := request(http.MethodGet, "/_apps/git/notes.git/info/refs?service=git-upload-pack", "")
	if rec := serve(gitReq); rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("anonymous git = %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}

	// The access key works as a Bearer token and as a git password.
	withKey := request(http.MethodPost, "/_apps/api/tools/list_apps", "{}")
	withKey.Header.Set("Authorization", "Bearer "+key)
	if rec := serve(withKey); rec.Code != http.StatusOK {
		t.Fatalf("key = %d: %s", rec.Code, rec.Body.String())
	}
	bad := request(http.MethodPost, "/_apps/api/tools/list_apps", "{}")
	bad.Header.Set("Authorization", "Bearer csk_not-a-real-key")
	if rec := serve(bad); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad key = %d", rec.Code)
	}

	// Browser sign-in.
	app, _, err := p.CreateApp(ctx, "Notes", "", "ana")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveFiles(ctx, app.Slug, "", plainFiles("1"), "v1", "ana"); err != nil {
		t.Fatal(err)
	}
	if res, err := p.Publish(ctx, app.Slug, "", "ana"); err != nil || !res.OK {
		t.Fatalf("publish: %v %+v", err, res)
	}
	if rec := serve(request(http.MethodGet, "/notes/", "")); rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous app = %d, want a redirect to sign in", rec.Code)
	}
	login := func(password, next string) *httptest.ResponseRecorder {
		form := url.Values{"username": {"ana"}, "password": {password}, "next": {next}}
		r := httptest.NewRequest(http.MethodPost, "http://apps.test/_apps/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return serve(r)
	}
	if rec := login("wrong password", "/notes/"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	// After signing in, never redirect to another site.
	if rec := login("correct horse battery", "//evil.example/x"); rec.Header().Get("Location") != "/" {
		t.Fatalf("open redirect: Location = %q", rec.Header().Get("Location"))
	}
	rec := login("correct horse battery", "/notes/")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/notes/" {
		t.Fatalf("login = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Name != sessionCookie {
		t.Fatalf("session cookie = %+v", cookies)
	}

	r := request(http.MethodGet, "/notes/", "")
	r.AddCookie(cookies[0])
	r.Header.Set(userHeader, "mallory")
	if rec := serve(r); rec.Code != http.StatusOK {
		t.Fatalf("signed-in app request = %d", rec.Code)
	}
	if got := (*passed)[len(*passed)-1].Header.Get(userHeader); got != "ana" {
		t.Fatalf("app saw user %q, want ana", got)
	}

	// Signing out ends the session.
	out := httptest.NewRequest(http.MethodPost, "http://apps.test/_apps/logout", nil)
	out.AddCookie(cookies[0])
	serve(out)
	r = request(http.MethodGet, "/notes/", "")
	r.AddCookie(cookies[0])
	if rec := serve(r); rec.Code != http.StatusSeeOther {
		t.Fatalf("request after sign-out = %d, want a redirect to sign in", rec.Code)
	}

	// Changing a password and removing a person both work.
	if err := p.SetPassword(ctx, "ana", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if rec := login("correct horse battery", "/"); rec.Code != http.StatusUnauthorized {
		t.Fatal("old password still works")
	}
	if err := p.RemoveUser(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	h.forgetIdentities()
	gone := request(http.MethodPost, "/_apps/api/tools/list_apps", "{}")
	gone.Header.Set("Authorization", "Bearer "+key)
	if rec := serve(gone); rec.Code != http.StatusUnauthorized {
		t.Fatalf("removed person's key still works: %d", rec.Code)
	}
}

func TestPasswordHashing(t *testing.T) {
	hash := hashPassword("correct horse battery")
	if !verifyPassword(hash, "correct horse battery") {
		t.Fatal("correct password rejected")
	}
	if verifyPassword(hash, "Correct horse battery") || verifyPassword("garbage", "x") || verifyPassword("", "") {
		t.Fatal("wrong password accepted")
	}
	if hashPassword("same") == hashPassword("same") {
		t.Fatal("hashes are not salted")
	}
}

func TestCommandHelpers(t *testing.T) {
	loopback := map[string]bool{"127.0.0.1:9080": true, "localhost:80": true, "[::1]:9080": true, "0.0.0.0:9080": false, ":443": false, "192.168.1.5:80": false}
	for addr, want := range loopback {
		if got := isLoopbackListen(addr); got != want {
			t.Errorf("isLoopbackListen(%q) = %v, want %v", addr, got, want)
		}
	}
	urls := map[[2]string]string{
		{"127.0.0.1:9080", ""}:       "http://localhost:9080",
		{"0.0.0.0:8000", ""}:         "http://localhost:8000",
		{"10.0.0.5:8000", ""}:        "http://10.0.0.5:8000",
		{":443", "apps.example.com"}: "https://apps.example.com",
	}
	for in, want := range urls {
		if got := derivePublicURL(in[0], in[1]); got != want {
			t.Errorf("derivePublicURL(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	cfg, err := BuildCaddyConfig(ServerConfig{Config: Config{DataDir: "/srv/data"}, Listen: "127.0.0.1:9080", AppMemoryLimit: "256MiB"})
	if err != nil {
		t.Fatalf("BuildCaddyConfig: %v", err)
	}
	raw := string(cfg.AppsRaw["http"])
	for _, want := range []string{`"handler":"python_apps"`, `"handler":"python"`, `"module_auto":"main:app"`, `"mount_prefix":"/{http.request.uri.path.0}"`, `"memory_limit":"256MiB"`, `/srv/data/apps/{http.request.uri.path.0}/current`} {
		if !strings.Contains(raw, want) {
			t.Errorf("config is missing %s:\n%s", want, raw)
		}
	}
	if _, err := BuildCaddyConfig(ServerConfig{Config: Config{DataDir: "/srv/data"}, Listen: ":1", AppMemoryLimit: "lots"}); err == nil {
		t.Error("expected an invalid memory limit to be refused")
	}
}
