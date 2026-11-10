package caddysnake

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// releases makes <root>/releases/a and /b and a link <root>/current -> a.
func releases(t *testing.T) (root, current, a, b string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses symbolic links, which need administrator rights on Windows")
	}
	root = mkdirDynamicTestDir(t, "pinned")
	a, b = filepath.Join(root, "releases", "a"), filepath.Join(root, "releases", "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	current = filepath.Join(root, "current")
	if err := os.Symlink(a, current); err != nil {
		t.Fatal(err)
	}
	return root, current, pinPath(a), pinPath(b)
}

func switchLink(t *testing.T, link, target string) {
	t.Helper()
	tmp := link + ".new"
	if err := os.Symlink(target, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, link); err != nil {
		t.Fatal(err)
	}
}

func TestPinPath(t *testing.T) {
	_, current, a, b := releases(t)
	if got := pinPath(current); got != a {
		t.Fatalf("pinPath(current) = %q, want %q", got, a)
	}
	if got := pinPath(filepath.Join(current, ".venv")); got != filepath.Join(current, ".venv") && got != filepath.Join(a, ".venv") {
		t.Fatalf("a path that does not exist yet came back as %q", got)
	}
	switchLink(t, current, b)
	if got := pinPath(current); got != b {
		t.Fatalf("after the switch pinPath(current) = %q, want %q", got, b)
	}
	if pinPath("") != "" {
		t.Fatal("an empty path must stay empty")
	}
}

// A worker is started from the release the link points at, not from the link.
func TestDynamicAppStartsFromThePinnedRelease(t *testing.T) {
	_, current, a, _ := releases(t)
	var startedIn atomic.Value
	d, err := NewDynamicApp("main:app", current, "", nil, nil,
		func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			startedIn.Store(dir)
			return &mockAppServer{}, nil
		},
		zap.NewNop(), false, nil, defaultDynamicAppLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Cleanup() })
	if err := d.HandleRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com/", nil)); err != nil {
		t.Fatal(err)
	}
	if got := startedIn.Load(); got != a {
		t.Fatalf("worker started in %q, want the release %q", got, a)
	}
}

// When the link moves while an app is starting, that start is thrown away and
// the request is served by a worker started from the new release.
func TestDynamicAppRestartsWhenTheLinkMovesDuringStart(t *testing.T) {
	_, current, a, b := releases(t)
	var mu sync.Mutex
	var starts []string
	var cleanedOld atomic.Bool
	d, err := NewDynamicApp("main:app", current, "", nil, nil,
		func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			mu.Lock()
			starts = append(starts, dir)
			first := len(starts) == 1
			mu.Unlock()
			if first {
				// A new version is published while this one is still starting.
				switchLink(t, current, b)
				return &mockAppServer{onCleanup: func() { cleanedOld.Store(true) }}, nil
			}
			return &mockAppServer{}, nil
		},
		zap.NewNop(), false, nil, defaultDynamicAppLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Cleanup() })
	if err := d.HandleRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com/", nil)); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	mu.Lock()
	got := append([]string{}, starts...)
	mu.Unlock()
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("starts = %v, want [%s %s]", got, a, b)
	}
	waitFor(t, "the worker of the replaced release to be stopped", func() bool { return cleanedOld.Load() })
	if apps := LoadedDynamicApps(); len(apps) == 0 {
		t.Fatal("no app is loaded after the retry")
	}
}
