package caddysnake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func TestFindSitePackagesInVenv(t *testing.T) {
	tempDir := t.TempDir()
	venvLibPath := filepath.Join(tempDir, "lib", "python3.12", "site-packages")

	err := os.MkdirAll(venvLibPath, 0755)
	if err != nil {
		t.Fatalf("failed to create test directory structure: %v", err)
	}

	result, err := findSitePackagesInVenv(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := venvLibPath
	if result != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, result)
	}
}

func TestFindSitePackagesInVenv_NoPythonDirectory(t *testing.T) {
	tempDir := t.TempDir()

	_, err := findSitePackagesInVenv(tempDir)
	if err == nil {
		t.Fatalf("expected an error, but got none")
	}

	expectedError := "unable to find a python3.* directory in the venv"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestFindSitePackagesInVenv_NoSitePackages(t *testing.T) {
	tempDir := t.TempDir()
	libPath := filepath.Join(tempDir, "lib", "python3.12")

	err := os.MkdirAll(libPath, 0755)
	if err != nil {
		t.Fatalf("failed to create test directory structure: %v", err)
	}

	_, err = findSitePackagesInVenv(tempDir)
	if err == nil {
		t.Fatalf("expected an error, but got none")
	}

	expectedError := "site-packages directory does not exist"
	if !strings.HasPrefix(err.Error(), expectedError) {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestContainsPlaceholder(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"/home/user/{host.labels.0}", true},
		{"{host.labels.2}_app:app", true},
		{"/var/www/{host}", true},
		{"/home/user/static", false},
		{"main:app", false},
		{"", false},
		{"{", false},
		{"}", false},
		{"{}", true},
		{"no-braces-here", false},
	}
	for _, tt := range tests {
		got := containsPlaceholder(tt.input)
		if got != tt.expected {
			t.Errorf("containsPlaceholder(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestDynamicAppResolveWithoutReplacer(t *testing.T) {
	d, _ := NewDynamicApp("main:app", "/home/{host.labels.0}", "/venvs/{host.labels.0}",
		func(module, dir, venv string) (AppServer, error) {
			return nil, nil
		},
		zap.NewNop(),
		false,
	)

	r := &http.Request{}
	r = r.WithContext(context.Background())

	key, module, dir, venv := d.resolve(r)

	if module != "main:app" {
		t.Errorf("expected module 'main:app', got %q", module)
	}
	if dir != "/home/{host.labels.0}" {
		t.Errorf("expected dir '/home/{host.labels.0}', got %q", dir)
	}
	if venv != "/venvs/{host.labels.0}" {
		t.Errorf("expected venv '/venvs/{host.labels.0}', got %q", venv)
	}
	expectedKey := "main:app|/home/{host.labels.0}|/venvs/{host.labels.0}"
	if key != expectedKey {
		t.Errorf("expected key %q, got %q", expectedKey, key)
	}
}

func TestDynamicAppGetOrCreate(t *testing.T) {
	var createCount int
	mockApp := &mockAppServer{}

	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			createCount++
			return mockApp, nil
		},
		zap.NewNop(),
		false,
	)

	app1, err := d.getOrCreateApp("key1", "main:app", "/home/test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app1 != mockApp {
		t.Error("expected mockApp to be returned")
	}
	if createCount != 1 {
		t.Errorf("expected factory to be called once, got %d", createCount)
	}

	app2, err := d.getOrCreateApp("key1", "main:app", "/home/test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app2 != mockApp {
		t.Error("expected same cached mockApp")
	}
	if createCount != 1 {
		t.Errorf("expected factory to still be called once, got %d", createCount)
	}

	_, err = d.getOrCreateApp("key2", "main:app", "/home/other", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createCount != 2 {
		t.Errorf("expected factory to be called twice, got %d", createCount)
	}
}

func TestDynamicAppCleanup(t *testing.T) {
	var cleanupCount int
	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			return &mockAppServer{onCleanup: func() { cleanupCount++ }}, nil
		},
		zap.NewNop(),
		false,
	)

	_, _ = d.getOrCreateApp("key1", "main:app", "/home/a", "")
	_, _ = d.getOrCreateApp("key2", "main:app", "/home/b", "")

	err := d.Cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanupCount != 2 {
		t.Errorf("expected 2 cleanups, got %d", cleanupCount)
	}

	d.mu.RLock()
	remaining := len(d.apps)
	d.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("expected 0 apps after cleanup, got %d", remaining)
	}
}

// mockAppServer is a simple mock implementing AppServer for testing.
type mockAppServer struct {
	onCleanup       func()
	cleanupErr      error
	onHandleRequest func(w http.ResponseWriter, r *http.Request) error
}

func (m *mockAppServer) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	if m.onHandleRequest != nil {
		return m.onHandleRequest(w, r)
	}
	return nil
}

func (m *mockAppServer) Cleanup() error {
	if m.onCleanup != nil {
		m.onCleanup()
	}
	return m.cleanupErr
}

func TestFindWorkingDirectory(t *testing.T) {
	tempDir := t.TempDir()

	abs, err := findWorkingDirectory(tempDir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if abs != tempDir {
		t.Errorf("expected %q, got %q", tempDir, abs)
	}

	nonExistent := tempDir + "-doesnotexist"
	_, err = findWorkingDirectory(nonExistent)
	if err == nil || !strings.Contains(err.Error(), "working_dir directory does not exist") {
		t.Errorf("expected error for non-existent directory, got: %v", err)
	}

	filePath := filepath.Join(tempDir, "afile.txt")
	os.WriteFile(filePath, []byte("test"), 0644)
	_, err = findWorkingDirectory(filePath)
	if err == nil || !strings.Contains(err.Error(), "working_dir is not a directory") {
		t.Errorf("expected error for file, got: %v", err)
	}
}

// ====================== UnmarshalCaddyfile Tests ======================

func TestUnmarshalCaddyfile_ShorthandWsgi(t *testing.T) {
	input := `python main:app`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.ModuleWsgi != "main:app" {
		t.Errorf("expected ModuleWsgi 'main:app', got %q", cs.ModuleWsgi)
	}
}

func TestUnmarshalCaddyfile_BlockAllOptions(t *testing.T) {
	input := `python {
		module_wsgi main:app
		working_dir /tmp
		venv /tmp/venv
		workers 4
		workers_runtime thread
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.ModuleWsgi != "main:app" {
		t.Errorf("expected ModuleWsgi 'main:app', got %q", cs.ModuleWsgi)
	}
	if cs.WorkingDir != "/tmp" {
		t.Errorf("expected WorkingDir '/tmp', got %q", cs.WorkingDir)
	}
	if cs.VenvPath != "/tmp/venv" {
		t.Errorf("expected VenvPath '/tmp/venv', got %q", cs.VenvPath)
	}
	if cs.Workers != "4" {
		t.Errorf("expected Workers '4', got %q", cs.Workers)
	}
	if cs.WorkersRuntime != "thread" {
		t.Errorf("expected WorkersRuntime 'thread', got %q", cs.WorkersRuntime)
	}
}

func TestUnmarshalCaddyfile_BlockAsgiWithLifespan(t *testing.T) {
	input := `python {
		module_asgi main:app
		lifespan on
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.ModuleAsgi != "main:app" {
		t.Errorf("expected ModuleAsgi 'main:app', got %q", cs.ModuleAsgi)
	}
	if cs.Lifespan != "on" {
		t.Errorf("expected Lifespan 'on', got %q", cs.Lifespan)
	}
}

func TestUnmarshalCaddyfile_LifespanOff(t *testing.T) {
	input := `python {
		module_asgi main:app
		lifespan off
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.Lifespan != "off" {
		t.Errorf("expected Lifespan 'off', got %q", cs.Lifespan)
	}
}

func TestUnmarshalCaddyfile_WorkersRuntimeProcess(t *testing.T) {
	input := `python {
		module_wsgi main:app
		workers_runtime process
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.WorkersRuntime != "process" {
		t.Errorf("expected WorkersRuntime 'process', got %q", cs.WorkersRuntime)
	}
}

func TestUnmarshalCaddyfile_InvalidLifespan(t *testing.T) {
	input := `python {
		module_asgi main:app
		lifespan maybe
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for invalid lifespan, got nil")
	}
}

func TestUnmarshalCaddyfile_InvalidWorkersRuntime(t *testing.T) {
	input := `python {
		module_wsgi main:app
		workers_runtime invalid
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for invalid workers_runtime, got nil")
	}
}

func TestUnmarshalCaddyfile_UnknownSubdirective(t *testing.T) {
	input := `python {
		module_wsgi main:app
		unknown_thing value
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for unknown subdirective, got nil")
	}
	if !strings.Contains(err.Error(), "unknown subdirective") {
		t.Errorf("expected 'unknown subdirective' in error, got: %v", err)
	}
}

func TestUnmarshalCaddyfile_TooManyArgs(t *testing.T) {
	input := `python main:app extra_arg`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for too many args, got nil")
	}
}

func TestUnmarshalCaddyfile_DynamicWorkingDir(t *testing.T) {
	input := `python {
		module_wsgi main:app
		working_dir /home/user/{host.labels.2}
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.WorkingDir != "/home/user/{host.labels.2}" {
		t.Errorf("expected WorkingDir with placeholder, got %q", cs.WorkingDir)
	}
}

func TestUnmarshalCaddyfile_PythonPath(t *testing.T) {
	input := `python {
		module_wsgi main:app
		python_path /usr/local/bin/python3.12
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.PythonPath != "/usr/local/bin/python3.12" {
		t.Errorf("expected PythonPath '/usr/local/bin/python3.12', got %q", cs.PythonPath)
	}
}

// ====================== CaddyModule and Validate Tests ======================

func TestCaddyModule(t *testing.T) {
	info := CaddySnake{}.CaddyModule()
	if info.ID != "http.handlers.python" {
		t.Errorf("expected module ID 'http.handlers.python', got %q", info.ID)
	}
	mod := info.New()
	if _, ok := mod.(*CaddySnake); !ok {
		t.Error("expected New() to return *CaddySnake")
	}
}

func TestValidate(t *testing.T) {
	cs := &CaddySnake{}
	if err := cs.Validate(); err != nil {
		t.Errorf("expected nil error from Validate, got: %v", err)
	}
}

// ====================== CaddySnake.Cleanup Tests ======================

func TestCaddySnakeCleanup_NilApp(t *testing.T) {
	cs := &CaddySnake{logger: zap.NewNop()}
	err := cs.Cleanup()
	if err != nil {
		t.Errorf("expected nil error for nil app, got: %v", err)
	}
}

func TestCaddySnakeCleanup_WithApp(t *testing.T) {
	var cleaned bool
	cs := &CaddySnake{
		logger: zap.NewNop(),
		app:    &mockAppServer{onCleanup: func() { cleaned = true }},
	}
	err := cs.Cleanup()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !cleaned {
		t.Error("expected app.Cleanup to be called")
	}
}

func TestCaddySnakeCleanup_NilReceiver(t *testing.T) {
	var cs *CaddySnake
	err := cs.Cleanup()
	if err != nil {
		t.Errorf("expected nil error for nil receiver, got: %v", err)
	}
}

// ====================== ServeHTTP Tests ======================

type mockNextHandler struct {
	called bool
	err    error
}

func (m *mockNextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	m.called = true
	return m.err
}

func TestServeHTTP_Success(t *testing.T) {
	var handled bool
	app := &mockAppServer{
		onHandleRequest: func(w http.ResponseWriter, r *http.Request) error {
			handled = true
			return nil
		},
	}
	cs := CaddySnake{app: app, logger: zap.NewNop()}
	next := &mockNextHandler{}
	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}

	err := cs.ServeHTTP(w, r, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Error("expected app.HandleRequest to be called")
	}
	if !next.called {
		t.Error("expected next handler to be called")
	}
}

func TestServeHTTP_AppError(t *testing.T) {
	appErr := errors.New("python error")
	app := &mockAppServer{
		onHandleRequest: func(w http.ResponseWriter, r *http.Request) error {
			return appErr
		},
	}
	cs := CaddySnake{app: app, logger: zap.NewNop()}
	next := &mockNextHandler{}
	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}

	err := cs.ServeHTTP(w, r, next)
	if err != appErr {
		t.Errorf("expected app error, got: %v", err)
	}
	if next.called {
		t.Error("next handler should not be called when app returns error")
	}
}

func TestServeHTTP_NextError(t *testing.T) {
	app := &mockAppServer{}
	nextErr := errors.New("next handler error")
	cs := CaddySnake{app: app, logger: zap.NewNop()}
	next := &mockNextHandler{err: nextErr}
	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}

	err := cs.ServeHTTP(w, r, next)
	if err != nextErr {
		t.Errorf("expected next error, got: %v", err)
	}
}

type mockResponseWriter struct {
	headers    http.Header
	body       string
	statusCode int
}

func (m *mockResponseWriter) Header() http.Header {
	return m.headers
}

func (m *mockResponseWriter) Write(data []byte) (int, error) {
	m.body = string(data)
	return len(data), nil
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}

// ====================== resolvePythonInterpreter Tests ======================

func TestResolvePythonInterpreter_ExplicitPath(t *testing.T) {
	result := resolvePythonInterpreter("/usr/local/bin/python3.12", "/some/venv")
	if result != "/usr/local/bin/python3.12" {
		t.Errorf("expected explicit path, got %q", result)
	}
}

func TestResolvePythonInterpreter_VenvPath(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	os.MkdirAll(binDir, 0755)
	pythonPath := filepath.Join(binDir, "python3")
	os.WriteFile(pythonPath, []byte("#!/bin/sh"), 0755)

	result := resolvePythonInterpreter("", tempDir)
	if result != pythonPath {
		t.Errorf("expected venv python %q, got %q", pythonPath, result)
	}
}

func TestResolvePythonInterpreter_Fallback(t *testing.T) {
	result := resolvePythonInterpreter("", "")
	if result != "python3" {
		t.Errorf("expected 'python3' fallback, got %q", result)
	}
}

func TestResolvePythonInterpreter_VenvFallbackToPython(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	os.MkdirAll(binDir, 0755)
	pythonPath := filepath.Join(binDir, "python")
	os.WriteFile(pythonPath, []byte("#!/bin/sh"), 0755)

	result := resolvePythonInterpreter("", tempDir)
	if result != pythonPath {
		t.Errorf("expected venv python %q, got %q", pythonPath, result)
	}
}

// ====================== writeCaddysnakePy Tests ======================

func TestWriteCaddysnakePy(t *testing.T) {
	path, err := writeCaddysnakePy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("expected non-empty file")
	}
}

// ====================== DynamicApp Extended Tests ======================

func TestDynamicAppGetOrCreate_FactoryError(t *testing.T) {
	factoryErr := errors.New("import failed")
	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			return nil, factoryErr
		},
		zap.NewNop(),
		false,
	)

	app, err := d.getOrCreateApp("key1", "main:app", "/home/test", "")
	if err != factoryErr {
		t.Errorf("expected factory error, got: %v", err)
	}
	if app != nil {
		t.Error("expected nil app on error")
	}

	d.mu.RLock()
	if len(d.apps) != 0 {
		t.Errorf("expected 0 apps after factory error, got %d", len(d.apps))
	}
	d.mu.RUnlock()
}

func TestDynamicAppCleanup_WithErrors(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			return &mockAppServer{cleanupErr: cleanupErr}, nil
		},
		zap.NewNop(),
		false,
	)

	_, _ = d.getOrCreateApp("key1", "main:app", "/home/a", "")
	_, _ = d.getOrCreateApp("key2", "main:app", "/home/b", "")

	err := d.Cleanup()
	if err == nil {
		t.Fatal("expected error from cleanup, got nil")
	}
	if !strings.Contains(err.Error(), "cleanup failed") {
		t.Errorf("expected 'cleanup failed' in error, got: %v", err)
	}
}

func TestDynamicAppHandleRequest(t *testing.T) {
	var handledModule, handledDir string
	mockApp := &mockAppServer{
		onHandleRequest: func(w http.ResponseWriter, r *http.Request) error {
			w.WriteHeader(200)
			return nil
		},
	}
	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			handledModule = module
			handledDir = dir
			return mockApp, nil
		},
		zap.NewNop(),
		false,
	)

	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	r = r.WithContext(context.Background())

	err := d.HandleRequest(w, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handledModule != "main:app" {
		t.Errorf("expected module 'main:app', got %q", handledModule)
	}
	if handledDir != "/home/test" {
		t.Errorf("expected dir '/home/test', got %q", handledDir)
	}
}

func TestDynamicAppHandleRequest_FactoryError(t *testing.T) {
	factoryErr := errors.New("import failed")
	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			return nil, factoryErr
		},
		zap.NewNop(),
		false,
	)

	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	r = r.WithContext(context.Background())

	err := d.HandleRequest(w, r)
	if err != factoryErr {
		t.Errorf("expected factory error, got: %v", err)
	}
}

func TestDynamicAppResolveWithReplacer(t *testing.T) {
	d, _ := NewDynamicApp("main:app", "/home/{custom.host}", "",
		func(module, dir, venv string) (AppServer, error) {
			return nil, nil
		},
		zap.NewNop(),
		false,
	)

	repl := caddy.NewReplacer()
	repl.Set("custom.host", "sub1.example.com")
	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, repl)
	r := &http.Request{}
	r = r.WithContext(ctx)

	key, module, dir, venv := d.resolve(r)
	if module != "main:app" {
		t.Errorf("expected module 'main:app', got %q", module)
	}
	if dir != "/home/sub1.example.com" {
		t.Errorf("expected dir '/home/sub1.example.com', got %q", dir)
	}
	if venv != "" {
		t.Errorf("expected empty venv, got %q", venv)
	}
	expectedKey := "main:app|/home/sub1.example.com|"
	if key != expectedKey {
		t.Errorf("expected key %q, got %q", expectedKey, key)
	}
}

func TestDynamicAppResolveMultiplePlaceholders(t *testing.T) {
	d, _ := NewDynamicApp("{custom.module}:app", "/home/{custom.host}", "/venvs/{custom.host}",
		func(module, dir, venv string) (AppServer, error) {
			return nil, nil
		},
		zap.NewNop(),
		false,
	)

	repl := caddy.NewReplacer()
	repl.Set("custom.module", "mymod")
	repl.Set("custom.host", "tenant1")
	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, repl)
	r := &http.Request{}
	r = r.WithContext(ctx)

	key, module, dir, venv := d.resolve(r)
	if module != "mymod:app" {
		t.Errorf("expected module 'mymod:app', got %q", module)
	}
	if dir != "/home/tenant1" {
		t.Errorf("expected dir '/home/tenant1', got %q", dir)
	}
	if venv != "/venvs/tenant1" {
		t.Errorf("expected venv '/venvs/tenant1', got %q", venv)
	}
	expectedKey := "mymod:app|/home/tenant1|/venvs/tenant1"
	if key != expectedKey {
		t.Errorf("expected key %q, got %q", expectedKey, key)
	}
}

func TestDynamicAppConcurrentAccess(t *testing.T) {
	var mu sync.Mutex
	createCount := 0
	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			mu.Lock()
			createCount++
			mu.Unlock()
			return &mockAppServer{}, nil
		},
		zap.NewNop(),
		false,
	)

	const goroutines = 50
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			r := &http.Request{}
			r = r.WithContext(context.Background())
			w := &mockResponseWriter{headers: make(http.Header)}
			errs <- d.HandleRequest(w, r)
		}()
	}

	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Errorf("goroutine returned error: %v", err)
		}
	}

	mu.Lock()
	if createCount != 1 {
		t.Errorf("expected factory to be called once (same key for all), got %d", createCount)
	}
	mu.Unlock()
}

func TestDynamicAppConcurrentDifferentKeys(t *testing.T) {
	var mu sync.Mutex
	createdKeys := make(map[string]bool)
	d, _ := NewDynamicApp("main:app", "", "",
		func(module, dir, venv string) (AppServer, error) {
			mu.Lock()
			createdKeys[module+"|"+dir+"|"+venv] = true
			mu.Unlock()
			return &mockAppServer{}, nil
		},
		zap.NewNop(),
		false,
	)

	const goroutines = 10
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		key := fmt.Sprintf("key%d", i)
		dir := fmt.Sprintf("/home/dir%d", i)
		go func(k, dirPath string) {
			_, err := d.getOrCreateApp(k, "main:app", dirPath, "")
			errs <- err
		}(key, dir)
	}

	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Errorf("goroutine returned error: %v", err)
		}
	}

	mu.Lock()
	if len(createdKeys) != goroutines {
		t.Errorf("expected %d unique apps created, got %d", goroutines, len(createdKeys))
	}
	mu.Unlock()

	d.mu.RLock()
	if len(d.apps) != goroutines {
		t.Errorf("expected %d apps in map, got %d", goroutines, len(d.apps))
	}
	d.mu.RUnlock()
}

// ====================== findPythonDirectory Tests ======================

func TestFindPythonDirectory_Found(t *testing.T) {
	tempDir := t.TempDir()
	os.MkdirAll(filepath.Join(tempDir, "python3.11"), 0755)

	dir, err := findPythonDirectory(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "python3.11" {
		t.Errorf("expected 'python3.11', got %q", dir)
	}
}

func TestFindPythonDirectory_MultipleVersions(t *testing.T) {
	tempDir := t.TempDir()
	os.MkdirAll(filepath.Join(tempDir, "python3.10"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "python3.12"), 0755)

	dir, err := findPythonDirectory(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "python3.10" && dir != "python3.12" {
		t.Errorf("expected 'python3.10' or 'python3.12', got %q", dir)
	}
}

func TestFindPythonDirectory_NotFound(t *testing.T) {
	tempDir := t.TempDir()
	os.MkdirAll(filepath.Join(tempDir, "not-python"), 0755)

	_, err := findPythonDirectory(tempDir)
	if err == nil {
		t.Fatal("expected error for missing python directory")
	}
	if !strings.Contains(err.Error(), "unable to find") {
		t.Errorf("expected 'unable to find' in error, got: %v", err)
	}
}

func TestFindPythonDirectory_EmptyDir(t *testing.T) {
	tempDir := t.TempDir()
	_, err := findPythonDirectory(tempDir)
	if err == nil {
		t.Fatal("expected error for empty directory")
	}
}

func TestFindPythonDirectory_NonExistentPath(t *testing.T) {
	_, err := findPythonDirectory("/nonexistent/path/to/lib")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestFindPythonDirectory_FileNotDir(t *testing.T) {
	tempDir := t.TempDir()
	os.WriteFile(filepath.Join(tempDir, "python3.11"), []byte("not a dir"), 0644)

	_, err := findPythonDirectory(tempDir)
	if err == nil {
		t.Fatal("expected error when python3.x is a file, not directory")
	}
}

// ====================== Additional Tests ======================

func TestUnmarshalCaddyfile_ModuleAsgiMissingArg(t *testing.T) {
	input := `python {
		module_asgi
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for missing module_asgi argument")
	}
}

func TestUnmarshalCaddyfile_ModuleWsgiMissingArg(t *testing.T) {
	input := `python {
		module_wsgi
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for missing module_wsgi argument")
	}
}

func TestUnmarshalCaddyfile_WorkingDirMissingArg(t *testing.T) {
	input := `python {
		module_wsgi main:app
		working_dir
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for missing working_dir argument")
	}
}

func TestUnmarshalCaddyfile_VenvMissingArg(t *testing.T) {
	input := `python {
		module_wsgi main:app
		venv
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for missing venv argument")
	}
}

func TestUnmarshalCaddyfile_WorkersMissingArg(t *testing.T) {
	input := `python {
		module_wsgi main:app
		workers
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for missing workers argument")
	}
}

func TestUnmarshalCaddyfile_LifespanMissingArg(t *testing.T) {
	input := `python {
		module_asgi main:app
		lifespan
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for missing lifespan argument")
	}
}

func TestUnmarshalCaddyfile_WorkersRuntimeMissingArg(t *testing.T) {
	input := `python {
		module_wsgi main:app
		workers_runtime
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("expected error for missing workers_runtime argument")
	}
}

func TestFindSitePackagesInVenv_SitePackagesIsFile(t *testing.T) {
	tempDir := t.TempDir()
	pythonDir := filepath.Join(tempDir, "lib", "python3.12")
	err := os.MkdirAll(pythonDir, 0755)
	if err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
	sitePackagesPath := filepath.Join(pythonDir, "site-packages")
	os.WriteFile(sitePackagesPath, []byte("not a dir"), 0644)

	_, err = findSitePackagesInVenv(tempDir)
	if err == nil {
		t.Fatal("expected error when site-packages is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' in error, got: %v", err)
	}
}

func TestDynamicAppGetOrCreate_DoubleCheckPath(t *testing.T) {
	factoryCalls := int32(0)
	mockApp := &mockAppServer{}

	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			atomic.AddInt32(&factoryCalls, 1)
			time.Sleep(10 * time.Millisecond)
			return mockApp, nil
		},
		zap.NewNop(),
		false,
	)

	const goroutines = 50
	var barrier sync.WaitGroup
	barrier.Add(1)
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			barrier.Wait()
			_, err := d.getOrCreateApp("key1", "main:app", "/home/test", "")
			errs <- err
		}()
	}

	barrier.Done()

	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if calls := atomic.LoadInt32(&factoryCalls); calls != 1 {
		t.Errorf("expected factory to be called once, got %d", calls)
	}
}

func TestDynamicAppCleanup_EmptyApps(t *testing.T) {
	d, _ := NewDynamicApp("main:app", "", "",
		func(module, dir, venv string) (AppServer, error) {
			return &mockAppServer{}, nil
		},
		zap.NewNop(),
		false,
	)

	err := d.Cleanup()
	if err != nil {
		t.Errorf("expected nil error for empty cleanup, got: %v", err)
	}
}

// ====================== waitForPortFile Tests ======================

func TestWaitForPortFile_Success(t *testing.T) {
	f, err := os.CreateTemp("", "portfile-*")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString("9090"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	port, err := waitForPortFile(path, 2*time.Second)
	if err != nil {
		t.Errorf("waitForPortFile: %v", err)
	}
	if port != 9090 {
		t.Errorf("expected port 9090, got %d", port)
	}
}

func TestWaitForPortFile_Timeout(t *testing.T) {
	f, err := os.CreateTemp("", "portfile-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()
	// Empty file - will never have valid port

	_, err = waitForPortFile(f.Name(), 100*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !strings.Contains(err.Error(), "not ready within") {
		t.Errorf("expected 'not ready within' in error, got: %v", err)
	}
}

func TestWaitForPortFile_InvalidContent(t *testing.T) {
	f, err := os.CreateTemp("", "portfile-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("not-a-port")
	f.Close()

	_, err = waitForPortFile(f.Name(), 100*time.Millisecond)
	if err == nil {
		t.Error("expected error for invalid port content")
	}
}

// ====================== PythonWorkerGroup Tests ======================

func TestPythonWorkerGroup_Cleanup_NilReceiver(t *testing.T) {
	var wg *PythonWorkerGroup
	err := wg.Cleanup()
	if err != nil {
		t.Errorf("expected nil error for nil receiver, got: %v", err)
	}
}

func TestPythonWorkerGroup_Cleanup_EmptyWorkers(t *testing.T) {
	wg := &PythonWorkerGroup{
		Workers:    []*PythonWorker{},
		ScriptPath: "",
	}
	err := wg.Cleanup()
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

// ====================== parsePythonDirective Tests ======================

func TestParsePythonDirective(t *testing.T) {
	d := caddyfile.NewTestDispenser("python main:app")
	h := httpcaddyfile.Helper{Dispenser: d}
	handler, err := parsePythonDirective(h)
	if err != nil {
		t.Fatalf("parsePythonDirective: %v", err)
	}
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
	cs, ok := handler.(CaddySnake)
	if !ok {
		t.Fatalf("expected CaddySnake, got %T", handler)
	}
	if cs.ModuleWsgi != "main:app" {
		t.Errorf("expected ModuleWsgi 'main:app', got %q", cs.ModuleWsgi)
	}
}

func TestParsePythonDirective_Block(t *testing.T) {
	d := caddyfile.NewTestDispenser(`python {
		module_wsgi main:app
		working_dir /tmp
		workers 2
	}`)
	h := httpcaddyfile.Helper{Dispenser: d}
	handler, err := parsePythonDirective(h)
	if err != nil {
		t.Fatalf("parsePythonDirective: %v", err)
	}
	cs, ok := handler.(CaddySnake)
	if !ok {
		t.Fatalf("expected CaddySnake, got %T", handler)
	}
	if cs.ModuleWsgi != "main:app" {
		t.Errorf("expected ModuleWsgi 'main:app', got %q", cs.ModuleWsgi)
	}
	if cs.WorkingDir != "/tmp" {
		t.Errorf("expected WorkingDir '/tmp', got %q", cs.WorkingDir)
	}
	if cs.Workers != "2" {
		t.Errorf("expected Workers '2', got %q", cs.Workers)
	}
}

func TestParsePythonDirective_ReturnsMiddlewareHandler(t *testing.T) {
	d := caddyfile.NewTestDispenser("python main:app")
	h := httpcaddyfile.Helper{Dispenser: d}
	handler, err := parsePythonDirective(h)
	if err != nil {
		t.Fatalf("parsePythonDirective: %v", err)
	}
	if _, ok := handler.(caddyhttp.MiddlewareHandler); !ok {
		t.Errorf("handler does not implement caddyhttp.MiddlewareHandler")
	}
}

func TestDynamicAppResolveWithNilReplacer(t *testing.T) {
	d, _ := NewDynamicApp("main:app", "/home/static", "",
		func(module, dir, venv string) (AppServer, error) {
			return nil, nil
		},
		zap.NewNop(),
		false,
	)

	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, (*caddy.Replacer)(nil))
	r := &http.Request{}
	r = r.WithContext(ctx)

	_, module, dir, _ := d.resolve(r)
	if module != "main:app" {
		t.Errorf("expected 'main:app', got %q", module)
	}
	if dir != "/home/static" {
		t.Errorf("expected '/home/static', got %q", dir)
	}
}

// ====================== Integration Tests: Real Python Workers ======================

func skipIfNoPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not found in PATH, skipping integration test: %v", err)
	}
}

const minimalWSGIApp = `def app(environ, start_response):
    start_response('200 OK', [('Content-Type', 'text/plain')])
    return [b'Hello from Python']
`

const minimalASGIApp = `async def app(scope, receive, send):
    await send({"type": "http.response.start", "status": 200, "headers": [[b"content-type", b"text/plain"]]})
    await send({"type": "http.response.body", "body": b"Hello from ASGI"})
`

func TestPythonWorkerGroup_LoadsAndServesWSGI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	skipIfNoPython(t)

	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "app.py")
	if err := os.WriteFile(appPath, []byte(minimalWSGIApp), 0644); err != nil {
		t.Fatalf("failed to write app.py: %v", err)
	}

	wg, err := NewPythonWorkerGroup("wsgi", "app:app", tempDir, "", "", 1, "python3")
	if err != nil {
		t.Fatalf("NewPythonWorkerGroup failed: %v", err)
	}
	defer wg.Cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = wg.HandleRequest(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go server.Serve(listener)
	defer server.Close()

	baseURL := "http://" + listener.Addr().String()
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d; body: %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("Hello from Python")) {
		t.Errorf("expected body to contain 'Hello from Python', got: %s", body)
	}
}

func TestPythonWorkerGroup_LoadsAndServesASGI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	skipIfNoPython(t)

	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "app.py")
	if err := os.WriteFile(appPath, []byte(minimalASGIApp), 0644); err != nil {
		t.Fatalf("failed to write app.py: %v", err)
	}

	wg, err := NewPythonWorkerGroup("asgi", "app:app", tempDir, "", "", 1, "python3")
	if err != nil {
		t.Fatalf("NewPythonWorkerGroup failed: %v", err)
	}
	defer wg.Cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = wg.HandleRequest(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go server.Serve(listener)
	defer server.Close()

	baseURL := "http://" + listener.Addr().String()
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d; body: %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("Hello from ASGI")) {
		t.Errorf("expected body to contain 'Hello from ASGI', got: %s", body)
	}
}
