// Caddy plugin to serve Python apps.
package caddysnake

// #cgo pkg-config: python3-embed
// #include "caddysnake.h"
import "C"
import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

//go:embed caddysnake.py
var caddysnake_py string

var SIZE_OF_CHAR_POINTER = unsafe.Sizeof((*C.char)(nil))

// MapKeyVal wraps the same structure defined in the C layer
type MapKeyVal struct {
	m           *C.MapKeyVal
	baseHeaders uintptr
	baseValues  uintptr
}

func NewMapKeyVal(count int) *MapKeyVal {
	m := C.MapKeyVal_new(C.size_t(count))
	return &MapKeyVal{
		m:           m,
		baseHeaders: uintptr(unsafe.Pointer(m.keys)),
		baseValues:  uintptr(unsafe.Pointer(m.values)),
	}
}

func NewMapKeyValFromSource(m *C.MapKeyVal) *MapKeyVal {
	return &MapKeyVal{
		m:           m,
		baseHeaders: uintptr(unsafe.Pointer(m.keys)),
		baseValues:  uintptr(unsafe.Pointer(m.values)),
	}
}

func (m *MapKeyVal) Cleanup() {
	if m.m != nil {
		C.MapKeyVal_free(m.m)
	}
}

func (m *MapKeyVal) Append(k, v string) {
	// Replicate the function MapKeyVal_append to avoid a CGO call
	if m.m == nil || m.m.length == m.m.capacity {
		panic("Maximum capacity reached")
	}
	pos := uintptr(m.m.length)
	*(**C.char)(unsafe.Pointer(m.baseHeaders + pos*SIZE_OF_CHAR_POINTER)) = C.CString(k)
	*(**C.char)(unsafe.Pointer(m.baseValues + pos*SIZE_OF_CHAR_POINTER)) = C.CString(v)
	m.m.length++
}

func (m *MapKeyVal) Get(pos int) (string, string) {
	if pos < 0 || pos > int(m.m.capacity) {
		panic("Expected pos to be within limits")
	}
	headerNamePtr := unsafe.Pointer(uintptr(unsafe.Pointer(m.m.keys)) + uintptr(pos)*SIZE_OF_CHAR_POINTER)
	headerValuePtr := unsafe.Pointer(uintptr(unsafe.Pointer(m.m.values)) + uintptr(pos)*SIZE_OF_CHAR_POINTER)
	headerName := *(**C.char)(headerNamePtr)
	headerValue := *(**C.char)(headerValuePtr)
	return C.GoString(headerName), C.GoString(headerValue)
}

func (m *MapKeyVal) Len() int {
	if m.m == nil {
		return 0
	}
	return int(m.m.length)
}

func (m *MapKeyVal) Capacity() int {
	if m.m == nil {
		return 0
	}
	return int(m.m.capacity)
}

// AppServer defines the interface to interacting with a WSGI or ASGI server
type AppServer interface {
	Cleanup() error
	HandleRequest(w http.ResponseWriter, r *http.Request) error
}

// CaddySnake module that communicates with a Python app
type CaddySnake struct {
	ModuleWsgi string `json:"module_wsgi,omitempty"`
	ModuleAsgi string `json:"module_asgi,omitempty"`
	Lifespan   string `json:"lifespan,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
	VenvPath   string `json:"venv_path,omitempty"`
	logger     *zap.Logger
	app        AppServer
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (f *CaddySnake) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		args := d.RemainingArgs()
		if len(args) == 1 {
			f.ModuleWsgi = args[0]
		} else if len(args) == 0 {
			for nesting := d.Nesting(); d.NextBlock(nesting); {
				switch d.Val() {
				case "module_asgi":
					if !d.Args(&f.ModuleAsgi) {
						return d.Errf("expected exactly one argument for module_asgi")
					}
				case "module_wsgi":
					if !d.Args(&f.ModuleWsgi) {
						return d.Errf("expected exactly one argument for module_wsgi")
					}
				case "lifespan":
					if !d.Args(&f.Lifespan) || (f.Lifespan != "on" && f.Lifespan != "off") {
						return d.Errf("expected exactly one argument for lifespan: on|off")
					}
				case "working_dir":
					if !d.Args(&f.WorkingDir) {
						return d.Errf("expected exactly one argument for working_dir")
					}
				case "venv":
					if !d.Args(&f.VenvPath) {
						return d.Errf("expected exactly one argument for venv")
					}
				default:
					return d.Errf("unknown subdirective: %s", d.Val())
				}
			}
		} else {
			return d.ArgErr()
		}
	}
	return nil
}

// CaddyModule returns the Caddy module information.
func (CaddySnake) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.python",
		New: func() caddy.Module { return new(CaddySnake) },
	}
}

// Provision sets up the module.
func (f *CaddySnake) Provision(ctx caddy.Context) error {
	f.logger = ctx.Logger(f)
	if f.ModuleWsgi != "" {
		w, err := NewWsgi(f.ModuleWsgi, f.WorkingDir, f.VenvPath)
		if err != nil {
			return err
		}
		if f.Lifespan != "" {
			f.logger.Warn("lifespan is only used in ASGI mode", zap.String("lifespan", f.Lifespan))
		}
		f.logger.Info("imported wsgi app", zap.String("module_wsgi", f.ModuleWsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath))
		f.app = w
	} else if f.ModuleAsgi != "" {
		var err error
		f.app, err = NewAsgi(f.ModuleAsgi, f.WorkingDir, f.VenvPath, f.Lifespan == "on", f.logger)
		if err != nil {
			return err
		}
		f.logger.Info("imported asgi app", zap.String("module_asgi", f.ModuleAsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath))
	} else {
		return errors.New("asgi or wsgi app needs to be specified")
	}
	return nil
}

// Validate implements caddy.Validator.
func (m *CaddySnake) Validate() error {
	return nil
}

// Cleanup frees resources uses by module
func (m *CaddySnake) Cleanup() error {
	if m != nil && m.app != nil {
		m.logger.Info("cleaning up module")
		return m.app.Cleanup()
	}
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (f CaddySnake) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if err := f.app.HandleRequest(w, r); err != nil {
		return err
	}
	return next.ServeHTTP(w, r)
}

// Interface guards
var (
	_ caddy.Provisioner           = (*CaddySnake)(nil)
	_ caddy.Validator             = (*CaddySnake)(nil)
	_ caddy.CleanerUpper          = (*CaddySnake)(nil)
	_ caddyhttp.MiddlewareHandler = (*CaddySnake)(nil)
	_ caddyfile.Unmarshaler       = (*CaddySnake)(nil)
)

func parsePythonDirective(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var app CaddySnake
	if err := app.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return app, nil
}

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

func init() {
	setupPy := C.CString(caddysnake_py)
	defer C.free(unsafe.Pointer(setupPy))
	C.Py_init_and_release_gil(setupPy)
	caddy.RegisterModule(CaddySnake{})
	httpcaddyfile.RegisterHandlerDirective("python", parsePythonDirective)
	initWsgi()
	initAsgi()
}

// findSitePackagesInVenv searches for the site-packages directory in a given venv.
// It returns the absolute path to the site-packages directory if found, or an error otherwise.
func findSitePackagesInVenv(venvPath string) (string, error) {
	libPath := filepath.Join(venvPath, "lib")
	pythonDir, err := findPythonDirectory(libPath)
	if err != nil {
		return "", err
	}
	sitePackagesPath := filepath.Join(libPath, pythonDir, "site-packages")
	fileInfo, err := os.Stat(sitePackagesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("site-packages directory does not exist in: %s", sitePackagesPath)
		}
		return "", err
	}
	if !fileInfo.IsDir() {
		return "", fmt.Errorf("found site-packages is not a directory: %s", sitePackagesPath)
	}
	return sitePackagesPath, nil
}

// findWorkingDirectory checks if the directory exists and returns the absolute path
func findWorkingDirectory(workingDir string) (string, error) {
	workingDirAbs, err := filepath.Abs(workingDir)
	if err != nil {
		return "", err
	}
	fileInfo, err := os.Stat(workingDirAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("working_dir directory does not exist in: %s", workingDirAbs)
		}
		return "", err
	}
	if !fileInfo.IsDir() {
		return "", fmt.Errorf("working_dir is not a directory: %s", workingDirAbs)
	}
	return workingDirAbs, nil
}

// findPythonDirectory searches for a directory that matches "python3.*" inside the given libPath.
func findPythonDirectory(libPath string) (string, error) {
	var pythonDir string
	found := false
	filepath.Walk(libPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() || found {
			return nil
		}
		matched, _ := regexp.MatchString(`python3\..*`, info.Name())
		if matched {
			pythonDir = info.Name()
			found = true
			// Use an error to stop walking the directory tree
			return errors.New("python directory found")
		}
		return nil
	})
	if !found || pythonDir == "" {
		return "", errors.New("unable to find a python3.* directory in the venv")
	}
	return pythonDir, nil
}

// Wsgi stores a reference to a Python Wsgi application
type Wsgi struct {
	app         *C.WsgiApp
	wsgiPattern string
}

var wsgiAppCache map[string]*Wsgi = map[string]*Wsgi{}

// NewWsgi imports a WSGI app
func NewWsgi(wsgiPattern, workingDir, venvPath string) (*Wsgi, error) {
	wsgiState.Lock()
	defer wsgiState.Unlock()

	if app, ok := wsgiAppCache[wsgiPattern]; ok {
		return app, nil
	}

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

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := C.WsgiApp_import(moduleName, appName, workingDirPath, packagesPath)
	if app == nil {
		return nil, errors.New("failed to import module")
	}

	result := &Wsgi{app, wsgiPattern}
	wsgiAppCache[wsgiPattern] = result
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

		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		C.WsgiApp_cleanup(m.app)
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
		if k == "COOKIE" {
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

	runtime.LockOSThread()
	C.WsgiApp_handle_request(
		m.app,
		C.int64_t(requestID),
		requestHeaders.m,
		buffer,
		bufferLen,
	)
	runtime.UnlockOSThread()

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

// ASGI: Implementation

// Asgi stores a reference to a Python Asgi application
type Asgi struct {
	app         *C.AsgiApp
	asgiPattern string
	logger      *zap.Logger
}

var asgiAppCache map[string]*Asgi = map[string]*Asgi{}

// NewAsgi imports a Python ASGI app
func NewAsgi(asgiPattern, workingDir, venvPath string, lifespan bool, logger *zap.Logger) (*Asgi, error) {
	asgiState.Lock()
	defer asgiState.Unlock()

	if app, ok := asgiAppCache[asgiPattern]; ok {
		return app, nil
	}

	moduleApp := strings.Split(asgiPattern, ":")
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

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := C.AsgiApp_import(moduleName, appName, workingDirPath, packagesPath)
	if app == nil {
		return nil, errors.New("failed to import module")
	}

	var err error

	if lifespan {
		status := C.AsgiApp_lifespan_startup(app)
		if uint8(status) == 0 {
			err = errors.New("startup failed")
		}
	}

	result := &Asgi{app, asgiPattern, logger}
	asgiAppCache[asgiPattern] = result
	return result, err
}

// Cleanup deallocates CGO resources used by Asgi app
func (m *Asgi) Cleanup() (err error) {
	if m != nil && m.app != nil {
		asgiState.Lock()
		if _, ok := asgiAppCache[m.asgiPattern]; !ok {
			asgiState.Unlock()
			return
		}
		delete(asgiAppCache, m.asgiPattern)
		asgiState.Unlock()

		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		status := C.AsgiApp_lifespan_shutdown(m.app)
		if uint8(status) == 0 {
			err = errors.New("shutdown failure")
		}

		C.AsgiApp_cleanup(m.app)
	}
	return
}

type WebsocketState uint8

const (
	WS_STARTING WebsocketState = iota + 2
	WS_CONNECTED
	WS_DISCONNECTED
)

// AsgiRequestHandler stores pointers to the request and the response writer
type AsgiRequestHandler struct {
	event                   *C.AsgiEvent
	w                       http.ResponseWriter
	r                       *http.Request
	completedBody           bool
	completedResponse       bool
	accumulatedResponseSize int
	done                    chan error

	operations chan AsgiOperations

	websocket      bool
	websocketState WebsocketState
	websocketConn  *websocket.Conn
}

func (h *AsgiRequestHandler) Cleanup() {
	h.completedResponse = true
	h.operations <- AsgiOperations{stop: true}
}

// AsgiOperations stores operations that should be executed in the background
type AsgiOperations struct {
	stop bool
	op   func()
}

func (h *AsgiRequestHandler) consume() {
	for {
		o := <-h.operations
		if o.op != nil {
			o.op()
		}
		if o.stop {
			if h.event != nil {
				runtime.LockOSThread()
				C.AsgiEvent_cleanup(h.event)
				runtime.UnlockOSThread()
			}
			close(h.operations)
			break
		}
	}
}

// NewAsgiRequestHandler initializes handler and starts queue that consumes operations
// in the background.
func NewAsgiRequestHandler(w http.ResponseWriter, r *http.Request, websocket bool) *AsgiRequestHandler {
	h := &AsgiRequestHandler{
		w:    w,
		r:    r,
		done: make(chan error, 2),

		operations: make(chan AsgiOperations, 16),

		websocket: websocket,
	}
	go h.consume()
	return h
}

type AsgiGlobalState struct {
	sync.RWMutex
	requestCounter uint64
	handlers       map[uint64]*AsgiRequestHandler
}

func (s *AsgiGlobalState) Request(h *AsgiRequestHandler) uint64 {
	s.Lock()
	defer s.Unlock()
	s.requestCounter++
	s.handlers[s.requestCounter] = h
	return s.requestCounter
}

func (s *AsgiGlobalState) Cleanup(requestID uint64) {
	s.Lock()
	defer s.Unlock()
	delete(s.handlers, requestID)
}

func initAsgi() {
	asgiStateOnce.Do(func() {
		asgiState = &AsgiGlobalState{
			requestCounter: 0,
			handlers:       make(map[uint64]*AsgiRequestHandler),
		}
	})
}

var (
	asgiState     *AsgiGlobalState
	asgiStateOnce sync.Once
)

var upgrader = websocket.Upgrader{} // use default options

func getRemoteHostPort(r *http.Request) (string, int) {
	host, port, _ := net.SplitHostPort(r.RemoteAddr)
	portN, _ := strconv.Atoi(port)
	return host, portN
}

func needsWebsocketUpgrade(r *http.Request) bool {
	if r.Method != "GET" {
		return false
	}

	containsConnectionUpgrade := false
	for _, v := range r.Header.Values("connection") {
		if strings.Contains(strings.ToLower(v), "upgrade") {
			containsConnectionUpgrade = true
			break
		}
	}
	if !containsConnectionUpgrade {
		return false
	}

	containsUpgradeWebsockets := false
	for _, v := range r.Header.Values("upgrade") {
		if strings.Contains(strings.ToLower(v), "websocket") {
			containsUpgradeWebsockets = true
			break
		}
	}

	return containsUpgradeWebsockets
}

func buildAsgiHeaders(r *http.Request, websocket bool) (*MapKeyVal, *MapKeyVal, error) {
	decodedPath, err := url.PathUnescape(r.URL.Path)
	if err != nil {
		return nil, nil, err
	}
	var connType, scheme string
	if websocket {
		connType = "websocket"
		scheme = "ws"
		if r.TLS != nil {
			scheme = "wss"
		}
	} else {
		connType = "http"
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	scopeMap := map[string]string{
		"type":         connType,
		"http_version": fmt.Sprintf("%d.%d", r.ProtoMajor, r.ProtoMinor),
		"method":       r.Method,
		"scheme":       scheme,
		"path":         decodedPath,
		"raw_path":     r.URL.EscapedPath(),
		"query_string": r.URL.RawQuery,
		"root_path":    "",
	}
	scope := NewMapKeyVal(len(scopeMap))
	for k, v := range scopeMap {
		scope.Append(k, v)
	}

	requestHeaders := NewMapKeyVal(len(r.Header))
	for k, items := range r.Header {
		if k == "Proxy" {
			// golang cgi issue 16405
			continue
		}

		joinStr := ", "
		if k == "Cookie" {
			joinStr = "; "
		}

		requestHeaders.Append(strings.ToLower(k), strings.Join(items, joinStr))
	}

	return requestHeaders, scope, nil
}

// HandleRequest passes request down to Python ASGI app and writes responses and headers.
func (m *Asgi) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	host, port := getHostPort(r)
	serverHostStr := C.CString(host)
	defer C.free(unsafe.Pointer(serverHostStr))

	clientHost, clientPort := getRemoteHostPort(r)
	clientHostStr := C.CString(clientHost)
	defer C.free(unsafe.Pointer(clientHostStr))

	websocket := needsWebsocketUpgrade(r)

	requestHeaders, scope, err := buildAsgiHeaders(r, websocket)
	if err != nil {
		return err
	}
	defer requestHeaders.Cleanup()
	defer scope.Cleanup()

	arh := NewAsgiRequestHandler(w, r, websocket)
	defer arh.Cleanup()

	requestID := asgiState.Request(arh)
	defer asgiState.Cleanup(requestID)

	var subprotocols *C.char = nil
	if websocket {
		subprotocols = C.CString(r.Header.Get("sec-websocket-protocol"))
		defer C.free(unsafe.Pointer(subprotocols))
	}

	runtime.LockOSThread()
	C.AsgiApp_handle_request(
		m.app,
		C.uint64_t(requestID),
		scope.m,
		requestHeaders.m,
		clientHostStr,
		C.int(clientPort),
		serverHostStr,
		C.int(port),
		subprotocols,
	)
	runtime.UnlockOSThread()

	if err := <-arh.done; err != nil {
		w.WriteHeader(500)
		m.logger.Debug(err.Error())
	}

	return nil
}

func (s *AsgiGlobalState) GetHandler(requestID uint64) *AsgiRequestHandler {
	s.RLock()
	defer s.RUnlock()
	h := s.handlers[requestID]
	return h
}

func (h *AsgiRequestHandler) SetWebsocketError(event *C.AsgiEvent, err error) {
	closeError, isClose := err.(*websocket.CloseError)
	closeCode := 1005
	if isClose {
		closeCode = closeError.Code
	}
	closeStr := fmt.Sprintf("%d", closeCode)
	bodyStr := C.CString(closeStr)
	bodyLen := C.size_t(len(closeStr))
	defer C.free(unsafe.Pointer(bodyStr))
	h.websocketState = WS_DISCONNECTED
	h.websocketConn.Close()
	runtime.LockOSThread()
	C.AsgiEvent_disconnect_websocket(event)
	C.AsgiEvent_set_websocket(event, bodyStr, bodyLen, C.uint8_t(0), C.uint8_t(0))
	runtime.UnlockOSThread()
	h.done <- fmt.Errorf("websocket closed: %d", closeCode)
}

func (h *AsgiRequestHandler) ReadWebsocketMessage(event *C.AsgiEvent) {
	mt, message, err := h.websocketConn.ReadMessage()
	if err != nil {
		h.SetWebsocketError(event, err)
		return
	}
	message = append(message, 0)
	bodyStr := (*C.char)(unsafe.Pointer(&message[0]))
	bodyLen := C.size_t(len(message) - 1)

	messageType := C.uint8_t(0)
	if mt == websocket.BinaryMessage {
		messageType = C.uint8_t(1)
	}

	runtime.LockOSThread()
	C.AsgiEvent_set_websocket(event, bodyStr, bodyLen, messageType, C.uint8_t(0))
	runtime.UnlockOSThread()
}

func (h *AsgiRequestHandler) DisconnectWebsocket(event *C.AsgiEvent) {
	runtime.LockOSThread()
	C.AsgiEvent_disconnect_websocket(event)
	C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(0))
	runtime.UnlockOSThread()
	h.done <- errors.New("websocket closed - receive start")
}

func (h *AsgiRequestHandler) ConnectWebsocket(event *C.AsgiEvent) {
	h.websocketState = WS_STARTING
	runtime.LockOSThread()
	C.AsgiEvent_connect_websocket(event)
	C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(0))
	runtime.UnlockOSThread()
}

func (h *AsgiRequestHandler) HandleWebsocket(event *C.AsgiEvent) C.uint8_t {
	switch h.websocketState {
	case WS_STARTING:
		panic("ASSERTION: websocket state should be WS_CONNECTED or WS_DISCONNECTED")
	case WS_CONNECTED:
		go h.ReadWebsocketMessage(event)
	case WS_DISCONNECTED:
		go h.DisconnectWebsocket(event)
	default:
		h.ConnectWebsocket(event)
	}
	return C.uint8_t(1)
}

func (h *AsgiRequestHandler) SetEvent(event *C.AsgiEvent) {
	h.event = event
}

func (h *AsgiRequestHandler) readBody(event *C.AsgiEvent) {
	var bodyStr *C.char
	var bodyLen C.size_t
	var moreBody C.uint8_t
	if !h.completedBody {
		buffer := make([]byte, 1<<16)
		n, err := h.r.Body.Read(buffer)
		if err != nil && err != io.EOF {
			h.done <- err
			return
		}
		h.completedBody = (err == io.EOF)
		buffer = append(buffer[:n], 0)
		bodyStr = (*C.char)(unsafe.Pointer(&buffer[0]))
		bodyLen = C.size_t(len(buffer) - 1) // -1 to remove null-terminator
	}

	if h.completedBody {
		moreBody = C.uint8_t(0)
	} else {
		moreBody = C.uint8_t(1)
	}

	runtime.LockOSThread()
	C.AsgiEvent_set(event, bodyStr, bodyLen, moreBody, C.uint8_t(0))
	runtime.UnlockOSThread()
}

func (h *AsgiRequestHandler) ReceiveStart(event *C.AsgiEvent) C.uint8_t {
	h.operations <- AsgiOperations{op: func() {
		h.readBody(event)
	}}
	return C.uint8_t(1)
}

func (h *AsgiRequestHandler) UpgradeWebsockets(headers http.Header, event *C.AsgiEvent) {
	wsConn, err := upgrader.Upgrade(h.w, h.r, headers)
	if err != nil {
		h.websocketState = WS_DISCONNECTED
		h.websocketConn.Close()
		runtime.LockOSThread()
		C.AsgiEvent_disconnect_websocket(event)
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
		return
	}
	h.websocketState = WS_CONNECTED
	h.websocketConn = wsConn

	runtime.LockOSThread()
	C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
	runtime.UnlockOSThread()
}

func (h *AsgiRequestHandler) HandleWebsocketHeaders(statusCode C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	wsHeaders := h.w.Header().Clone()
	if headers != nil {
		mapHeaders := NewMapKeyValFromSource(headers)
		defer mapHeaders.Cleanup()

		for i := range mapHeaders.Len() {
			headerName, headerValue := mapHeaders.Get(i)
			wsHeaders.Add(headerName, headerValue)
		}
	}
	switch h.websocketState {
	case WS_STARTING:
		h.UpgradeWebsockets(wsHeaders, event)
	case WS_DISCONNECTED:
		runtime.LockOSThread()
		C.AsgiEvent_disconnect_websocket(event)
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
	}
}

func (h *AsgiRequestHandler) HandleHeaders(statusCode C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	h.operations <- AsgiOperations{op: func() {
		if headers != nil {
			mapHeaders := NewMapKeyValFromSource(headers)
			defer mapHeaders.Cleanup()

			for i := 0; i < mapHeaders.Len(); i++ {
				headerName, headerValue := mapHeaders.Get(i)
				h.w.Header().Add(headerName, headerValue)
			}
		}

		h.w.WriteHeader(int(statusCode))

		runtime.LockOSThread()
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
	}}
}

func (h *AsgiRequestHandler) SendResponse(body *C.char, bodyLen C.size_t, moreBody C.uint8_t, event *C.AsgiEvent) {
	h.operations <- AsgiOperations{op: func() {
		defer C.free(unsafe.Pointer(body))
		bodyBytes := C.GoBytes(unsafe.Pointer(body), C.int(bodyLen))
		h.accumulatedResponseSize += len(bodyBytes)
		_, err := h.w.Write(bodyBytes)
		if f, ok := h.w.(http.Flusher); ok {
			f.Flush()
		}
		if err != nil {
			h.done <- err
		} else if int(moreBody) == 0 {
			h.done <- nil
		}

		runtime.LockOSThread()
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
	}}
}

func (h *AsgiRequestHandler) SendResponseWebsocket(body *C.char, bodyLen C.size_t, messageType C.uint8_t, event *C.AsgiEvent) {
	h.operations <- AsgiOperations{op: func() {
		defer C.free(unsafe.Pointer(body))
		var bodyBytes []byte
		var wsMessageType int
		if messageType == C.uint8_t(0) {
			wsMessageType = websocket.TextMessage
			bodyBytes = []byte(C.GoString(body))
		} else {
			wsMessageType = websocket.BinaryMessage
			bodyBytes = C.GoBytes(unsafe.Pointer(body), C.int(bodyLen))
		}
		err := h.websocketConn.WriteMessage(wsMessageType, bodyBytes)
		if err != nil {
			h.websocketState = WS_DISCONNECTED
			h.websocketConn.Close()
			runtime.LockOSThread()
			C.AsgiEvent_disconnect_websocket(event)
			C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
			runtime.UnlockOSThread()
			return
		}

		runtime.LockOSThread()
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
	}}
}

func (h *AsgiRequestHandler) CancelRequest() {
	h.done <- errors.New("request cancelled")
}

//export asgi_receive_start
func asgi_receive_start(requestID C.uint64_t, event *C.AsgiEvent) C.uint8_t {
	h := asgiState.GetHandler(uint64(requestID))
	if h == nil || h.completedResponse {
		return C.uint8_t(0)
	}
	h.SetEvent(event)

	if h.websocket {
		return h.HandleWebsocket(event)
	}

	return h.ReceiveStart(event)
}

//export asgi_set_headers
func asgi_set_headers(requestID C.uint64_t, statusCode C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	h := asgiState.GetHandler(uint64(requestID))
	h.SetEvent(event)

	if h.websocket {
		h.HandleWebsocketHeaders(statusCode, headers, event)
		return
	}

	h.HandleHeaders(statusCode, headers, event)
}

//export asgi_send_response
func asgi_send_response(requestID C.uint64_t, body *C.char, bodyLen C.size_t, moreBody C.uint8_t, event *C.AsgiEvent) {
	h := asgiState.GetHandler(uint64(requestID))
	h.SetEvent(event)

	h.SendResponse(body, bodyLen, moreBody, event)
}

//export asgi_send_response_websocket
func asgi_send_response_websocket(requestID C.uint64_t, body *C.char, bodyLen C.size_t, messageType C.uint8_t, event *C.AsgiEvent) {
	h := asgiState.GetHandler(uint64(requestID))
	h.SetEvent(event)

	h.SendResponseWebsocket(body, bodyLen, messageType, event)
}

//export asgi_cancel_request
func asgi_cancel_request(requestID C.uint64_t) {
	h := asgiState.GetHandler(uint64(requestID))
	if h != nil {
		h.CancelRequest()
	}
}

//export asgi_cancel_request_websocket
func asgi_cancel_request_websocket(requestID C.uint64_t, reason *C.char, code C.int) {
	asgiState.RLock()
	defer asgiState.RUnlock()
	arh, ok := asgiState.handlers[uint64(requestID)]
	if ok {
		var reasonText string
		if reason != nil {
			defer C.free(unsafe.Pointer(reason))
			reasonText = C.GoString(reason)
		}
		closeCode := int(code)
		if arh.websocketState == WS_STARTING {
			arh.w.WriteHeader(403)
			arh.done <- fmt.Errorf("websocket closed: %d '%s'", closeCode, reasonText)
		} else if arh.websocketState == WS_CONNECTED {
			arh.websocketState = WS_DISCONNECTED
			closeMessage := websocket.FormatCloseMessage(closeCode, reasonText)
			go func() {
				if arh.websocketConn != nil {
					arh.websocketConn.WriteControl(websocket.CloseMessage, closeMessage, time.Now().Add(5*time.Second))
					arh.websocketConn.Close()
					arh.done <- fmt.Errorf("websocket closed: %d '%s'", closeCode, reasonText)
				}
			}()
		}
	}
}
