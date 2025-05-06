package caddysnake

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	if m.Len() != 0 {
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
