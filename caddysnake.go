// Caddy plugin that gives native support for Python WSGI apps.
package caddysnake

// #cgo pkg-config: python-3.12-embed
// #include "caddysnake.h"
import "C"
import (
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

type CaddySnake struct {
	ModuleName string `json:"module_name,omitempty"`
	logger     *zap.Logger
	wsgi       *Wsgi
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (f *CaddySnake) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		if !d.Args(&f.ModuleName) {
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
func (f *CaddySnake) Provision(ctx caddy.Context) (err error) {
	f.logger = ctx.Logger(f)
	f.wsgi, err = NewWsgi(f.ModuleName)
	return
}

// Validate implements caddy.Validator.
func (m *CaddySnake) Validate() error {
	return nil
}

func (m *CaddySnake) Cleanup() error {
	m.logger.Info("Cleaning up caddy-snake")
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (f CaddySnake) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if err := f.wsgi.HandleRequest(w, r); err != nil {
		return err
	}
	return next.ServeHTTP(w, r)
}

// Interface guards
var (
	_ caddy.Provisioner           = (*CaddySnake)(nil)
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

type RequestHandler struct {
	status_code C.int
	headers     *C.HTTPHeaders
	body        *C.char
}

var lock sync.RWMutex = sync.RWMutex{}
var request_counter int64 = 0
var handlers map[int64]chan RequestHandler = map[int64]chan RequestHandler{}

func init() {
	C.Py_init_and_release_gil()
	caddy.RegisterModule(CaddySnake{})
	httpcaddyfile.RegisterHandlerDirective("python", parsePythonDirective)
}

type Wsgi struct{}

func NewWsgi(wsgi_pattern string) (*Wsgi, error) {
	module_app := strings.Split(wsgi_pattern, ":")
	if len(module_app) != 2 {
		return nil, errors.New("expected pattern $(MODULE_NAME):$(VARIABLE_NAME)")
	}
	module_name := C.CString(module_app[0])
	defer C.free(unsafe.Pointer(module_name))
	app_name := C.CString(module_app[1])
	defer C.free(unsafe.Pointer(app_name))

	result := C.App_import(module_name, app_name)
	if int(result) < 0 {
		return nil, errors.New("failed to import module")
	}

	app := &Wsgi{}
	return app, nil
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

func (m *Wsgi) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	host, port, _ := net.SplitHostPort(r.Host)
	extra_headers := map[string]string{
		"SERVER_NAME":     host,
		"SERVER_PORT":     port,
		"SERVER_PROTOCOL": r.Proto,
		"X_FROM":          "caddy-snake",
		"REQUEST_METHOD":  r.Method,
		"SCRIPT_NAME":     r.URL.Path,
		"PATH_INFO":       r.URL.Path,
		"QUERY_STRING":    r.URL.RawQuery,
		"CONTENT_TYPE":    r.Header.Get("Content-type"),
		"CONTENT_LENGTH":  r.Header.Get("Content-length"),
		"wsgi.url_scheme": strings.ToLower(strings.Split(r.Proto, "/")[0]),
	}
	rh := C.HTTPHeaders_new(C.size_t(len(r.Header) + len(extra_headers)))
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

	ch := make(chan RequestHandler)
	lock.Lock()
	request_counter++
	request_id := request_counter
	handlers[request_id] = ch
	lock.Unlock()

	runtime.LockOSThread()
	C.App_handle_request(C.int64_t(request_id), rh, body_str)
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

//export go_callback
func go_callback(request_id C.int64_t, status_code C.int, headers *C.HTTPHeaders, body *C.char) {
	lock.Lock()
	defer lock.Unlock()
	ch := handlers[int64(request_id)]
	ch <- RequestHandler{
		status_code: status_code,
		body:        body,
		headers:     headers,
	}
	delete(handlers, int64(request_id))
}
