package caddysnake

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

func TestAutoreloadableApp_HandleRequest(t *testing.T) {
	var handled bool
	mockApp := &mockAppServer{
		onHandleRequest: func(w http.ResponseWriter, r *http.Request) error {
			handled = true
			w.WriteHeader(200)
			return nil
		},
	}

	tempDir := t.TempDir()
	a, err := NewAutoreloadableApp(mockApp, tempDir, func() (AppServer, error) { return mockApp, nil }, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	err = a.HandleRequest(w, r)
	if err != nil {
		t.Errorf("HandleRequest: %v", err)
	}
	if !handled {
		t.Error("expected mock app HandleRequest to be called")
	}
}

func TestAutoreloadableApp_Cleanup(t *testing.T) {
	var cleanupCount int
	mockApp := &mockAppServer{onCleanup: func() { cleanupCount++ }}

	tempDir := t.TempDir()
	a, err := NewAutoreloadableApp(mockApp, tempDir, func() (AppServer, error) { return mockApp, nil }, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}

	err = a.Cleanup()
	if err != nil {
		t.Errorf("Cleanup: %v", err)
	}
	if err := a.Cleanup(); err != nil {
		t.Errorf("second Cleanup: %v", err)
	}
	if cleanupCount != 1 {
		t.Errorf("expected one underlying cleanup, got %d", cleanupCount)
	}
}

func TestIsPythonFileEvent(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		op     fsnotify.Op
		expect bool
	}{
		{"py write", "/tmp/app.py", fsnotify.Write, true},
		{"py create", "/tmp/foo.py", fsnotify.Create, true},
		{"py remove", "/x/y.py", fsnotify.Remove, true},
		{"py rename", "/a/b/c.py", fsnotify.Rename, true},
		{"txt write", "/tmp/app.txt", fsnotify.Write, false},
		{"no ext", "/tmp/script", fsnotify.Write, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := fsnotify.Event{Name: tt.path, Op: tt.op}
			got := isPythonFileEvent(ev)
			if got != tt.expect {
				t.Errorf("isPythonFileEvent(%q, %v) = %v, want %v", tt.path, tt.op, got, tt.expect)
			}
		})
	}
}

func TestHandleNewDirEvent_NotCreate(t *testing.T) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()
	// handleNewDirEvent with Write event should return early without adding
	ev := fsnotify.Event{Name: "/tmp/foo", Op: fsnotify.Write}
	handleNewDirEvent(ev, watcher)
	// No panic and no-op
}

func TestHandleNewDirEvent_CreateFile(t *testing.T) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()
	// Create event for a file (not dir) - should not add
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	ev := fsnotify.Event{Name: f, Op: fsnotify.Create}
	handleNewDirEvent(ev, watcher)
	// No panic - file is not a dir so it returns early
}

func TestAutoreloadableApp_ReloadFailure_TerminatesWhenExitFuncSet(t *testing.T) {
	var exitCode int
	exitCalled := make(chan struct{})
	exitFunc := func(code int) {
		exitCode = code
		close(exitCalled)
	}

	mockApp := &mockAppServer{}
	reloadErr := errors.New("app deleted")
	a, err := NewAutoreloadableApp(mockApp, t.TempDir(), func() (AppServer, error) {
		return nil, reloadErr
	}, zap.NewNop(), exitFunc)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	// Trigger reload by calling reload() directly (simulating file change after debounce)
	a.reload()

	select {
	case <-exitCalled:
		if exitCode != 1 {
			t.Errorf("expected exit code 1, got %d", exitCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exitOnReloadFailure was not called after reload failure")
	}
}

func TestErrorApp_Returns503(t *testing.T) {
	appErr := errors.New("syntax error in app.py")
	ea := &errorApp{err: appErr}

	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	err := ea.HandleRequest(w, r)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if w.statusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.statusCode)
	}
	if w.body != "Service temporarily unavailable" {
		t.Errorf("expected generic unavailable message, got: %s", w.body)
	}
}

func TestErrorApp_Cleanup(t *testing.T) {
	ea := &errorApp{err: errors.New("test")}
	if err := ea.Cleanup(); err != nil {
		t.Errorf("expected nil error from Cleanup, got: %v", err)
	}
}

func TestAutoreloadableApp_ReloadFailure_FallsBackToErrorApp(t *testing.T) {
	cleaned := false
	mockApp := &mockAppServer{onCleanup: func() { cleaned = true }}
	reloadErr := errors.New("syntax error")
	a, err := NewAutoreloadableApp(mockApp, t.TempDir(), func() (AppServer, error) {
		return nil, reloadErr
	}, zap.NewNop(), nil) // nil exitOnReloadFailure
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	a.reload()

	// After failed reload, requests should get 503
	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	err = a.HandleRequest(w, r)
	if err != nil {
		t.Errorf("HandleRequest: %v", err)
	}
	if w.statusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.statusCode)
	}
	if !cleaned {
		t.Error("expected replaced app to be cleaned after reload failure")
	}
}

func TestAutoreloadableApp_ReloadRecovery(t *testing.T) {
	mockApp := &mockAppServer{
		onHandleRequest: func(w http.ResponseWriter, r *http.Request) error {
			w.WriteHeader(200)
			w.Write([]byte("recovered"))
			return nil
		},
	}
	failFirst := true
	a, err := NewAutoreloadableApp(&mockAppServer{}, t.TempDir(), func() (AppServer, error) {
		if failFirst {
			failFirst = false
			return nil, errors.New("temporary failure")
		}
		return mockApp, nil
	}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	// First reload fails
	a.reload()
	w := &mockResponseWriter{headers: make(http.Header)}
	a.HandleRequest(w, &http.Request{})
	if w.statusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 after failed reload, got %d", w.statusCode)
	}

	// Second reload succeeds (developer fixed the error)
	a.reload()
	w2 := &mockResponseWriter{headers: make(http.Header)}
	a.HandleRequest(w2, &http.Request{})
	if w2.statusCode != 200 {
		t.Errorf("expected 200 after recovery, got %d", w2.statusCode)
	}
}

func TestAutoreloadableApp_SerializesReloads(t *testing.T) {
	var active atomic.Int32
	var maxActive atomic.Int32
	factory := func() (AppServer, error) {
		current := active.Add(1)
		for {
			seen := maxActive.Load()
			if current <= seen || maxActive.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		active.Add(-1)
		return &mockAppServer{}, nil
	}

	a, err := NewAutoreloadableApp(&mockAppServer{}, t.TempDir(), factory, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			a.reload()
		}()
	}
	wg.Wait()

	if got := maxActive.Load(); got != 1 {
		t.Fatalf("expected one factory call at a time, got %d", got)
	}
}

func TestAutoreloadableApp_FileChangeTriggersReload(t *testing.T) {
	reloadCalled := make(chan struct{}, 1)
	var reloadCount int32

	mockApp := &mockAppServer{}
	tempDir := t.TempDir()

	a, err := NewAutoreloadableApp(mockApp, tempDir, func() (AppServer, error) {
		atomic.AddInt32(&reloadCount, 1)
		select {
		case reloadCalled <- struct{}{}:
		default:
		}
		return mockApp, nil
	}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	// Write a .py file to trigger the watcher
	pyFile := filepath.Join(tempDir, "test_trigger.py")
	if err := os.WriteFile(pyFile, []byte("x = 1"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	select {
	case <-reloadCalled:
		// reload was triggered by file change
	case <-time.After(5 * time.Second):
		t.Fatal("expected reload to be triggered by .py file change")
	}
}

// A working_dir that is itself a symlink is the normal shape of a
// release-directory deploy (`releases/active -> releases/main`). filepath.Walk
// lstat()s its root and does not descend into a symlink, so watching such a
// root used to add zero watches and autoreload silently never fired.
func TestAutoreloadableApp_FileChangeTriggersReload_SymlinkedWorkingDir(t *testing.T) {
	reloadCalled := make(chan struct{}, 1)

	mockApp := &mockAppServer{}
	base := t.TempDir()
	target := filepath.Join(base, "release")
	nested := filepath.Join(target, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	link := filepath.Join(base, "active")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	a, err := NewAutoreloadableApp(mockApp, link, func() (AppServer, error) {
		select {
		case reloadCalled <- struct{}{}:
		default:
		}
		return mockApp, nil
	}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	// Write through the real path, as a deploy does — the watcher must have
	// followed the link to see it, including into subdirectories.
	pyFile := filepath.Join(nested, "test_trigger.py")
	if err := os.WriteFile(pyFile, []byte("x = 1"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	select {
	case <-reloadCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("expected reload to be triggered through a symlinked working_dir")
	}

	// Pin HOW it resolved, not just that it fired. Without this, a "fix" that
	// watched the link's parent directory, or that followed symlinks inside the
	// tree, would also pass. The watch list must be exactly the resolved target
	// and its subdirectory.
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	want := map[string]bool{
		resolvedTarget:                       true,
		filepath.Join(resolvedTarget, "pkg"): true,
	}
	got := a.watcher.WatchList()
	if len(got) != len(want) {
		t.Fatalf("watch list = %v, want exactly %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected watched path %q, want one of %v", p, want)
		}
	}
}

// A symlink ANYWHERE in the working_dir's ancestry is enough to make the
// watched path differ from the configured one, because EvalSymlinks resolves
// every component — macOS /var and /tmp, a symlinked /home or /srv, a
// container's /app. Nothing here is a symlinked working_dir, and it must still
// reload. This is the case that silently regressed dynamic apps, and the one a
// t.TempDir()-only test cannot see on Linux CI.
func TestAutoreloadableApp_FileChangeTriggersReload_SymlinkedAncestor(t *testing.T) {
	reloadCalled := make(chan struct{}, 1)

	mockApp := &mockAppServer{}
	base := t.TempDir()
	realParent := filepath.Join(base, "real-parent")
	workingDir := filepath.Join(realParent, "app")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkParent := filepath.Join(base, "link-parent")
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	// The configured working_dir is a plain directory; only its PARENT is a link.
	configured := filepath.Join(linkParent, "app")

	a, err := NewAutoreloadableApp(mockApp, configured, func() (AppServer, error) {
		select {
		case reloadCalled <- struct{}{}:
		default:
		}
		return mockApp, nil
	}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	if err := os.WriteFile(filepath.Join(workingDir, "t.py"), []byte("x = 1"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	select {
	case <-reloadCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("expected reload through a symlinked ancestor")
	}
}

// A broken symlink must fall back to the literal path rather than panic, and
// must leave the app serving.
func TestAutoreloadableApp_BrokenSymlinkWorkingDir(t *testing.T) {
	mockApp := &mockAppServer{}
	base := t.TempDir()
	link := filepath.Join(base, "active")
	if err := os.Symlink(filepath.Join(base, "does-not-exist"), link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	a, err := NewAutoreloadableApp(mockApp, link, func() (AppServer, error) {
		return mockApp, nil
	}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp should tolerate a broken working_dir link: %v", err)
	}
	defer a.Cleanup()

	if watched := a.watcher.WatchList(); len(watched) != 0 {
		t.Errorf("expected no watches for a broken symlink, got %v", watched)
	}
}

// DynamicApp matches fsnotify events against its dirToKeys map to decide which
// tenant to reload. The watcher is rooted at the RESOLVED working directory, so
// events carry the resolved prefix; if dirToKeys is keyed off the unresolved
// path instead, every event is dropped and dynamic autoreload silently does
// nothing while still logging "autoreload enabled". This test fails both when
// the root is not resolved at all (zero watches) and when it is resolved on
// only one of the two sides (watches, but no attribution).
func TestDynamicApp_AutoreloadThroughSymlinkedWorkingDir(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "release")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	link := filepath.Join(base, "active")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	d, err := NewDynamicApp("main:app", link, "", nil, nil,
		func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			return &mockAppServer{}, nil
		},
		zap.NewNop(),
		true, // autoreload
		nil, defaultDynamicAppLimits(),
	)
	if err != nil {
		t.Fatalf("NewDynamicApp: %v", err)
	}
	defer d.Cleanup()

	if _, err := d.getOrCreateApp("key1", "main:app", link, "", nil, nil); err != nil {
		t.Fatalf("getOrCreateApp: %v", err)
	}

	// Write through the real path, as a deploy does.
	if err := os.WriteFile(filepath.Join(target, "t.py"), []byte("x = 1"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A reload evicts the cached app for that directory (cleanup is async).
	deadline := time.Now().Add(5 * time.Second)
	for {
		d.mu.RLock()
		_, stillCached := d.apps["key1"]
		d.mu.RUnlock()
		if !stillCached {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("expected the dynamic app to be evicted by autoreload through a symlinked working_dir")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
