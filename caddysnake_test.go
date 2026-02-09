package caddysnake

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
)

func TestFindSitePackagesInVenv(t *testing.T) {
	// Set up a temporary directory for the virtual environment simulation
	tempDir := t.TempDir()
	venvLibPath := filepath.Join(tempDir, "lib", "python3.12", "site-packages")

	// Create the directory structure
	err := os.MkdirAll(venvLibPath, 0755)
	if err != nil {
		t.Fatalf("failed to create test directory structure: %v", err)
	}

	// Test the function
	result, err := findSitePackagesInVenv(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the result
	expectedPath := venvLibPath
	if result != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, result)
	}

	// Clean up is handled automatically by t.TempDir()
}

func TestFindSitePackagesInVenv_NoPythonDirectory(t *testing.T) {
	// Set up a temporary directory for the virtual environment simulation
	tempDir := t.TempDir()

	// Test the function
	_, err := findSitePackagesInVenv(tempDir)
	if err == nil {
		t.Fatalf("expected an error, but got none")
	}

	// Verify the error message
	expectedError := "unable to find a python3.* directory in the venv"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestFindSitePackagesInVenv_NoSitePackages(t *testing.T) {
	// Set up a temporary directory for the virtual environment simulation
	tempDir := t.TempDir()
	libPath := filepath.Join(tempDir, "lib", "python3.12")

	// Create the lib/python3.12 directory, but omit site-packages
	err := os.MkdirAll(libPath, 0755)
	if err != nil {
		t.Fatalf("failed to create test directory structure: %v", err)
	}

	// Test the function
	_, err = findSitePackagesInVenv(tempDir)
	if err == nil {
		t.Fatalf("expected an error, but got none")
	}

	// Verify the error message
	expectedError := "site-packages directory does not exist"
	if !strings.HasPrefix(err.Error(), expectedError) {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestNewMapKeyVal(t *testing.T) {
	m := NewMapKeyVal(3)
	for i := 0; i < m.Capacity(); i++ {
		m.Append(fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
	}
	if m == nil {
		t.Fatal("Expected non-nil MapKeyVal")
	}
	if m.Len() != 3 {
		t.Fatalf("Expected length 3, got %d", m.Len())
	}
	defer m.Cleanup()
}

func TestNewMapKeyValFromSource(t *testing.T) {
	m := NewMapKeyVal(3)
	for i := 0; i < m.Capacity(); i++ {
		m.Append(fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
	}
	m = NewMapKeyValFromSource(m.m)
	if m == nil {
		t.Fatal("Expected non-nil MapKeyVal")
	}
	if m.Len() != 3 {
		t.Fatalf("Expected length 3, got %d", m.Len())
	}
	defer m.Cleanup()
}

func TestSetAndGet(t *testing.T) {
	m := NewMapKeyVal(2)
	defer m.Cleanup()

	m.Append("Content-Type", "application/json")
	m.Append("Accept", "text/plain")

	k0, v0 := m.Get(0)
	if k0 != "Content-Type" || v0 != "application/json" {
		t.Errorf("Unexpected result at pos 0: got (%s, %s)", k0, v0)
	}

	k1, v1 := m.Get(1)
	if k1 != "Accept" || v1 != "text/plain" {
		t.Errorf("Unexpected result at pos 1: got (%s, %s)", k1, v1)
	}
}

func TestSetGetBounds(t *testing.T) {
	m := NewMapKeyVal(1)
	m.Append("Content-Type", "application/json")
	defer m.Cleanup()

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic for out-of-bounds Set, but did not panic")
		}
	}()
	m.Append("Overflow", "Oops")
}

func TestGetBounds(t *testing.T) {
	m := NewMapKeyVal(1)
	m.Append("Content-Type", "application/json")
	defer m.Cleanup()

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic for out-of-bounds Get, but did not panic")
		}
	}()
	m.Get(5)
}

func TestLenNull(t *testing.T) {
	m := MapKeyVal{}

	if m.Len() != 0 {
		t.Errorf("Expected length 0, got %d", m.Len())
	}

	if m.Capacity() != 0 {
		t.Errorf("Expected capacity 0, got %d", m.Capacity())
	}
}

func TestUpperCaseAndUnderscore(t *testing.T) {
	tests := []struct {
		input    rune
		expected rune
	}{
		{'a', 'A'},
		{'z', 'Z'},
		{'m', 'M'},
		{'-', '_'},
		{'=', '_'},
		{'A', 'A'}, // already uppercase
		{'_', '_'}, // should remain the same
		{'1', '1'}, // number
		{'$', '$'}, // symbol
	}

	for _, tt := range tests {
		got := upperCaseAndUnderscore(tt.input)
		if got != tt.expected {
			t.Errorf("upperCaseAndUnderscore(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBytesAsBuffer(t *testing.T) {
	// Test with a non-empty byte slice
	input := []byte("hello world")
	buffer, bufferLen := bytesAsBuffer(input)

	if buffer == nil {
		t.Errorf("Expected non-nil buffer, got nil")
	}

	if int(bufferLen) != len(input) {
		t.Errorf("Expected buffer length %d, got %d", len(input), bufferLen)
	}

	// Test with an empty byte slice
	emptyInput := []byte("")
	emptyBuffer, emptyBufferLen := bytesAsBuffer(emptyInput)

	if emptyBuffer == nil {
		t.Errorf("Expected non-nil buffer for empty input, got nil")
	}

	if emptyBufferLen != 0 {
		t.Errorf("Expected buffer length 0 for empty input, got %d", emptyBufferLen)
	}
}

type mockNetAddr struct {
	addr string
}

func (m *mockNetAddr) Network() string {
	return "tcp"
}

func (m *mockNetAddr) String() string {
	return m.addr
}

func TestBuildWsgiHeaders(t *testing.T) {
	// Create a sample HTTP request
	r := &http.Request{
		Method: "GET",
		Proto:  "HTTP/1.1",
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Content-Length": []string{"123"},
			"Custom-Header":  []string{"CustomValue"},
		},
		URL: &url.URL{
			Path:     "/test/path",
			RawQuery: "key=value",
		},
		Host: "localhost:8080",
		Body: io.NopCloser(strings.NewReader("")),
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	// Call the function
	headers := buildWsgiHeaders(r)
	defer headers.Cleanup()

	// Check the headers
	expectedHeaders := map[string]string{
		"SERVER_NAME":        "localhost",
		"SERVER_PORT":        "8080",
		"SERVER_PROTOCOL":    "HTTP/1.1",
		"REQUEST_METHOD":     "GET",
		"PATH_INFO":          "/test/path",
		"QUERY_STRING":       "key=value",
		"CONTENT_TYPE":       "application/json",
		"CONTENT_LENGTH":     "123",
		"HTTP_CUSTOM_HEADER": "CustomValue",
		"SCRIPT_NAME":        "",
		"X_FROM":             "caddy-snake",
		"wsgi.url_scheme":    "http",
	}

	for i := 0; i < headers.Len(); i++ {
		key, value := headers.Get(i)
		if expectedValue, ok := expectedHeaders[key]; ok {
			if value != expectedValue {
				t.Errorf("Header %s: expected %s, got %s", key, expectedValue, value)
			}
			delete(expectedHeaders, key)
		} else {
			t.Errorf("Unexpected header: %s=%s", key, value)
		}
	}

	if len(expectedHeaders) > 0 {
		t.Errorf("Missing headers: %v", expectedHeaders)
	}
}

func TestWsgiState(t *testing.T) {
	state := &WsgiGlobalState{
		handlers: make(map[int64]chan WsgiResponse),
	}

	// Test Request method
	requestID := state.Request()
	if requestID != 1 {
		t.Errorf("Expected request ID 1, got %d", requestID)
	}
	if _, exists := state.handlers[requestID]; !exists {
		t.Errorf("Handler for request ID %d does not exist", requestID)
	}

	// Test Response method
	response := WsgiResponse{
		statusCode: 200,
		body:       nil,
		bodySize:   0,
	}
	go state.Response(requestID, response)

	result := state.WaitResponse(requestID)
	if result.statusCode != 200 {
		t.Errorf("Expected status code 200, got %d", result.statusCode)
	}
}

func TestWsgiResponseWrite(t *testing.T) {
	// Mock HTTP ResponseWriter
	mockWriter := &mockResponseWriter{
		headers: make(http.Header),
	}

	// Create a WsgiResponse with mock data
	response := &WsgiResponse{
		statusCode: 200,
		headers:    nil,
		body:       nil,
		bodySize:   0,
	}

	// Set headers in the WsgiResponse
	responseHeaders := NewMapKeyVal(2)
	responseHeaders.Append("Content-Type", "text/plain")
	responseHeaders.Append("X-Custom-Header", "CustomValue")
	response.headers = responseHeaders.m
	// defer responseHeaders.Cleanup()

	// Call the Write method
	response.Write(mockWriter)

	// Validate the response
	if mockWriter.statusCode != 200 {
		t.Errorf("Expected status code 200, got %d", mockWriter.statusCode)
	}

	if mockWriter.body != "" {
		t.Errorf("Expected body to be empty, got '%s'", mockWriter.body)
	}

	if mockWriter.headers.Get("Content-Type") != "text/plain" {
		t.Errorf("Expected Content-Type 'text/plain', got '%s'", mockWriter.headers.Get("Content-Type"))
	}

	if mockWriter.headers.Get("X-Custom-Header") != "CustomValue" {
		t.Errorf("Expected X-Custom-Header 'CustomValue', got '%s'", mockWriter.headers.Get("X-Custom-Header"))
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

func TestWebsocketUpgrade(t *testing.T) {
	// Create a simple GET request
	r := &http.Request{
		Method: "POST",
		Header: http.Header{},
	}
	if needsWebsocketUpgrade(r) {
		t.Error("Expected POST request not to be upgraded to websockets")
	}

	r.Method = "GET"
	if needsWebsocketUpgrade(r) {
		t.Error("Expected request not to be upgraded to websockets, missing headers")
	}

	r.Header.Add("connection", "upgrade")
	if needsWebsocketUpgrade(r) {
		t.Error("Expected request not to be upgraded to websockets, missing header: upgrade")
	}

	r.Header.Add("upgrade", "websocket")
	if !needsWebsocketUpgrade(r) {
		t.Error("Expected requests to be upgraded to websockets")
	}
}

func TestRemoteHostPort(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "10.10.10.10:54321",
	}
	host, port := getRemoteHostPort(r)
	if host != "10.10.10.10" {
		t.Error("Expected host to be 10.10.10.10")
	}
	if port != 54321 {
		t.Error("Expected port to be 54321")
	}
}

func TestBuildAsgiHeaders(t *testing.T) {
	// Create a sample HTTP request
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Content-Length": []string{"123"},
			"Custom-Header":  []string{"CustomValue"},
		},
		URL: &url.URL{
			Path:     "/test/path",
			RawQuery: "key=value",
		},
		Host: "localhost:8080",
		Body: io.NopCloser(strings.NewReader("")),
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	// Call the function
	headers, scope, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Error("Expected err to be nil")
	}
	defer headers.Cleanup()

	// Check the headers
	expectedHeaders := map[string]string{
		"content-type":   "application/json",
		"content-length": "123",
		"custom-header":  "CustomValue",
	}

	for i := 0; i < headers.Len(); i++ {
		key, value := headers.Get(i)
		if expectedValue, ok := expectedHeaders[key]; ok {
			if value != expectedValue {
				t.Errorf("Header %s: expected %s, got %s", key, expectedValue, value)
			}
			delete(expectedHeaders, key)
		} else {
			t.Errorf("Unexpected header: %s=%s", key, value)
		}
	}

	if len(expectedHeaders) > 0 {
		t.Errorf("Missing headers: %v", expectedHeaders)
	}

	// Check the scope
	expectedScope := map[string]string{
		"type":         "http",
		"http_version": "1.1",
		"method":       "GET",
		"scheme":       "http",
		"path":         "/test/path",
		"raw_path":     r.URL.EscapedPath(),
		"query_string": r.URL.RawQuery,
		"root_path":    "",
	}

	for i := 0; i < scope.Len(); i++ {
		key, value := scope.Get(i)
		if expectedValue, ok := expectedScope[key]; ok {
			if value != expectedValue {
				t.Errorf("Scope %s: expected %s, got %s", key, expectedValue, value)
			}
			delete(expectedScope, key)
		} else {
			t.Errorf("Unexpected header: %s=%s", key, value)
		}
	}

	if len(expectedScope) > 0 {
		t.Errorf("Missing scope: %v", expectedScope)
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
	// When there is no Caddy replacer in the context, resolve should return
	// the patterns as-is.
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

	// First call should create the app.
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

	// Second call with same key should return cached app without calling factory.
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

	// Third call with different key should create a new app.
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

	// Create two apps.
	_, _ = d.getOrCreateApp("key1", "main:app", "/home/a", "")
	_, _ = d.getOrCreateApp("key2", "main:app", "/home/b", "")

	err := d.Cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanupCount != 2 {
		t.Errorf("expected 2 cleanups, got %d", cleanupCount)
	}

	// After cleanup, the apps map should be empty.
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

	// Should succeed for existing directory
	abs, err := findWorkingDirectory(tempDir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if abs != tempDir {
		t.Errorf("expected %q, got %q", tempDir, abs)
	}

	// Should fail for non-existent directory
	nonExistent := tempDir + "-doesnotexist"
	_, err = findWorkingDirectory(nonExistent)
	if err == nil || !strings.Contains(err.Error(), "working_dir directory does not exist") {
		t.Errorf("expected error for non-existent directory, got: %v", err)
	}

	// Should fail for a file (not a directory)
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

// mockNextHandler implements caddyhttp.Handler for testing.
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

// ====================== getHostPort Tests ======================

func TestGetHostPort_WithPort(t *testing.T) {
	r := &http.Request{
		Host: "example.com:9080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"0.0.0.0:9080"})
	r = r.WithContext(ctx)

	host, port := getHostPort(r)
	if host != "example.com" {
		t.Errorf("expected host 'example.com', got %q", host)
	}
	if port != 9080 {
		t.Errorf("expected port 9080, got %d", port)
	}
}

func TestGetHostPort_WithoutPort(t *testing.T) {
	r := &http.Request{
		Host: "example.com",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"0.0.0.0:443"})
	r = r.WithContext(ctx)

	host, port := getHostPort(r)
	if host != "example.com" {
		t.Errorf("expected host 'example.com', got %q", host)
	}
	if port != 443 {
		t.Errorf("expected port 443, got %d", port)
	}
}

// ====================== buildWsgiHeaders Extended Tests ======================

func TestBuildWsgiHeaders_ProxyHeaderExcluded(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Proto:  "HTTP/1.1",
		Header: http.Header{
			"Proxy":  []string{"malicious"},
			"Accept": []string{"text/html"},
		},
		URL:  &url.URL{Path: "/", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers := buildWsgiHeaders(r)
	defer headers.Cleanup()

	for i := 0; i < headers.Len(); i++ {
		key, _ := headers.Get(i)
		if key == "HTTP_PROXY" {
			t.Error("Proxy header should be excluded from WSGI headers")
		}
	}
}

func TestBuildWsgiHeaders_CookieJoinedWithSemicolon(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Proto:  "HTTP/1.1",
		Header: http.Header{
			"Cookie": []string{"session=abc", "token=xyz"},
		},
		URL:  &url.URL{Path: "/", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers := buildWsgiHeaders(r)
	defer headers.Cleanup()

	found := false
	for i := 0; i < headers.Len(); i++ {
		key, value := headers.Get(i)
		if key == "HTTP_COOKIE" {
			found = true
			if value != "session=abc; token=xyz" {
				t.Errorf("expected cookie joined with '; ', got %q", value)
			}
		}
	}
	if !found {
		t.Error("HTTP_COOKIE header not found")
	}
}

func TestBuildWsgiHeaders_MultipleHeaderValues(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Proto:  "HTTP/1.1",
		Header: http.Header{
			"Accept-Encoding": []string{"gzip", "deflate"},
		},
		URL:  &url.URL{Path: "/", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers := buildWsgiHeaders(r)
	defer headers.Cleanup()

	found := false
	for i := 0; i < headers.Len(); i++ {
		key, value := headers.Get(i)
		if key == "HTTP_ACCEPT_ENCODING" {
			found = true
			if value != "gzip, deflate" {
				t.Errorf("expected 'gzip, deflate', got %q", value)
			}
		}
	}
	if !found {
		t.Error("HTTP_ACCEPT_ENCODING header not found")
	}
}

// ====================== buildAsgiHeaders Extended Tests ======================

func TestBuildAsgiHeaders_WebsocketMode(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		URL:        &url.URL{Path: "/ws", RawQuery: ""},
		Host:       "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers, scope, err := buildAsgiHeaders(r, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer headers.Cleanup()
	defer scope.Cleanup()

	scopeMap := make(map[string]string)
	for i := 0; i < scope.Len(); i++ {
		k, v := scope.Get(i)
		scopeMap[k] = v
	}
	if scopeMap["type"] != "websocket" {
		t.Errorf("expected type 'websocket', got %q", scopeMap["type"])
	}
	if scopeMap["scheme"] != "ws" {
		t.Errorf("expected scheme 'ws', got %q", scopeMap["scheme"])
	}
}

func TestBuildAsgiHeaders_TLSScheme(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		URL:        &url.URL{Path: "/", RawQuery: ""},
		Host:       "localhost:443",
		TLS:        &tls.ConnectionState{},
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:443"})
	r = r.WithContext(ctx)

	_, scope, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer scope.Cleanup()

	scopeMap := make(map[string]string)
	for i := 0; i < scope.Len(); i++ {
		k, v := scope.Get(i)
		scopeMap[k] = v
	}
	if scopeMap["scheme"] != "https" {
		t.Errorf("expected scheme 'https', got %q", scopeMap["scheme"])
	}
}

func TestBuildAsgiHeaders_TLSWebsocketScheme(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		URL:        &url.URL{Path: "/ws", RawQuery: ""},
		Host:       "localhost:443",
		TLS:        &tls.ConnectionState{},
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:443"})
	r = r.WithContext(ctx)

	_, scope, err := buildAsgiHeaders(r, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer scope.Cleanup()

	scopeMap := make(map[string]string)
	for i := 0; i < scope.Len(); i++ {
		k, v := scope.Get(i)
		scopeMap[k] = v
	}
	if scopeMap["scheme"] != "wss" {
		t.Errorf("expected scheme 'wss', got %q", scopeMap["scheme"])
	}
}

func TestBuildAsgiHeaders_ProxyHeaderExcluded(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Proxy":  []string{"malicious"},
			"Accept": []string{"text/html"},
		},
		URL:  &url.URL{Path: "/", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers, _, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer headers.Cleanup()

	for i := 0; i < headers.Len(); i++ {
		key, _ := headers.Get(i)
		if key == "proxy" {
			t.Error("proxy header should be excluded from ASGI headers")
		}
	}
}

func TestBuildAsgiHeaders_CookieJoinedWithSemicolon(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Cookie": []string{"session=abc", "token=xyz"},
		},
		URL:  &url.URL{Path: "/", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers, _, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer headers.Cleanup()

	found := false
	for i := 0; i < headers.Len(); i++ {
		key, value := headers.Get(i)
		if key == "cookie" {
			found = true
			if value != "session=abc; token=xyz" {
				t.Errorf("expected cookie joined with '; ', got %q", value)
			}
		}
	}
	if !found {
		t.Error("cookie header not found")
	}
}

func TestBuildAsgiHeaders_EncodedPath(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		URL:        &url.URL{Path: "/hello world", RawPath: "/hello%20world", RawQuery: ""},
		Host:       "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	_, scope, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer scope.Cleanup()

	scopeMap := make(map[string]string)
	for i := 0; i < scope.Len(); i++ {
		k, v := scope.Get(i)
		scopeMap[k] = v
	}
	if scopeMap["path"] != "/hello world" {
		t.Errorf("expected decoded path '/hello world', got %q", scopeMap["path"])
	}
	if scopeMap["raw_path"] != "/hello%20world" {
		t.Errorf("expected raw path '/hello%%20world', got %q", scopeMap["raw_path"])
	}
}

func TestBuildAsgiHeaders_HTTP2(t *testing.T) {
	r := &http.Request{
		Method:     "POST",
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		ProtoMinor: 0,
		Header:     http.Header{},
		URL:        &url.URL{Path: "/api", RawQuery: "q=1"},
		Host:       "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	_, scope, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer scope.Cleanup()

	scopeMap := make(map[string]string)
	for i := 0; i < scope.Len(); i++ {
		k, v := scope.Get(i)
		scopeMap[k] = v
	}
	if scopeMap["http_version"] != "2.0" {
		t.Errorf("expected http_version '2.0', got %q", scopeMap["http_version"])
	}
	if scopeMap["method"] != "POST" {
		t.Errorf("expected method 'POST', got %q", scopeMap["method"])
	}
	if scopeMap["query_string"] != "q=1" {
		t.Errorf("expected query_string 'q=1', got %q", scopeMap["query_string"])
	}
}

// ====================== WsgiResponse.Write Extended Tests ======================

func TestWsgiResponseWrite_500NilBody(t *testing.T) {
	w := &mockResponseWriter{headers: make(http.Header)}
	response := &WsgiResponse{
		statusCode: 500,
		headers:    nil,
		body:       nil,
		bodySize:   0,
	}
	response.Write(w)

	if w.statusCode != 500 {
		t.Errorf("expected status 500, got %d", w.statusCode)
	}
	if w.body != "Internal Server Error" {
		t.Errorf("expected 'Internal Server Error' body, got %q", w.body)
	}
}

func TestWsgiResponseWrite_NilHeadersNonError(t *testing.T) {
	w := &mockResponseWriter{headers: make(http.Header)}
	response := &WsgiResponse{
		statusCode: 200,
		headers:    nil,
		body:       nil,
		bodySize:   0,
	}
	response.Write(w)

	if w.statusCode != 200 {
		t.Errorf("expected status 200, got %d", w.statusCode)
	}
	if w.body != "" {
		t.Errorf("expected empty body for 200 with nil body, got %q", w.body)
	}
}

func TestWsgiResponseWrite_404NilBody(t *testing.T) {
	w := &mockResponseWriter{headers: make(http.Header)}
	response := &WsgiResponse{
		statusCode: 404,
		headers:    nil,
		body:       nil,
		bodySize:   0,
	}
	response.Write(w)

	if w.statusCode != 404 {
		t.Errorf("expected status 404, got %d", w.statusCode)
	}
	// Only 500 should produce "Internal Server Error" body
	if w.body != "" {
		t.Errorf("expected empty body for 404, got %q", w.body)
	}
}

// ====================== AsgiGlobalState Tests ======================

func TestAsgiGlobalState_Lifecycle(t *testing.T) {
	state := newAsgiGlobalState()

	// All shards should be initialized.
	for i := 0; i < asgiShardCount; i++ {
		if state.shards[i] == nil {
			t.Fatalf("shard %d is nil", i)
		}
	}

	// Register a handler.
	w := &mockResponseWriter{headers: make(http.Header)}
	r := &http.Request{}
	h := &AsgiRequestHandler{
		w:          w,
		r:          r,
		done:       make(chan error, 2),
		operations: make(chan AsgiOperations, 16),
	}
	id := state.Request(h)
	if id == 0 {
		t.Error("expected non-zero request ID")
	}

	// GetHandler should return the handler.
	got := state.GetHandler(id)
	if got != h {
		t.Error("expected to get back the same handler")
	}

	// GetHandler for non-existent ID should return nil.
	got = state.GetHandler(999999)
	if got != nil {
		t.Error("expected nil for non-existent handler")
	}

	// Cleanup should remove the handler.
	state.Cleanup(id)
	got = state.GetHandler(id)
	if got != nil {
		t.Error("expected nil after cleanup")
	}
}

func TestAsgiGlobalState_ShardDistribution(t *testing.T) {
	state := newAsgiGlobalState()

	// IDs that differ by asgiShardCount should map to the same shard.
	shard0 := state.shardFor(0)
	shardN := state.shardFor(uint64(asgiShardCount))
	if shard0 != shardN {
		t.Errorf("IDs 0 and %d should map to the same shard", asgiShardCount)
	}

	// Consecutive IDs should map to different shards.
	shard1 := state.shardFor(1)
	if shard0 == shard1 {
		t.Error("IDs 0 and 1 should map to different shards")
	}
}

func TestAsgiGlobalState_MultipleHandlers(t *testing.T) {
	state := newAsgiGlobalState()

	handlers := make([]*AsgiRequestHandler, 10)
	ids := make([]uint64, 10)
	for i := 0; i < 10; i++ {
		handlers[i] = &AsgiRequestHandler{
			w:          &mockResponseWriter{headers: make(http.Header)},
			r:          &http.Request{},
			done:       make(chan error, 2),
			operations: make(chan AsgiOperations, 16),
		}
		ids[i] = state.Request(handlers[i])
	}

	// All IDs should be unique and all handlers retrievable.
	seen := make(map[uint64]bool)
	for i, id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID: %d", id)
		}
		seen[id] = true
		got := state.GetHandler(id)
		if got != handlers[i] {
			t.Errorf("handler mismatch for ID %d", id)
		}
	}

	// Cleanup one and verify others still exist.
	state.Cleanup(ids[0])
	if state.GetHandler(ids[0]) != nil {
		t.Error("expected nil after cleanup")
	}
	if state.GetHandler(ids[1]) == nil {
		t.Error("expected other handler to still exist")
	}
}

// ====================== WsgiGlobalState Extended Tests ======================

func TestWsgiState_MultipleRequests(t *testing.T) {
	state := &WsgiGlobalState{
		handlers: make(map[int64]chan WsgiResponse),
	}

	id1 := state.Request()
	id2 := state.Request()
	id3 := state.Request()

	if id1 == id2 || id2 == id3 || id1 == id3 {
		t.Error("expected unique request IDs")
	}
	if id1 != 1 || id2 != 2 || id3 != 3 {
		t.Errorf("expected sequential IDs 1,2,3 got %d,%d,%d", id1, id2, id3)
	}

	// Respond in reverse order to test independence.
	go state.Response(id3, WsgiResponse{statusCode: 203})
	go state.Response(id1, WsgiResponse{statusCode: 201})
	go state.Response(id2, WsgiResponse{statusCode: 202})

	r3 := state.WaitResponse(id3)
	r1 := state.WaitResponse(id1)
	r2 := state.WaitResponse(id2)

	if r1.statusCode != 201 {
		t.Errorf("expected 201 for id1, got %d", r1.statusCode)
	}
	if r2.statusCode != 202 {
		t.Errorf("expected 202 for id2, got %d", r2.statusCode)
	}
	if r3.statusCode != 203 {
		t.Errorf("expected 203 for id3, got %d", r3.statusCode)
	}

	// All handlers should be cleaned up after WaitResponse.
	if len(state.handlers) != 0 {
		t.Errorf("expected 0 handlers after all responses, got %d", len(state.handlers))
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

	// Map should remain empty after factory error.
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

	// Verify all are in the map.
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
	// Should find one of them (first match during walk).
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
	// Create a file named python3.11 (not a directory).
	os.WriteFile(filepath.Join(tempDir, "python3.11"), []byte("not a dir"), 0644)

	_, err := findPythonDirectory(tempDir)
	if err == nil {
		t.Fatal("expected error when python3.x is a file, not directory")
	}
}

// ====================== needsWebsocketUpgrade Extended Tests ======================

func TestNeedsWebsocketUpgrade_CaseInsensitive(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Header: http.Header{
			"Connection": []string{"Upgrade"},
			"Upgrade":    []string{"WebSocket"},
		},
	}
	if !needsWebsocketUpgrade(r) {
		t.Error("expected case-insensitive websocket upgrade detection")
	}
}

func TestNeedsWebsocketUpgrade_MixedCaseMultiValue(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Header: http.Header{
			"Connection": []string{"keep-alive, Upgrade"},
			"Upgrade":    []string{"WEBSOCKET"},
		},
	}
	if !needsWebsocketUpgrade(r) {
		t.Error("expected mixed-case multi-value websocket upgrade detection")
	}
}

func TestNeedsWebsocketUpgrade_MissingUpgradeHeader(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Header: http.Header{
			"Connection": []string{"upgrade"},
			// No Upgrade header
		},
	}
	if needsWebsocketUpgrade(r) {
		t.Error("expected false when Upgrade header is missing")
	}
}

func TestNeedsWebsocketUpgrade_WrongUpgradeValue(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Header: http.Header{
			"Connection": []string{"upgrade"},
			"Upgrade":    []string{"h2c"},
		},
	}
	if needsWebsocketUpgrade(r) {
		t.Error("expected false when Upgrade value is not websocket")
	}
}

// ====================== MapKeyVal.Cleanup nil Tests ======================

func TestMapKeyValCleanup_Nil(t *testing.T) {
	m := &MapKeyVal{}
	// Should not panic on nil internal state.
	m.Cleanup()
}

// ====================== WebsocketState Constants Tests ======================

func TestWebsocketStateConstants(t *testing.T) {
	if WS_STARTING != 2 {
		t.Errorf("expected WS_STARTING=2, got %d", WS_STARTING)
	}
	if WS_CONNECTED != 3 {
		t.Errorf("expected WS_CONNECTED=3, got %d", WS_CONNECTED)
	}
	if WS_DISCONNECTED != 4 {
		t.Errorf("expected WS_DISCONNECTED=4, got %d", WS_DISCONNECTED)
	}
}

// ====================== getRemoteHostPort Extended Tests ======================

func TestGetRemoteHostPort_IPv6(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "[::1]:54321",
	}
	host, port := getRemoteHostPort(r)
	if host != "::1" {
		t.Errorf("expected host '::1', got %q", host)
	}
	if port != 54321 {
		t.Errorf("expected port 54321, got %d", port)
	}
}

func TestGetRemoteHostPort_StandardIPv4(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "192.168.1.100:80",
	}
	host, port := getRemoteHostPort(r)
	if host != "192.168.1.100" {
		t.Errorf("expected host '192.168.1.100', got %q", host)
	}
	if port != 80 {
		t.Errorf("expected port 80, got %d", port)
	}
}

// ====================== Additional Coverage Tests ======================

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
	// Create site-packages as a file, not a directory.
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

func TestBuildAsgiHeaders_InvalidPercentEncoding(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		URL:        &url.URL{Path: "/bad%path", RawQuery: ""},
		Host:       "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	_, _, err := buildAsgiHeaders(r, false)
	if err == nil {
		t.Fatal("expected error for invalid percent-encoded path")
	}
}

func TestDynamicAppGetOrCreate_DoubleCheckPath(t *testing.T) {
	// Test the double-check locking path by using a barrier so many goroutines
	// all pass the read-lock check (not finding the key) before any acquires
	// the write lock. The first to get the write lock creates the app via factory;
	// subsequent goroutines hit the double-check and find it without calling factory.
	factoryCalls := int32(0)
	mockApp := &mockAppServer{}

	d, _ := NewDynamicApp("main:app", "/home/test", "",
		func(module, dir, venv string) (AppServer, error) {
			atomic.AddInt32(&factoryCalls, 1)
			// Small delay so other goroutines queue on the write lock.
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
			barrier.Wait() // All start at the same instant.
			_, err := d.getOrCreateApp("key1", "main:app", "/home/test", "")
			errs <- err
		}()
	}

	// Release all goroutines simultaneously so they all pass the read-lock
	// before any one acquires the write lock.
	barrier.Done()

	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Factory should be called exactly once — all other goroutines use the double-check path.
	if calls := atomic.LoadInt32(&factoryCalls); calls != 1 {
		t.Errorf("expected factory to be called once, got %d", calls)
	}
}

func TestBuildAsgiHeaders_ProxyExactCanonicalKey(t *testing.T) {
	// Ensure that the Proxy header is excluded using the canonical key check.
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Proxy":       []string{"evil"},
			"X-Forwarded": []string{"ok"},
		},
		URL:  &url.URL{Path: "/", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers, _, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer headers.Cleanup()

	for i := 0; i < headers.Len(); i++ {
		key, _ := headers.Get(i)
		if key == "proxy" {
			t.Error("proxy header should be excluded")
		}
	}
	// X-Forwarded should be present.
	found := false
	for i := 0; i < headers.Len(); i++ {
		key, _ := headers.Get(i)
		if key == "x-forwarded" {
			found = true
		}
	}
	if !found {
		t.Error("x-forwarded header should be present")
	}
}

func TestBuildWsgiHeaders_ContentTypeLengthExcluded(t *testing.T) {
	// Ensure Content-Type and Content-Length are not duplicated as HTTP_ headers.
	r := &http.Request{
		Method: "POST",
		Proto:  "HTTP/1.1",
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Content-Length": []string{"42"},
			"Accept":         []string{"*/*"},
		},
		URL:  &url.URL{Path: "/api", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers := buildWsgiHeaders(r)
	defer headers.Cleanup()

	for i := 0; i < headers.Len(); i++ {
		key, _ := headers.Get(i)
		if key == "HTTP_CONTENT_TYPE" {
			t.Error("Content-Type should not appear as HTTP_CONTENT_TYPE")
		}
		if key == "HTTP_CONTENT_LENGTH" {
			t.Error("Content-Length should not appear as HTTP_CONTENT_LENGTH")
		}
	}

	// CONTENT_TYPE and CONTENT_LENGTH should still appear in extra headers.
	headerMap := make(map[string]string)
	for i := 0; i < headers.Len(); i++ {
		k, v := headers.Get(i)
		headerMap[k] = v
	}
	if headerMap["CONTENT_TYPE"] != "application/json" {
		t.Errorf("expected CONTENT_TYPE 'application/json', got %q", headerMap["CONTENT_TYPE"])
	}
	if headerMap["CONTENT_LENGTH"] != "42" {
		t.Errorf("expected CONTENT_LENGTH '42', got %q", headerMap["CONTENT_LENGTH"])
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

	// Cleanup with no apps should succeed with nil error.
	err := d.Cleanup()
	if err != nil {
		t.Errorf("expected nil error for empty cleanup, got: %v", err)
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

	// Context with a nil replacer value should fall through gracefully.
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

func TestWsgiResponseWrite_WithHeaders(t *testing.T) {
	w := &mockResponseWriter{headers: make(http.Header)}
	responseHeaders := NewMapKeyVal(2)
	responseHeaders.Append("X-Test", "value1")
	responseHeaders.Append("X-Other", "value2")

	response := &WsgiResponse{
		statusCode: 201,
		headers:    responseHeaders.m,
		body:       nil,
		bodySize:   0,
	}
	response.Write(w)

	if w.statusCode != 201 {
		t.Errorf("expected status 201, got %d", w.statusCode)
	}
	if w.headers.Get("X-Test") != "value1" {
		t.Errorf("expected X-Test 'value1', got %q", w.headers.Get("X-Test"))
	}
	if w.headers.Get("X-Other") != "value2" {
		t.Errorf("expected X-Other 'value2', got %q", w.headers.Get("X-Other"))
	}
}

func TestGetHostPort_IPv6(t *testing.T) {
	r := &http.Request{
		Host: "[::1]:9080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"[::1]:9080"})
	r = r.WithContext(ctx)

	host, port := getHostPort(r)
	if host != "::1" {
		t.Errorf("expected host '::1', got %q", host)
	}
	if port != 9080 {
		t.Errorf("expected port 9080, got %d", port)
	}
}

func TestBuildWsgiHeaders_EmptyHeaders(t *testing.T) {
	r := &http.Request{
		Method: "GET",
		Proto:  "HTTP/1.1",
		Header: http.Header{},
		URL:    &url.URL{Path: "/", RawQuery: ""},
		Host:   "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers := buildWsgiHeaders(r)
	defer headers.Cleanup()

	// Should have the extra headers but no HTTP_ headers.
	headerMap := make(map[string]string)
	for i := 0; i < headers.Len(); i++ {
		k, v := headers.Get(i)
		headerMap[k] = v
	}
	if headerMap["SERVER_NAME"] != "localhost" {
		t.Errorf("expected SERVER_NAME 'localhost', got %q", headerMap["SERVER_NAME"])
	}
	if headerMap["REQUEST_METHOD"] != "GET" {
		t.Errorf("expected REQUEST_METHOD 'GET', got %q", headerMap["REQUEST_METHOD"])
	}
}

func TestBuildAsgiHeaders_EmptyHeaders(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		URL:        &url.URL{Path: "/", RawQuery: ""},
		Host:       "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers, scope, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer headers.Cleanup()
	defer scope.Cleanup()

	if headers.Len() != 0 {
		t.Errorf("expected 0 headers with empty request headers, got %d", headers.Len())
	}

	scopeMap := make(map[string]string)
	for i := 0; i < scope.Len(); i++ {
		k, v := scope.Get(i)
		scopeMap[k] = v
	}
	if scopeMap["type"] != "http" {
		t.Errorf("expected type 'http', got %q", scopeMap["type"])
	}
}

func TestBuildAsgiHeaders_MultipleHeaderValues(t *testing.T) {
	r := &http.Request{
		Method:     "GET",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Accept-Encoding": []string{"gzip", "deflate"},
		},
		URL:  &url.URL{Path: "/", RawQuery: ""},
		Host: "localhost:8080",
	}
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, &mockNetAddr{"localhost:8080"})
	r = r.WithContext(ctx)

	headers, _, err := buildAsgiHeaders(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer headers.Cleanup()

	found := false
	for i := 0; i < headers.Len(); i++ {
		key, value := headers.Get(i)
		if key == "accept-encoding" {
			found = true
			if value != "gzip, deflate" {
				t.Errorf("expected 'gzip, deflate', got %q", value)
			}
		}
	}
	if !found {
		t.Error("accept-encoding header not found")
	}
}
