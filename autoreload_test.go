package caddysnake

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

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
	a, err := NewAutoreloadableApp(mockApp, tempDir, func() (AppServer, error) { return mockApp, nil }, zap.NewNop())
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
	a, err := NewAutoreloadableApp(mockApp, tempDir, func() (AppServer, error) { return mockApp, nil }, zap.NewNop())
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
