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
	m            *C.MapKeyVal
	base_headers uintptr
	base_values  uintptr
}

func NewMapKeyVal(count int) *MapKeyVal {
	m := C.MapKeyVal_new(C.size_t(count))
	return &MapKeyVal{
		m:            m,
		base_headers: uintptr(unsafe.Pointer(m.keys)),
		base_values:  uintptr(unsafe.Pointer(m.values)),
	}
}

func (m *MapKeyVal) Cleanup() {
	if m.m != nil {
		C.MapKeyVal_free(m.m, m.m.count)
	}
}

func (m *MapKeyVal) Set(k, v string, pos int) {
	if pos < 0 || pos > int(m.m.count) {
		panic("Expected pos to be within limits")
	}
	*(**C.char)(unsafe.Pointer(m.base_headers + uintptr(pos)*SIZE_OF_CHAR_POINTER)) = C.CString(k)
	*(**C.char)(unsafe.Pointer(m.base_values + uintptr(pos)*SIZE_OF_CHAR_POINTER)) = C.CString(v)
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

// WsgiRequestHandler tracks the state of a HTTP request to a WSGI App
type WsgiRequestHandler struct {
	status_code C.int
	headers     *C.MapKeyVal
	body        *C.char
	body_size   C.size_t
}

var wsgi_lock sync.RWMutex = sync.RWMutex{}
var wsgi_request_counter int64 = 0
var wsgi_handlers map[int64]chan WsgiRequestHandler = map[int64]chan WsgiRequestHandler{}

func init() {
	setup_py := C.CString(caddysnake_py)
	defer C.free(unsafe.Pointer(setup_py))
	C.Py_init_and_release_gil(setup_py)
	caddy.RegisterModule(CaddySnake{})
	httpcaddyfile.RegisterHandlerDirective("python", parsePythonDirective)
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
func findWorkingDirectory(working_dir string) (string, error) {
	working_dir_abs, err := filepath.Abs(working_dir)
	if err != nil {
		return "", err
	}
	fileInfo, err := os.Stat(working_dir_abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("working_dir directory does not exist in: %s", working_dir_abs)
		}
		return "", err
	}
	if !fileInfo.IsDir() {
		return "", fmt.Errorf("working_dir is not a directory: %s", working_dir_abs)
	}
	return working_dir_abs, nil
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
	app          *C.WsgiApp
	wsgi_pattern string
}

var wsgiapp_cache map[string]*Wsgi = map[string]*Wsgi{}

// NewWsgi imports a WSGI app
func NewWsgi(wsgi_pattern, working_dir, venv_path string) (*Wsgi, error) {
	wsgi_lock.Lock()
	defer wsgi_lock.Unlock()

	if app, ok := wsgiapp_cache[wsgi_pattern]; ok {
		return app, nil
	}

	module_app := strings.Split(wsgi_pattern, ":")
	if len(module_app) != 2 {
		return nil, errors.New("expected pattern $(MODULE_NAME):$(VARIABLE_NAME)")
	}
	module_name := C.CString(module_app[0])
	defer C.free(unsafe.Pointer(module_name))
	app_name := C.CString(module_app[1])
	defer C.free(unsafe.Pointer(app_name))

	var packages_path *C.char = nil
	if venv_path != "" {
		sitePackagesPath, err := findSitePackagesInVenv(venv_path)
		if err != nil {
			return nil, err
		}
		packages_path = C.CString(sitePackagesPath)
		defer C.free(unsafe.Pointer(packages_path))
	}

	var working_dir_path *C.char = nil
	if working_dir != "" {
		working_dir_abs, err := findWorkingDirectory(working_dir)
		if err != nil {
			return nil, err
		}
		working_dir_path = C.CString(working_dir_abs)
		defer C.free(unsafe.Pointer(working_dir_path))
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := C.WsgiApp_import(module_name, app_name, working_dir_path, packages_path)
	if app == nil {
		return nil, errors.New("failed to import module")
	}

	result := &Wsgi{app, wsgi_pattern}
	wsgiapp_cache[wsgi_pattern] = result
	return result, nil
}

// Cleanup deallocates CGO resources used by Wsgi app
func (m *Wsgi) Cleanup() error {
	if m.app != nil {
		wsgi_lock.Lock()
		if _, ok := wsgiapp_cache[m.wsgi_pattern]; !ok {
			wsgi_lock.Unlock()
			return nil
		}
		delete(wsgiapp_cache, m.wsgi_pattern)
		wsgi_lock.Unlock()

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

// HandleRequest passes request down to Python Wsgi app and writes responses and headers.
func (m *Wsgi) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	srvAddr := ctx.Value(http.LocalAddrContextKey).(net.Addr)
	_, port, _ := net.SplitHostPort(srvAddr.String())
	host, _, _ := net.SplitHostPort(r.Host)
	if host == "" {
		// net.SplitHostPort returns error and an empty host when port is missing
		host = r.Host
	}
	extra_headers := map[string]string{
		"SERVER_NAME":     host,
		"SERVER_PORT":     port,
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
	headers_length := len(r.Header)
	if _, ok := r.Header[textproto.CanonicalMIMEHeaderKey("Proxy")]; ok {
		headers_length -= 1
	}
	if _, ok := r.Header[textproto.CanonicalMIMEHeaderKey("Content-Type")]; ok {
		headers_length -= 1
	}
	if _, ok := r.Header[textproto.CanonicalMIMEHeaderKey("Content-Length")]; ok {
		headers_length -= 1
	}
	rh := NewMapKeyVal(headers_length + len(extra_headers))
	defer rh.Cleanup()
	i := 0
	for k, items := range r.Header {
		key := strings.Map(upperCaseAndUnderscore, k)
		if key == "PROXY" {
			// golang cgi issue 16405
			continue
		}
		// Content type and length already defined in extra_headers
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

		rh.Set("HTTP_"+key, strings.Join(items, joinStr), i)
		i++
	}
	for k, v := range extra_headers {
		rh.Set(k, v, i)
		i++
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	body_str := C.CString(string(body))
	defer C.free(unsafe.Pointer(body_str))

	ch := make(chan WsgiRequestHandler)
	wsgi_lock.Lock()
	wsgi_request_counter++
	request_id := wsgi_request_counter
	wsgi_handlers[request_id] = ch
	wsgi_lock.Unlock()

	runtime.LockOSThread()
	C.WsgiApp_handle_request(m.app, C.int64_t(request_id), rh, body_str)
	runtime.UnlockOSThread()

	h := <-ch

	if h.headers != nil {
		defer C.free(unsafe.Pointer(h.headers))
		defer C.free(unsafe.Pointer(h.headers.keys))
		defer C.free(unsafe.Pointer(h.headers.values))

		for i := 0; i < int(h.headers.count); i++ {
			header_name_ptr := unsafe.Pointer(uintptr(unsafe.Pointer(h.headers.keys)) + uintptr(i)*size_of_char_pointer)
			header_value_ptr := unsafe.Pointer(uintptr(unsafe.Pointer(h.headers.values)) + uintptr(i)*size_of_char_pointer)
			header_name := *(**C.char)(header_name_ptr)
			defer C.free(unsafe.Pointer(header_name))
			header_value := *(**C.char)(header_value_ptr)
			defer C.free(unsafe.Pointer(header_value))
			w.Header().Add(C.GoString(header_name), C.GoString(header_value))
		}
	}

	w.WriteHeader(int(h.status_code))

	if h.body != nil {
		defer C.free(unsafe.Pointer(h.body))
		body_bytes := C.GoBytes(unsafe.Pointer(h.body), C.int(h.body_size))
		w.Write(body_bytes)
	} else if h.status_code == 500 {
		w.Write([]byte("Internal Server Error"))
	}

	return nil
}

//export wsgi_write_response
func wsgi_write_response(request_id C.int64_t, status_code C.int, headers *C.MapKeyVal, body *C.char, body_size C.size_t) {
	wsgi_lock.Lock()
	defer wsgi_lock.Unlock()
	ch := wsgi_handlers[int64(request_id)]
	ch <- WsgiRequestHandler{
		status_code: status_code,
		body:        body,
		body_size:   body_size,
		headers:     headers,
	}
	delete(wsgi_handlers, int64(request_id))
}

// ASGI: Implementation

// Asgi stores a reference to a Python Asgi application
type Asgi struct {
	app          *C.AsgiApp
	asgi_pattern string
	logger       *zap.Logger
}

var asgiapp_cache map[string]*Asgi = map[string]*Asgi{}

// NewAsgi imports a Python ASGI app
func NewAsgi(asgi_pattern, working_dir, venv_path string, lifespan bool, logger *zap.Logger) (*Asgi, error) {
	asgi_lock.Lock()
	defer asgi_lock.Unlock()

	if app, ok := asgiapp_cache[asgi_pattern]; ok {
		return app, nil
	}

	module_app := strings.Split(asgi_pattern, ":")
	if len(module_app) != 2 {
		return nil, errors.New("expected pattern $(MODULE_NAME):$(VARIABLE_NAME)")
	}
	module_name := C.CString(module_app[0])
	defer C.free(unsafe.Pointer(module_name))
	app_name := C.CString(module_app[1])
	defer C.free(unsafe.Pointer(app_name))

	var packages_path *C.char = nil
	if venv_path != "" {
		sitePackagesPath, err := findSitePackagesInVenv(venv_path)
		if err != nil {
			return nil, err
		}
		packages_path = C.CString(sitePackagesPath)
		defer C.free(unsafe.Pointer(packages_path))
	}

	var working_dir_path *C.char = nil
	if working_dir != "" {
		working_dir_abs, err := findWorkingDirectory(working_dir)
		if err != nil {
			return nil, err
		}
		working_dir_path = C.CString(working_dir_abs)
		defer C.free(unsafe.Pointer(working_dir_path))
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := C.AsgiApp_import(module_name, app_name, working_dir_path, packages_path)
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

	result := &Asgi{app, asgi_pattern, logger}
	asgiapp_cache[asgi_pattern] = result
	return result, err
}

// Cleanup deallocates CGO resources used by Asgi app
func (m *Asgi) Cleanup() (err error) {
	if m != nil && m.app != nil {
		asgi_lock.Lock()
		if _, ok := asgiapp_cache[m.asgi_pattern]; !ok {
			asgi_lock.Unlock()
			return
		}
		delete(asgiapp_cache, m.asgi_pattern)
		asgi_lock.Unlock()

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

type WsMessage struct {
	mt      int
	message []byte
}

// AsgiRequestHandler stores pointers to the request and the response writer
type AsgiRequestHandler struct {
	event                     *C.AsgiEvent
	w                         http.ResponseWriter
	r                         *http.Request
	completed_body            bool
	completed_response        bool
	accumulated_response_size int
	done                      chan error

	operations chan AsgiOperations

	is_websocket    bool
	websocket_state WebsocketState
	websocket_conn  *websocket.Conn
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
func NewAsgiRequestHandler(w http.ResponseWriter, r *http.Request) *AsgiRequestHandler {
	h := &AsgiRequestHandler{
		w:    w,
		r:    r,
		done: make(chan error, 2),

		operations: make(chan AsgiOperations, 16),
	}
	go h.consume()
	return h
}

var asgi_lock sync.RWMutex = sync.RWMutex{}
var asgi_request_counter uint64 = 0
var asgi_handlers map[uint64]*AsgiRequestHandler = map[uint64]*AsgiRequestHandler{}
var upgrader = websocket.Upgrader{} // use default options

// HandleRequest passes request down to Python ASGI app and writes responses and headers.
func (m *Asgi) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	srvAddr := ctx.Value(http.LocalAddrContextKey).(net.Addr)
	_, server_port_string, _ := net.SplitHostPort(srvAddr.String())
	server_port, _ := strconv.Atoi(server_port_string)
	server_host, _, _ := net.SplitHostPort(r.Host)
	if server_host == "" {
		// net.SplitHostPort returns error and an empty host when port is missing
		server_host = r.Host
	}
	server_host_str := C.CString(server_host)
	defer C.free(unsafe.Pointer(server_host_str))
	client_host, client_port_string, _ := net.SplitHostPort(r.RemoteAddr)
	client_port, _ := strconv.Atoi(client_port_string)
	client_host_str := C.CString(client_host)
	defer C.free(unsafe.Pointer(client_host_str))

	contains_connection_upgrade := false
	for _, v := range r.Header.Values("connection") {
		if strings.Contains(strings.ToLower(v), "upgrade") {
			contains_connection_upgrade = true
			break
		}
	}
	contains_upgrade_websockets := false
	for _, v := range r.Header.Values("upgrade") {
		if strings.Contains(strings.ToLower(v), "websocket") {
			contains_upgrade_websockets = true
			break
		}
	}
	is_websocket := contains_connection_upgrade && contains_upgrade_websockets && r.Method == "GET"

	decodedPath, err := url.PathUnescape(r.URL.Path)
	if err != nil {
		return err
	}
	var conn_type, scheme string
	if is_websocket {
		conn_type = "websocket"
		scheme = "ws"
		if r.TLS != nil {
			scheme = "wss"
		}
	} else {
		conn_type = "http"
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	scope_map := map[string]string{
		"type":         conn_type,
		"http_version": fmt.Sprintf("%d.%d", r.ProtoMajor, r.ProtoMinor),
		"method":       r.Method,
		"scheme":       scheme,
		"path":         decodedPath,
		"raw_path":     r.URL.EscapedPath(),
		"query_string": r.URL.RawQuery,
		"root_path":    "",
	}
	scope := C.MapKeyVal_new(C.size_t(len(scope_map)))
	defer C.free(unsafe.Pointer(scope))
	defer C.free(unsafe.Pointer(scope.keys))
	defer C.free(unsafe.Pointer(scope.values))
	scope_count := 0
	base_of_keys := uintptr(unsafe.Pointer(scope.keys))
	base_of_values := uintptr(unsafe.Pointer(scope.values))
	size_of_pointer := unsafe.Sizeof(scope.keys)
	for k, v := range scope_map {
		key_str := C.CString(k)
		defer C.free(unsafe.Pointer(key_str))
		value_str := C.CString(v)
		defer C.free(unsafe.Pointer(value_str))
		*(**C.char)(unsafe.Pointer(base_of_keys + uintptr(scope_count)*size_of_pointer)) = key_str
		*(**C.char)(unsafe.Pointer(base_of_values + uintptr(scope_count)*size_of_pointer)) = value_str
		scope_count++
	}

	request_headers := C.MapKeyVal_new(C.size_t(len(r.Header)))
	defer C.free(unsafe.Pointer(request_headers))
	defer C.free(unsafe.Pointer(request_headers.keys))
	defer C.free(unsafe.Pointer(request_headers.values))
	header_count := 0
	base_of_keys = uintptr(unsafe.Pointer(request_headers.keys))
	base_of_values = uintptr(unsafe.Pointer(request_headers.values))
	for k, items := range r.Header {
		if k == "Proxy" {
			// golang cgi issue 16405
			continue
		}

		joinStr := ", "
		if k == "Cookie" {
			joinStr = "; "
		}

		key_str := C.CString(strings.ToLower(k))
		defer C.free(unsafe.Pointer(key_str))
		value_str := C.CString(strings.Join(items, joinStr))
		defer C.free(unsafe.Pointer(value_str))
		*(**C.char)(unsafe.Pointer(base_of_keys + uintptr(header_count)*size_of_pointer)) = key_str
		*(**C.char)(unsafe.Pointer(base_of_values + uintptr(header_count)*size_of_pointer)) = value_str
		header_count++
	}

	arh := NewAsgiRequestHandler(w, r)
	arh.is_websocket = is_websocket

	asgi_lock.Lock()
	asgi_request_counter++
	request_id := asgi_request_counter
	asgi_handlers[request_id] = arh
	asgi_lock.Unlock()
	defer func() {
		arh.completed_response = true
		arh.operations <- AsgiOperations{stop: true}
		asgi_lock.Lock()
		delete(asgi_handlers, request_id)
		asgi_lock.Unlock()
	}()

	var subprotocols *C.char = nil
	if is_websocket {
		subprotocols = C.CString(r.Header.Get("sec-websocket-protocol"))
		defer C.free(unsafe.Pointer(subprotocols))
	}

	runtime.LockOSThread()
	C.AsgiApp_handle_request(
		m.app,
		C.uint64_t(request_id),
		scope,
		request_headers,
		client_host_str,
		C.int(client_port),
		server_host_str,
		C.int(server_port),
		subprotocols,
	)
	runtime.UnlockOSThread()

	if err := <-arh.done; err != nil {
		w.WriteHeader(500)
		m.logger.Debug(err.Error())
	}

	return nil
}

//export asgi_receive_start
func asgi_receive_start(request_id C.uint64_t, event *C.AsgiEvent) C.uint8_t {
	asgi_lock.Lock()
	defer asgi_lock.Unlock()
	arh := asgi_handlers[uint64(request_id)]
	if arh == nil || arh.completed_response {
		return C.uint8_t(0)
	}

	arh.event = event

	if arh.is_websocket {
		switch arh.websocket_state {
		case WS_STARTING:
			// TODO: this shouldn't happen, what do I do here?
			fmt.Println("SHOULD NOT SEE THIS - PLEASE REPORT")
		case WS_CONNECTED:
			go func() {
				mt, message, err := arh.websocket_conn.ReadMessage()
				if err != nil {
					closeError, isClose := err.(*websocket.CloseError)
					closeCode := 1005
					if isClose {
						closeCode = closeError.Code
					}
					body_str := C.CString(fmt.Sprintf("%d", closeCode))
					defer C.free(unsafe.Pointer(body_str))
					arh.websocket_state = WS_DISCONNECTED
					arh.websocket_conn.Close()
					runtime.LockOSThread()
					C.AsgiEvent_disconnect_websocket(event)
					C.AsgiEvent_set_websocket(event, body_str, C.uint8_t(0), C.uint8_t(0))
					runtime.UnlockOSThread()
					arh.done <- fmt.Errorf("websocket closed: %d", closeCode)
					return
				}
				body_str := C.CString(string(message))
				defer C.free(unsafe.Pointer(body_str))

				message_type := C.uint8_t(0)
				if mt == websocket.BinaryMessage {
					message_type = C.uint8_t(1)
				}

				runtime.LockOSThread()
				C.AsgiEvent_set_websocket(event, body_str, message_type, C.uint8_t(0))
				runtime.UnlockOSThread()
			}()
		case WS_DISCONNECTED:
			go func() {
				runtime.LockOSThread()
				C.AsgiEvent_disconnect_websocket(event)
				C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(0))
				runtime.UnlockOSThread()
				arh.done <- errors.New("websocket closed - receive start")
			}()
		default:
			arh.websocket_state = WS_STARTING
			runtime.LockOSThread()
			C.AsgiEvent_connect_websocket(event)
			C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(0))
			runtime.UnlockOSThread()
		}
		return C.uint8_t(1)
	}

	arh.operations <- AsgiOperations{op: func() {
		var body_str *C.char
		var more_body C.uint8_t
		if !arh.completed_body {
			buffer := make([]byte, 1<<16)
			_, err := arh.r.Body.Read(buffer)
			if err != nil && err != io.EOF {
				arh.done <- err
				return
			}
			arh.completed_body = (err == io.EOF)
			body_str = C.CString(string(buffer))
			defer C.free(unsafe.Pointer(body_str))
		}

		if arh.completed_body {
			more_body = C.uint8_t(0)
		} else {
			more_body = C.uint8_t(1)
		}

		runtime.LockOSThread()
		C.AsgiEvent_set(event, body_str, more_body, C.uint8_t(0))
		runtime.UnlockOSThread()
	}}

	return C.uint8_t(1)
}

//export asgi_set_headers
func asgi_set_headers(request_id C.uint64_t, status_code C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	asgi_lock.Lock()
	defer asgi_lock.Unlock()
	arh := asgi_handlers[uint64(request_id)]

	arh.event = event

	if arh.is_websocket {
		ws_headers := arh.w.Header().Clone()
		if headers != nil {
			size_of_pointer := unsafe.Sizeof(headers.keys)
			defer C.free(unsafe.Pointer(headers))
			defer C.free(unsafe.Pointer(headers.keys))
			defer C.free(unsafe.Pointer(headers.values))

			for i := 0; i < int(headers.count); i++ {
				header_name_ptr := unsafe.Pointer(uintptr(unsafe.Pointer(headers.keys)) + uintptr(i)*size_of_pointer)
				header_value_ptr := unsafe.Pointer(uintptr(unsafe.Pointer(headers.values)) + uintptr(i)*size_of_pointer)
				header_name := *(**C.char)(header_name_ptr)
				defer C.free(unsafe.Pointer(header_name))
				header_value := *(**C.char)(header_value_ptr)
				defer C.free(unsafe.Pointer(header_value))
				ws_headers.Add(C.GoString(header_name), C.GoString(header_value))
			}
		}
		switch arh.websocket_state {
		case WS_STARTING:
			ws_conn, err := upgrader.Upgrade(arh.w, arh.r, ws_headers)
			if err != nil {
				arh.websocket_state = WS_DISCONNECTED
				arh.websocket_conn.Close()
				runtime.LockOSThread()
				C.AsgiEvent_disconnect_websocket(event)
				C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(1))
				runtime.UnlockOSThread()
				return
			}
			arh.websocket_state = WS_CONNECTED
			arh.websocket_conn = ws_conn

			runtime.LockOSThread()
			C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(1))
			runtime.UnlockOSThread()
		case WS_DISCONNECTED:
			runtime.LockOSThread()
			C.AsgiEvent_disconnect_websocket(event)
			C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(1))
			runtime.UnlockOSThread()
		}
		return
	}

	arh.operations <- AsgiOperations{op: func() {
		if headers != nil {
			size_of_pointer := unsafe.Sizeof(headers.keys)
			defer C.free(unsafe.Pointer(headers))
			defer C.free(unsafe.Pointer(headers.keys))
			defer C.free(unsafe.Pointer(headers.values))

			for i := 0; i < int(headers.count); i++ {
				header_name_ptr := unsafe.Pointer(uintptr(unsafe.Pointer(headers.keys)) + uintptr(i)*size_of_pointer)
				header_value_ptr := unsafe.Pointer(uintptr(unsafe.Pointer(headers.values)) + uintptr(i)*size_of_pointer)
				header_name := *(**C.char)(header_name_ptr)
				defer C.free(unsafe.Pointer(header_name))
				header_value := *(**C.char)(header_value_ptr)
				defer C.free(unsafe.Pointer(header_value))
				arh.w.Header().Add(C.GoString(header_name), C.GoString(header_value))
			}
		}

		arh.w.WriteHeader(int(status_code))

		runtime.LockOSThread()
		C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
	}}
}

//export asgi_send_response
func asgi_send_response(request_id C.uint64_t, body *C.char, body_len C.size_t, more_body C.uint8_t, event *C.AsgiEvent) {
	asgi_lock.Lock()
	defer asgi_lock.Unlock()
	arh := asgi_handlers[uint64(request_id)]

	arh.event = event

	arh.operations <- AsgiOperations{op: func() {
		defer C.free(unsafe.Pointer(body))
		body_bytes := C.GoBytes(unsafe.Pointer(body), C.int(body_len))
		arh.accumulated_response_size += len(body_bytes)
		_, err := arh.w.Write(body_bytes)
		if f, ok := arh.w.(http.Flusher); ok {
			f.Flush()
		}
		if err != nil {
			arh.done <- err
		} else if int(more_body) == 0 {
			arh.done <- nil
		}

		runtime.LockOSThread()
		C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
	}}
}

//export asgi_send_response_websocket
func asgi_send_response_websocket(request_id C.uint64_t, body *C.char, body_len C.size_t, message_type C.uint8_t, event *C.AsgiEvent) {
	asgi_lock.Lock()
	defer asgi_lock.Unlock()
	arh := asgi_handlers[uint64(request_id)]

	arh.event = event

	arh.operations <- AsgiOperations{op: func() {
		defer C.free(unsafe.Pointer(body))
		var body_bytes []byte
		var ws_message_type int
		if message_type == C.uint8_t(0) {
			ws_message_type = websocket.TextMessage
			body_bytes = []byte(C.GoString(body))
		} else {
			ws_message_type = websocket.BinaryMessage
			body_bytes = C.GoBytes(unsafe.Pointer(body), C.int(body_len))
		}
		err := arh.websocket_conn.WriteMessage(ws_message_type, body_bytes)
		if err != nil {
			arh.websocket_state = WS_DISCONNECTED
			arh.websocket_conn.Close()
			runtime.LockOSThread()
			C.AsgiEvent_disconnect_websocket(event)
			C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(1))
			runtime.UnlockOSThread()
			return
		}

		runtime.LockOSThread()
		C.AsgiEvent_set(event, nil, C.uint8_t(0), C.uint8_t(1))
		runtime.UnlockOSThread()
	}}
}

//export asgi_cancel_request
func asgi_cancel_request(request_id C.uint64_t) {
	asgi_lock.Lock()
	defer asgi_lock.Unlock()
	arh, ok := asgi_handlers[uint64(request_id)]
	if ok {
		arh.done <- errors.New("request cancelled")
	}
}

//export asgi_cancel_request_websocket
func asgi_cancel_request_websocket(request_id C.uint64_t, reason *C.char, code C.int) {
	asgi_lock.Lock()
	defer asgi_lock.Unlock()
	arh, ok := asgi_handlers[uint64(request_id)]
	if ok {
		var reasonText string
		if reason != nil {
			defer C.free(unsafe.Pointer(reason))
			reasonText = C.GoString(reason)
		}
		closeCode := int(code)
		if arh.websocket_state == WS_STARTING {
			arh.w.WriteHeader(403)
			arh.done <- fmt.Errorf("websocket closed: %d '%s'", closeCode, reasonText)
		} else if arh.websocket_state == WS_CONNECTED {
			arh.websocket_state = WS_DISCONNECTED
			closeMessage := websocket.FormatCloseMessage(closeCode, reasonText)
			go func() {
				if arh.websocket_conn != nil {
					arh.websocket_conn.WriteControl(websocket.CloseMessage, closeMessage, time.Now().Add(5*time.Second))
					arh.websocket_conn.Close()
					arh.done <- fmt.Errorf("websocket closed: %d '%s'", closeCode, reasonText)
				}
			}()
		}
	}
}
