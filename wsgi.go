package caddysnake

// #include "caddysnake.h"
import "C"
import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// WsgiResponse holds the response from the WSGI app
type WsgiResponse struct {
	statusCode C.int
	headers    *C.MapKeyVal
	body       *C.char
	bodySize   C.size_t
}

func (r *WsgiResponse) Write(w http.ResponseWriter) {
	if r.headers != nil {
		resultHeaders := NewMapKeyValFromSource(r.headers)
		defer resultHeaders.Cleanup()

		for i := 0; i < resultHeaders.Len(); i++ {
			k, v := resultHeaders.Get(i)
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(int(r.statusCode))

	if r.body != nil {
		defer C.free(unsafe.Pointer(r.body))
		bodyBytes := C.GoBytes(unsafe.Pointer(r.body), C.int(r.bodySize))
		w.Write(bodyBytes)
	} else if r.statusCode == 500 {
		w.Write([]byte("Internal Server Error"))
	}
}

// WsgiGlobalState holds the global state for all requests to WSGI apps
type WsgiGlobalState struct {
	sync.RWMutex
	requestCounter int64
	handlers       map[int64]chan WsgiResponse
}

// Request creates a new request handler and returns its ID
func (s *WsgiGlobalState) Request() int64 {
	s.Lock()
	defer s.Unlock()
	s.requestCounter++
	s.handlers[s.requestCounter] = make(chan WsgiResponse)
	return s.requestCounter
}

// Response sends the response to the channel and closes it
func (s *WsgiGlobalState) Response(requestID int64, response WsgiResponse) {
	s.RLock()
	ch := s.handlers[requestID]
	s.RUnlock()
	ch <- response
}

// WaitResponse waits for the response from the channel and returns it
func (s *WsgiGlobalState) WaitResponse(requestID int64) WsgiResponse {
	s.RLock()
	ch := s.handlers[requestID]
	s.RUnlock()
	response := <-ch
	close(ch)
	s.Lock()
	delete(s.handlers, requestID)
	s.Unlock()
	return response
}

var (
	wsgiState     *WsgiGlobalState
	wsgiStateOnce sync.Once
)

func initWsgi() {
	wsgiStateOnce.Do(func() {
		wsgiState = &WsgiGlobalState{
			handlers:       make(map[int64]chan WsgiResponse),
			requestCounter: 0,
		}
	})
}

// Wsgi stores a reference to a Python Wsgi application
type Wsgi struct {
	app         *C.WsgiApp
	wsgiPattern string
}

var wsgiAppCache map[string]*Wsgi = map[string]*Wsgi{}

// importWsgiApp performs the actual Python WSGI app import without caching.
func importWsgiApp(wsgiPattern, workingDir, venvPath string) (*C.WsgiApp, error) {
	moduleApp := strings.Split(wsgiPattern, ":")
	if len(moduleApp) != 2 {
		return nil, errors.New("expected pattern $(MODULE_NAME):$(VARIABLE_NAME)")
	}
	moduleName := C.CString(moduleApp[0])
	defer C.free(unsafe.Pointer(moduleName))
	appName := C.CString(moduleApp[1])
	defer C.free(unsafe.Pointer(appName))

	var packagesPath *C.char = nil
	if venvPath != "" {
		sitePackagesPath, err := findSitePackagesInVenv(venvPath)
		if err != nil {
			return nil, err
		}
		packagesPath = C.CString(sitePackagesPath)
		defer C.free(unsafe.Pointer(packagesPath))
	}

	var workingDirPath *C.char = nil
	if workingDir != "" {
		workingDirAbs, err := findWorkingDirectory(workingDir)
		if err != nil {
			return nil, err
		}
		workingDirPath = C.CString(workingDirAbs)
		defer C.free(unsafe.Pointer(workingDirPath))
	}

	var app *C.WsgiApp
	pythonMainThread.do(func() {
		app = C.WsgiApp_import(moduleName, appName, workingDirPath, packagesPath)
	})
	if app == nil {
		return nil, errors.New("failed to import module")
	}

	return app, nil
}

// NewWsgi imports a WSGI app with global caching by wsgi pattern.
func NewWsgi(wsgiPattern, workingDir, venvPath string) (*Wsgi, error) {
	wsgiState.Lock()
	defer wsgiState.Unlock()

	if app, ok := wsgiAppCache[wsgiPattern]; ok {
		return app, nil
	}

	cApp, err := importWsgiApp(wsgiPattern, workingDir, venvPath)
	if err != nil {
		return nil, err
	}

	result := &Wsgi{cApp, wsgiPattern}
	wsgiAppCache[wsgiPattern] = result
	return result, nil
}

// NewDynamicWsgiApp imports a WSGI app for dynamic (per-request) use.
// It uses a composite cache key (pattern + working dir) so that the same module
// loaded from different directories is tracked separately for cleanup.
func NewDynamicWsgiApp(wsgiPattern, workingDir, venvPath string) (*Wsgi, error) {
	cApp, err := importWsgiApp(wsgiPattern, workingDir, venvPath)
	if err != nil {
		return nil, err
	}

	cacheKey := wsgiPattern + "@" + workingDir
	result := &Wsgi{cApp, cacheKey}

	wsgiState.Lock()
	wsgiAppCache[cacheKey] = result
	wsgiState.Unlock()

	return result, nil
}

// Cleanup deallocates CGO resources used by Wsgi app
func (m *Wsgi) Cleanup() error {
	if m.app != nil {
		wsgiState.Lock()
		if _, ok := wsgiAppCache[m.wsgiPattern]; !ok {
			wsgiState.Unlock()
			return nil
		}
		delete(wsgiAppCache, m.wsgiPattern)
		wsgiState.Unlock()

		pythonMainThread.do(func() {
			C.WsgiApp_cleanup(m.app)
		})
	}
	return nil
}

// from golang cgi
func upperCaseAndUnderscore(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z':
		return r - ('a' - 'A')
	case r == '-':
		return '_'
	case r == '=':
		return '_'
	}
	return r
}

func getHostPort(r *http.Request) (string, int) {
	ctx := r.Context()
	srvAddr := ctx.Value(http.LocalAddrContextKey).(net.Addr)
	_, port, _ := net.SplitHostPort(srvAddr.String())
	host, _, _ := net.SplitHostPort(r.Host)
	if host == "" {
		// net.SplitHostPort returns error and an empty host when port is missing
		host = r.Host
	}
	portN, _ := strconv.Atoi(port)
	return host, portN
}

// buildWsgiHeaders builds the WSGI headers from the HTTP request.
func buildWsgiHeaders(r *http.Request) *MapKeyVal {
	host, port := getHostPort(r)

	extraHeaders := map[string]string{
		"SERVER_NAME":     host,
		"SERVER_PORT":     fmt.Sprintf("%d", port),
		"SERVER_PROTOCOL": r.Proto,
		"X_FROM":          "caddy-snake",
		"REQUEST_METHOD":  r.Method,
		"SCRIPT_NAME":     "",
		"PATH_INFO":       r.URL.Path,
		"QUERY_STRING":    r.URL.RawQuery,
		"CONTENT_TYPE":    r.Header.Get("Content-type"),
		"CONTENT_LENGTH":  r.Header.Get("Content-length"),
		"wsgi.url_scheme": strings.ToLower(strings.Split(r.Proto, "/")[0]),
	}
	headersLength := len(r.Header)
	if _, ok := r.Header[textproto.CanonicalMIMEHeaderKey("Proxy")]; ok {
		headersLength -= 1
	}
	if _, ok := r.Header[textproto.CanonicalMIMEHeaderKey("Content-Type")]; ok {
		headersLength -= 1
	}
	if _, ok := r.Header[textproto.CanonicalMIMEHeaderKey("Content-Length")]; ok {
		headersLength -= 1
	}
	requestHeaders := NewMapKeyVal(headersLength + len(extraHeaders))
	for k, items := range r.Header {
		key := strings.Map(upperCaseAndUnderscore, k)
		if key == "PROXY" {
			// golang cgi issue 16405
			continue
		}
		// Content type and length already defined in extraHeaders
		if key == "CONTENT_TYPE" {
			continue
		}
		if key == "CONTENT_LENGTH" {
			continue
		}

		joinStr := ", "
		if key == "COOKIE" {
			joinStr = "; "
		}

		requestHeaders.Append("HTTP_"+key, strings.Join(items, joinStr))
	}
	for k, v := range extraHeaders {
		requestHeaders.Append(k, v)
	}
	return requestHeaders
}

// bytesAsBuffer converts a byte slice to a C char pointer and its length.
func bytesAsBuffer(b []byte) (*C.char, C.size_t) {
	// Append null-terminator for strings
	b = append(b, 0)
	buffer := (*C.char)(unsafe.Pointer(&b[0]))
	bufferLen := C.size_t(len(b) - 1) // -1 to remove null-terminator
	return buffer, bufferLen
}

// HandleRequest passes request down to Python Wsgi app and writes responses and headers.
func (m *Wsgi) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	requestHeaders := buildWsgiHeaders(r)
	defer requestHeaders.Cleanup()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	buffer, bufferLen := bytesAsBuffer(body)

	requestID := wsgiState.Request()

	pythonMainThread.do(func() {
		C.WsgiApp_handle_request(
			m.app,
			C.int64_t(requestID),
			requestHeaders.m,
			buffer,
			bufferLen,
		)
	})

	response := wsgiState.WaitResponse(requestID)

	response.Write(w)

	return nil
}

//export wsgi_write_response
func wsgi_write_response(requestID C.int64_t, statusCode C.int, headers *C.MapKeyVal, body *C.char, bodySize C.size_t) {
	wsgiState.Response(int64(requestID), WsgiResponse{
		statusCode: statusCode,
		headers:    headers,
		body:       body,
		bodySize:   bodySize,
	})
}
