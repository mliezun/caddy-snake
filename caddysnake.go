// Caddy plugin that provides native support for Python WSGI apps.
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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

//go:embed caddysnake.py
var caddysnake_py string

type AppServer interface {
	Cleanup()
	HandleRequest(w http.ResponseWriter, r *http.Request) error
}

// CaddySnake module that communicates with a Wsgi app to handle requests
type CaddySnake struct {
	ModuleWsgi string `json:"module_wsgi,omitempty"`
	ModuleAsgi string `json:"module_asgi,omitempty"`
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
		w, err := NewWsgi(f.ModuleWsgi, f.VenvPath)
		if err != nil {
			return err
		}
		f.logger.Info("imported wsgi app", zap.String("module_wsgi", f.ModuleWsgi), zap.String("venv_path", f.VenvPath))
		f.app = w
	} else if f.ModuleAsgi != "" {
		a, err := NewAsgi(f.ModuleAsgi, f.VenvPath)
		if err != nil {
			return err
		}
		f.logger.Info("imported asgi app", zap.String("module_asgi", f.ModuleAsgi), zap.String("venv_path", f.VenvPath))
		f.app = a
	}
	return nil
}

// Validate implements caddy.Validator.
func (m *CaddySnake) Validate() error {
	return nil
}

// Cleanup frees resources uses by module
func (m *CaddySnake) Cleanup() error {
	if m.app != nil {
		m.logger.Info("cleaning up module")
		m.app.Cleanup()
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

// WsgiRequestHandler stores the result of a request handled by a Wsgi app
type WsgiRequestHandler struct {
	status_code C.int
	headers     *C.MapKeyVal
	body        *C.char
}

var lock sync.RWMutex = sync.RWMutex{}
var request_counter int64 = 0
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
	app *C.WsgiApp
}

func NewWsgi(wsgi_pattern string, venv_path string) (*Wsgi, error) {
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

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := C.WsgiApp_import(module_name, app_name, packages_path)
	if app == nil {
		return nil, errors.New("failed to import module")
	}
	return &Wsgi{app}, nil
}

// Cleanup deallocates CGO resources used by Wsgi app
func (m *Wsgi) Cleanup() {
	if m.app != nil {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		C.WsgiApp_cleanup(m.app)
	}
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
	rh := C.MapKeyVal_new(C.size_t(len(r.Header) + len(extra_headers)))
	defer C.free(unsafe.Pointer(rh))
	defer C.free(unsafe.Pointer(rh.keys))
	defer C.free(unsafe.Pointer(rh.values))
	i := 0
	size_of_char_pointer := unsafe.Sizeof(rh.keys)
	base_headers := uintptr(unsafe.Pointer(rh.keys))
	base_values := uintptr(unsafe.Pointer(rh.values))
	for k, items := range r.Header {
		key := strings.Map(upperCaseAndUnderscore, k)
		if key == "PROXY" {
			// golang cgi issue 16405
			continue
		}

		joinStr := ", "
		if k == "COOKIE" {
			joinStr = "; "
		}

		key_str := C.CString("HTTP_" + key)
		defer C.free(unsafe.Pointer(key_str))
		value_str := C.CString(strings.Join(items, joinStr))
		defer C.free(unsafe.Pointer(value_str))
		*(**C.char)(unsafe.Pointer(base_headers + uintptr(i)*size_of_char_pointer)) = key_str
		*(**C.char)(unsafe.Pointer(base_values + uintptr(i)*size_of_char_pointer)) = value_str
		i++
	}
	for k, v := range extra_headers {
		key_str := C.CString(k)
		defer C.free(unsafe.Pointer(key_str))
		value_str := C.CString(v)
		defer C.free(unsafe.Pointer(value_str))
		*(**C.char)(unsafe.Pointer(base_headers + uintptr(i)*size_of_char_pointer)) = key_str
		*(**C.char)(unsafe.Pointer(base_values + uintptr(i)*size_of_char_pointer)) = value_str
		i++
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	body_str := C.CString(string(body))
	defer C.free(unsafe.Pointer(body_str))

	ch := make(chan WsgiRequestHandler)
	lock.Lock()
	request_counter++
	request_id := request_counter
	wsgi_handlers[request_id] = ch
	lock.Unlock()

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
		w.Write([]byte(C.GoString(h.body)))
	} else if h.status_code == 500 {
		w.Write([]byte("Interal Server Error"))
	}

	return nil
}

//export wsgi_write_response
func wsgi_write_response(request_id C.int64_t, status_code C.int, headers *C.MapKeyVal, body *C.char) {
	lock.Lock()
	defer lock.Unlock()
	ch := wsgi_handlers[int64(request_id)]
	ch <- WsgiRequestHandler{
		status_code: status_code,
		body:        body,
		headers:     headers,
	}
	delete(wsgi_handlers, int64(request_id))
}

// ASGI: Implementation

// Asgi stores a reference to a Python Asgi application
type Asgi struct {
	app *C.AsgiApp
}

func NewAsgi(wsgi_pattern string, venv_path string) (*Asgi, error) {
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

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := C.AsgiApp_import(module_name, app_name, packages_path)
	if app == nil {
		return nil, errors.New("failed to import module")
	}
	return &Asgi{app}, nil
}

// Cleanup deallocates CGO resources used by Wsgi app
func (m *Asgi) Cleanup() {
	if m.app != nil {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		C.AsgiApp_cleanup(m.app)
	}
}

// AsgiRequestHandler stores pointers to the request and the response writer
type AsgiRequestHandler struct {
	w    http.ResponseWriter
	r    *http.Request
	done chan error

	operations chan AsgiOperations

	is_websocket bool
}

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
			break
		}
	}
}

func NewAsgiRequestHandler(w http.ResponseWriter, r *http.Request) *AsgiRequestHandler {
	h := &AsgiRequestHandler{
		w:    w,
		r:    r,
		done: make(chan error),

		operations: make(chan AsgiOperations, 4),
	}
	go h.consume()
	return h
}

var asgi_lock sync.RWMutex = sync.RWMutex{}
var asgi_request_counter uint64 = 0
var asgi_handlers map[uint64]*AsgiRequestHandler = map[uint64]*AsgiRequestHandler{}

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

	is_websocket := r.Header.Get("connection") == "Upgrade" && r.Header.Get("upgrade") == "websocket" && r.Method == "GET"

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

		key_str := C.CString(k)
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
		asgi_lock.Lock()
		delete(asgi_handlers, request_id)
		asgi_lock.Unlock()
	}()

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
	)
	runtime.UnlockOSThread()

	if err := <-arh.done; err != nil {
		arh.operations <- AsgiOperations{stop: true}
		return err
	}

	return nil
}

//export asgi_receive_start
func asgi_receive_start(request_id C.uint64_t, event *C.AsgiEvent) {
	asgi_lock.Lock()
	arh := asgi_handlers[uint64(request_id)]
	asgi_lock.Unlock()

	arh.operations <- AsgiOperations{stop: false, op: func() {
		body, err := io.ReadAll(arh.r.Body)
		if err != nil {
			arh.done <- err
			return
		}
		body_str := C.CString(string(body))
		defer C.free(unsafe.Pointer(body_str))

		runtime.LockOSThread()
		C.AsgiEvent_set(event, body_str)
		runtime.UnlockOSThread()
	}}
}

//export asgi_set_headers
func asgi_set_headers(request_id C.uint64_t, status_code C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	asgi_lock.Lock()
	arh := asgi_handlers[uint64(request_id)]
	asgi_lock.Unlock()

	arh.operations <- AsgiOperations{stop: false, op: func() {
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
		C.AsgiEvent_set(event, nil)
		runtime.UnlockOSThread()
	}}
}

//export asgi_add_response
func asgi_add_response(request_id C.uint64_t, body *C.char) {
	asgi_lock.Lock()
	arh := asgi_handlers[uint64(request_id)]
	asgi_lock.Unlock()

	arh.operations <- AsgiOperations{stop: false, op: func() {
		body_bytes := []byte(C.GoString(body))
		_, err := arh.w.Write(body_bytes)
		if err != nil {
			arh.done <- err
		}
	}}
}

//export asgi_send_response
func asgi_send_response(request_id C.uint64_t, event *C.AsgiEvent) {
	asgi_lock.Lock()
	arh := asgi_handlers[uint64(request_id)]
	asgi_lock.Unlock()

	arh.operations <- AsgiOperations{stop: true, op: func() {
		arh.done <- nil

		runtime.LockOSThread()
		C.AsgiEvent_set(event, nil)
		runtime.UnlockOSThread()
	}}
}
