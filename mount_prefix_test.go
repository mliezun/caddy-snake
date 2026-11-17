package caddysnake

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func TestNormalizeMountPrefix(t *testing.T) {
	ok := map[string]string{
		"":              "",
		"/":             "",
		"  /app  ":      "/app",
		"/app":          "/app",
		"/app/":         "/app",
		"/tools/my-app": "/tools/my-app",
		"/a.b_c~d/E9":   "/a.b_c~d/E9",
	}
	for in, want := range ok {
		got, err := normalizeMountPrefix(in)
		if err != nil {
			t.Errorf("normalizeMountPrefix(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeMountPrefix(%q) = %q, want %q", in, got, want)
		}
	}

	bad := []string{
		"app",        // no leading slash
		"//app",      // empty segment
		"/a//b",      // empty segment
		"/a/../b",    // dot-dot
		"/./a",       // dot
		"/my app",    // space
		"/a%2Fb",     // percent
		"/café",      // non-ASCII
		"/a?b",       // query delimiter
		"/a\r\nX: y", // header injection
	}
	for _, in := range bad {
		if got, err := normalizeMountPrefix(in); err == nil {
			t.Errorf("normalizeMountPrefix(%q) = %q, want error", in, got)
		}
	}
}

func handlerStatus(t *testing.T, err error) int {
	t.Helper()
	var he caddyhttp.HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("expected caddyhttp.HandlerError, got %v", err)
	}
	return he.StatusCode
}

func TestApplyMountPrefix_Disabled(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/anything", nil)
	for _, pattern := range []string{"", "/"} {
		w := httptest.NewRecorder()
		out, handled, err := applyMountPrefix(pattern, w, r)
		if err != nil || handled {
			t.Fatalf("pattern %q: handled=%v err=%v", pattern, handled, err)
		}
		if got := mountPrefixFromContext(out.Context()); got != "" {
			t.Fatalf("pattern %q: prefix in context = %q, want empty", pattern, got)
		}
	}
}

func TestApplyMountPrefix_InsidePrefix(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/app/items/1?q=2", nil)
	w := httptest.NewRecorder()
	out, handled, err := applyMountPrefix("/app/", w, r)
	if err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got := mountPrefixFromContext(out.Context()); got != "/app" {
		t.Fatalf("prefix in context = %q, want /app", got)
	}
	if out.URL.Path != "/app/items/1" {
		t.Fatalf("path was rewritten to %q; the worker strips the prefix, not Go", out.URL.Path)
	}
}

func TestApplyMountPrefix_BarePrefixRedirects(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/app?a=1&b=2", nil)
	w := httptest.NewRecorder()
	_, handled, err := applyMountPrefix("/app", w, r)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v, want handled redirect", handled, err)
	}
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want 308", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/app/?a=1&b=2" {
		t.Fatalf("Location = %q", got)
	}
}

func TestApplyMountPrefix_OutsidePrefix(t *testing.T) {
	for _, path := range []string{"/", "/other", "/application", "/app2/x", "//app/x"} {
		r := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
		_, handled, err := applyMountPrefix("/app", httptest.NewRecorder(), r)
		if handled || err == nil {
			t.Fatalf("path %q: handled=%v err=%v, want 404 error", path, handled, err)
		}
		if got := handlerStatus(t, err); got != http.StatusNotFound {
			t.Fatalf("path %q: status = %d, want 404", path, got)
		}
	}
}

func requestWithReplacer(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	repl := caddy.NewReplacer()
	repl.Map(func(key string) (any, bool) {
		if key == "test.segment" {
			segs := splitFirstSegment(r.URL.Path)
			return segs, true
		}
		return nil, false
	})
	return r.WithContext(context.WithValue(r.Context(), caddy.ReplacerCtxKey, repl))
}

// splitFirstSegment mirrors {http.request.uri.path.0} for tests that do not run
// the full caddyhttp server.
func splitFirstSegment(path string) string {
	for len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return path
}

func TestApplyMountPrefix_Placeholder(t *testing.T) {
	r := requestWithReplacer("http://example.com/alpha/reports")
	out, handled, err := applyMountPrefix("/{test.segment}", httptest.NewRecorder(), r)
	if err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got := mountPrefixFromContext(out.Context()); got != "/alpha" {
		t.Fatalf("prefix = %q, want /alpha", got)
	}

	// Bare dynamic prefix redirects too.
	w := httptest.NewRecorder()
	_, handled, err = applyMountPrefix("/{test.segment}", w, requestWithReplacer("http://example.com/alpha"))
	if err != nil || !handled || w.Header().Get("Location") != "/alpha/" {
		t.Fatalf("handled=%v err=%v location=%q", handled, err, w.Header().Get("Location"))
	}

	// The site root resolves to "/" which means "no prefix".
	out, handled, err = applyMountPrefix("/{test.segment}", httptest.NewRecorder(), requestWithReplacer("http://example.com/"))
	if err != nil || handled || mountPrefixFromContext(out.Context()) != "" {
		t.Fatalf("root: handled=%v err=%v prefix=%q", handled, err, mountPrefixFromContext(out.Context()))
	}

	// A segment that cannot be a mount point is a 404, not a server error.
	_, handled, err = applyMountPrefix("/{test.segment}", httptest.NewRecorder(), requestWithReplacer("http://example.com/not%20valid/x"))
	if handled || err == nil || handlerStatus(t, err) != http.StatusNotFound {
		t.Fatalf("invalid segment: handled=%v err=%v", handled, err)
	}
}

func TestSetPythonWorkerOutboundHeaders_MountPrefix(t *testing.T) {
	// Client-supplied value is always removed.
	in := httptest.NewRequest(http.MethodGet, "http://example.com/app/x", nil)
	in.RemoteAddr = "203.0.113.5:1"
	in.Header.Set(caddySnakeMountPrefixHeader, "/evil")
	pr := &httputil.ProxyRequest{In: in, Out: in.Clone(context.Background())}
	setPythonWorkerOutboundHeaders(pr, "127.0.0.1:9999", "")
	if got := pr.Out.Header.Get(caddySnakeMountPrefixHeader); got != "" {
		t.Fatalf("spoofed header survived: %q", got)
	}

	// The resolved prefix from the request context replaces it.
	withPrefix, handled, err := applyMountPrefix("/app", httptest.NewRecorder(), in)
	if err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	pr = &httputil.ProxyRequest{In: withPrefix, Out: withPrefix.Clone(context.Background())}
	setPythonWorkerOutboundHeaders(pr, "127.0.0.1:9999", "")
	if got := pr.Out.Header.Get(caddySnakeMountPrefixHeader); got != "/app" {
		t.Fatalf("%s = %q, want /app", caddySnakeMountPrefixHeader, got)
	}
	if got := pr.Out.URL.Path; got != "/app/x" {
		t.Fatalf("outbound path = %q, want the full path", got)
	}
}

func TestCaddyfileMountPrefix(t *testing.T) {
	d := caddyfile.NewTestDispenser(`python {
		module_wsgi "main:app"
		mount_prefix /tools/inventory
	}`)
	var cs CaddySnake
	if err := cs.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile: %v", err)
	}
	if cs.MountPrefix != "/tools/inventory" {
		t.Fatalf("MountPrefix = %q", cs.MountPrefix)
	}

	d = caddyfile.NewTestDispenser(`python {
		module_wsgi "main:app"
		mount_prefix
	}`)
	if err := new(CaddySnake).UnmarshalCaddyfile(d); err == nil {
		t.Fatal("expected error for mount_prefix without an argument")
	}
}

func TestModuleAuto(t *testing.T) {
	d := caddyfile.NewTestDispenser(`python {
		module_auto "main:app"
		lifespan on
	}`)
	var cs CaddySnake
	if err := cs.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile: %v", err)
	}
	if cs.ModuleAuto != "main:app" {
		t.Fatalf("ModuleAuto = %q", cs.ModuleAuto)
	}
	iface, module, err := cs.pythonInterfaceAndModule()
	if err != nil || iface != "auto" || module != "main:app" {
		t.Fatalf("pythonInterfaceAndModule = %q, %q, %v", iface, module, err)
	}
	if err := cs.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// The worker chooses the runtime once it knows the interface.
	if got := effectivePythonRuntime("auto", ""); got != "" {
		t.Fatalf("effectivePythonRuntime(auto) = %q, want empty", got)
	}
	cs.Runtime = "uvloop"
	if err := cs.Validate(); err == nil {
		t.Fatal("expected Validate to reject runtime together with module_auto")
	}
	both := &CaddySnake{ModuleAuto: "main:app", ModuleWsgi: "main:app"}
	if err := both.Validate(); err == nil {
		t.Fatal("expected Validate to reject module_auto together with module_wsgi")
	}
}

func TestValidateMountPrefix(t *testing.T) {
	cs := &CaddySnake{ModuleWsgi: "main:app", MountPrefix: "no-leading-slash"}
	if err := cs.Validate(); err == nil {
		t.Fatal("expected Validate to reject a mount_prefix without a leading slash")
	}
	// Placeholders are validated per request, not at config load.
	cs = &CaddySnake{ModuleWsgi: "main:app", MountPrefix: "/{http.request.uri.path.0}"}
	if err := cs.Validate(); err != nil {
		t.Fatalf("Validate rejected a placeholder mount_prefix: %v", err)
	}
	cs = &CaddySnake{ModuleWsgi: "main:app", MountPrefix: "/ok"}
	if err := cs.Validate(); err != nil {
		t.Fatalf("Validate rejected a valid mount_prefix: %v", err)
	}
}
