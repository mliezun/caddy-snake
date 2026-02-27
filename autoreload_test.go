package caddysnake

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	var cleaned bool
	mockApp := &mockAppServer{onCleanup: func() { cleaned = true }}

	tempDir := t.TempDir()
	a, err := NewAutoreloadableApp(mockApp, tempDir, func() (AppServer, error) { return mockApp, nil }, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}

	err = a.Cleanup()
	if err != nil {
		t.Errorf("Cleanup: %v", err)
	}
	if !cleaned {
		t.Error("expected underlying app Cleanup to be called")
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

func TestErrorApp_Returns500(t *testing.T) {
	appErr := errors.New("syntax error in app.py")
	ea := &errorApp{err: appErr}

	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	err := ea.HandleRequest(w, r)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if w.statusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.statusCode)
	}
	if !strings.Contains(w.body, "syntax error in app.py") {
		t.Errorf("expected error message in body, got: %s", w.body)
	}
}

func TestErrorApp_Cleanup(t *testing.T) {
	ea := &errorApp{err: errors.New("test")}
	if err := ea.Cleanup(); err != nil {
		t.Errorf("expected nil error from Cleanup, got: %v", err)
	}
}

func TestAutoreloadableApp_ReloadFailure_FallsBackToErrorApp(t *testing.T) {
	mockApp := &mockAppServer{}
	reloadErr := errors.New("syntax error")
	a, err := NewAutoreloadableApp(mockApp, t.TempDir(), func() (AppServer, error) {
		return nil, reloadErr
	}, zap.NewNop(), nil) // nil exitOnReloadFailure
	if err != nil {
		t.Fatalf("NewAutoreloadableApp: %v", err)
	}
	defer a.Cleanup()

	a.reload()

	// After failed reload, requests should get 500
	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	err = a.HandleRequest(w, r)
	if err != nil {
		t.Errorf("HandleRequest: %v", err)
	}
	if w.statusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.statusCode)
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
	if w.statusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 after failed reload, got %d", w.statusCode)
	}

	// Second reload succeeds (developer fixed the error)
	a.reload()
	w2 := &mockResponseWriter{headers: make(http.Header)}
	a.HandleRequest(w2, &http.Request{})
	if w2.statusCode != 200 {
		t.Errorf("expected 200 after recovery, got %d", w2.statusCode)
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
