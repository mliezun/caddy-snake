package caddysnake

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
)

func collectOutput(t *testing.T) (get func() []WorkerOutput) {
	t.Helper()
	var mu sync.Mutex
	var got []WorkerOutput
	cancel := ObserveWorkerOutput(func(o WorkerOutput) {
		mu.Lock()
		got = append(got, o)
		mu.Unlock()
	})
	t.Cleanup(cancel)
	return func() []WorkerOutput {
		mu.Lock()
		defer mu.Unlock()
		return append([]WorkerOutput(nil), got...)
	}
}

func TestOutputTee_PassesThroughAndSplitsLines(t *testing.T) {
	get := collectOutput(t)
	var dst bytes.Buffer
	tee := newOutputTee(&dst, "/srv/apps/inventory", "main:app", "stderr")

	for _, chunk := range []string{"Traceback (most recent", " call last):\r\n  File x\npart", "ial"} {
		if n, err := tee.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got := dst.String(); got != "Traceback (most recent call last):\r\n  File x\npartial" {
		t.Fatalf("destination got %q", got)
	}
	lines := get()
	if len(lines) != 2 {
		t.Fatalf("observed %d lines, want 2 (the unterminated one is held back): %+v", len(lines), lines)
	}
	if lines[0].Line != "Traceback (most recent call last):" || lines[1].Line != "  File x" {
		t.Fatalf("lines = %q, %q", lines[0].Line, lines[1].Line)
	}
	if lines[0].WorkingDir != "/srv/apps/inventory" || lines[0].App != "main:app" || lines[0].Stream != "stderr" {
		t.Fatalf("metadata = %+v", lines[0])
	}

	_, _ = tee.Write([]byte(" line\n"))
	if lines = get(); len(lines) != 3 || lines[2].Line != "partial line" {
		t.Fatalf("after completing the line: %+v", lines)
	}
}

func TestOutputTee_LongLineIsFlushedAndCancelStops(t *testing.T) {
	var mu sync.Mutex
	var n int
	cancel := ObserveWorkerOutput(func(WorkerOutput) { mu.Lock(); n++; mu.Unlock() })
	tee := newOutputTee(&bytes.Buffer{}, "/x", "a:b", "stdout")
	_, _ = tee.Write(bytes.Repeat([]byte("x"), maxObservedLine+1))
	mu.Lock()
	first := n
	mu.Unlock()
	if first != 1 {
		t.Fatalf("oversized unterminated line emitted %d times, want 1", first)
	}
	cancel()
	_, _ = tee.Write([]byte("after cancel\n"))
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Fatalf("observer still called after cancel (%d calls)", n)
	}
}

func TestParseMemoryLimit(t *testing.T) {
	if n, err := parseMemoryLimit(""); err != nil || n != 0 {
		t.Fatalf("empty = %d, %v", n, err)
	}
	if n, err := parseMemoryLimit("256MiB"); err != nil || n != 256<<20 {
		t.Fatalf("256MiB = %d, %v", n, err)
	}
	for _, bad := range []string{"0", "lots", "-1MB"} {
		if _, err := parseMemoryLimit(bad); err == nil {
			t.Errorf("parseMemoryLimit(%q) expected error", bad)
		}
	}
}

func TestProcessMemoryBytes_Self(t *testing.T) {
	n, err := processMemoryBytes(os.Getpid())
	if err != nil {
		t.Skipf("process memory not measurable here: %v", err)
	}
	if n < 1<<20 {
		t.Fatalf("own memory = %d bytes, expected at least 1 MiB", n)
	}
}

// memApp is an app server that reports a configurable amount of memory.
type memApp struct {
	mockAppServer
	mem atomic.Int64
}

func (m *memApp) MemoryBytes() (int64, error) { return m.mem.Load(), nil }

func newMemDynamicApp(t *testing.T, limits DynamicAppLimits) (d *DynamicApp, dir string, created *[]*memApp, cleaned *atomic.Int32) {
	t.Helper()
	dir = mkdirDynamicTestDir(t, "inventory")
	cleaned = &atomic.Int32{}
	var mu sync.Mutex
	list := []*memApp{}
	d, err := NewDynamicApp("main:app", dir, "", nil, nil,
		func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			app := &memApp{}
			app.onCleanup = func() { cleaned.Add(1) }
			app.mem.Store(10 << 20)
			mu.Lock()
			list = append(list, app)
			mu.Unlock()
			return app, nil
		},
		zap.NewNop(), false, nil, limits,
	)
	if err != nil {
		t.Fatalf("NewDynamicApp: %v", err)
	}
	t.Cleanup(func() { _ = d.Cleanup() })
	return d, dir, &list, cleaned
}

func hit(t *testing.T, d *DynamicApp) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err := d.HandleRequest(httptest.NewRecorder(), r); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
}

func TestLoadedDynamicAppsAndReloadWorkingDir(t *testing.T) {
	d, dir, created, cleaned := newMemDynamicApp(t, defaultDynamicAppLimits())
	hit(t, d)

	resolved := resolveDirBestEffort(dir)
	var found *DynamicAppInfo
	for _, info := range LoadedDynamicApps() {
		if info.WorkingDir == resolved {
			info := info
			found = &info
		}
	}
	if found == nil {
		t.Fatalf("LoadedDynamicApps did not list %s", resolved)
	}
	if found.MemoryBytes != 10<<20 || found.ActiveRequests != 0 {
		t.Fatalf("info = %+v", *found)
	}

	// Unrelated directories are left alone.
	if n := ReloadWorkingDir(mkdirDynamicTestDir(t, "other")); n != 0 {
		t.Fatalf("ReloadWorkingDir(other) = %d, want 0", n)
	}
	// The parent of the app directory matches everything under it.
	parent := dir[:strings.LastIndex(dir, string(os.PathSeparator))]
	if n := ReloadWorkingDir(parent); n != 1 {
		t.Fatalf("ReloadWorkingDir(parent) = %d, want 1", n)
	}
	waitFor(t, "old instance cleanup", func() bool { return cleaned.Load() == 1 })

	hit(t, d)
	if len(*created) != 2 {
		t.Fatalf("expected the app to be created again after reload, got %d instances", len(*created))
	}

	_ = d.Cleanup()
	for _, info := range LoadedDynamicApps() {
		if info.WorkingDir == resolved {
			t.Fatal("app still listed after its handler was cleaned up")
		}
	}
}

func TestMemoryLimitRestartsOnlyAppsOverTheLimit(t *testing.T) {
	get := collectOutput(t)
	limits := defaultDynamicAppLimits()
	d, dir, created, cleaned := newMemDynamicApp(t, limits)
	hit(t, d)

	d.enforceMemoryLimit(64 << 20)
	time.Sleep(50 * time.Millisecond)
	if cleaned.Load() != 0 {
		t.Fatal("app under the limit was restarted")
	}

	(*created)[0].mem.Store(200 << 20)
	d.enforceMemoryLimit(64 << 20)
	waitFor(t, "restart of the app over its limit", func() bool { return cleaned.Load() == 1 })

	var notice *WorkerOutput
	for _, o := range get() {
		if o.Stream == "caddy-snake" && o.WorkingDir == resolveDirBestEffort(dir) {
			o := o
			notice = &o
		}
	}
	if notice == nil || !strings.Contains(notice.Line, "memory_limit") {
		t.Fatalf("expected a caddy-snake notice about memory_limit, got %+v", notice)
	}

	hit(t, d)
	if len(*created) != 2 {
		t.Fatalf("expected a fresh instance after the restart, got %d", len(*created))
	}
}

func TestMemoryLimitMonitorRuns(t *testing.T) {
	t.Setenv(envMemoryCheckInterval, "20ms")
	limits := defaultDynamicAppLimits()
	limits.MemoryLimitBytes = 64 << 20
	d, _, created, cleaned := newMemDynamicApp(t, limits)
	hit(t, d)
	(*created)[0].mem.Store(500 << 20)
	waitFor(t, "the background monitor to restart the app", func() bool { return cleaned.Load() == 1 })
}

func TestMemoryLimitConfig(t *testing.T) {
	d := caddyfile.NewTestDispenser(`python {
		module_auto "main:app"
		working_dir "/srv/apps/{http.request.uri.path.0}"
		memory_limit 256MiB
	}`)
	var cs CaddySnake
	if err := cs.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile: %v", err)
	}
	if cs.MemoryLimit != "256MiB" {
		t.Fatalf("MemoryLimit = %q", cs.MemoryLimit)
	}
	if err := cs.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	bad := &CaddySnake{ModuleWsgi: "main:app", MemoryLimit: "plenty"}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected Validate to reject an unparseable memory_limit")
	}

	// A fixed (non-dynamic) app cannot be restarted, so the option is refused.
	static := &CaddySnake{ModuleWsgi: "main:app", MemoryLimit: "256MiB", Workers: "1"}
	ctx, cancel := caddy.NewContext(caddy.Context{Context: t.Context()})
	defer cancel()
	err := static.Provision(ctx)
	if err == nil {
		_ = static.Cleanup()
		t.Fatal("expected Provision to reject memory_limit without placeholders")
	}
	if !strings.Contains(err.Error(), "memory_limit requires dynamic module loading") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRealWorkerOutputAndMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a Python worker")
	}
	get := collectOutput(t)
	dir := t.TempDir()
	src := "import sys\nprint('hello from import', file=sys.stderr)\n\n" +
		"def app(environ, start_response):\n" +
		"    start_response('200 OK', [('Content-Type', 'text/plain')])\n" +
		"    return [b'ok']\n"
	if err := os.WriteFile(dir+"/main.py", []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	wg, err := NewPythonWorkerGroup("auto", "main:app", dir, "", "", "", 1, "python3", "", "", nil, nil, 0, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewPythonWorkerGroup: %v", err)
	}
	defer func() { _ = wg.Cleanup() }()

	waitFor(t, "the worker's stderr line to reach the observer", func() bool {
		for _, o := range get() {
			if o.Line == "hello from import" && o.Stream == "stderr" && o.App == "main:app" {
				return true
			}
		}
		return false
	})

	mem, err := wg.MemoryBytes()
	if err != nil {
		t.Fatalf("MemoryBytes: %v", err)
	}
	if mem < 2<<20 || mem > 2<<30 {
		t.Fatalf("worker memory = %d bytes, outside any plausible range", mem)
	}

	rec := httptest.NewRecorder()
	if err := wg.HandleRequest(rec, httptest.NewRequest(http.MethodGet, "http://example.com/", nil)); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("auto-detected WSGI app returned %d %q", rec.Code, rec.Body.String())
	}
}
