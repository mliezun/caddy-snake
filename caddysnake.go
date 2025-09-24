// Caddy plugin to serve Python apps.
package caddysnake

// #cgo pkg-config: python3-embed
// #include "caddysnake.h"
import "C"
import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/spf13/cobra"
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
	Workers    string `json:"workers,omitempty"`
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
				case "workers":
					if !d.Args(&f.Workers) {
						return d.Errf("expected exactly one argument for workers")
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
	var err error
	f.logger = ctx.Logger(f)
	workers, _ := strconv.Atoi(f.Workers)
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if f.ModuleWsgi != "" {
		if workers == 100 {
			initPythonMainThread()
			initWsgi()
			f.app, err = NewWsgi(f.ModuleWsgi, f.WorkingDir, f.VenvPath)
			if err != nil {
				return err
			}
		} else {
			f.app, err = NewPythonWorkerGroup("wsgi", f.ModuleWsgi, f.WorkingDir, f.VenvPath, f.Lifespan, workers)
			if err != nil {
				return err
			}
		}
		if f.Lifespan != "" {
			f.logger.Warn("lifespan is only used in ASGI mode", zap.String("lifespan", f.Lifespan))
		}
		f.logger.Info("imported wsgi app", zap.String("module_wsgi", f.ModuleWsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath))
	} else if f.ModuleAsgi != "" {
		if workers == 100 {
			initPythonMainThread()
			initAsgi()
			f.app, err = NewAsgi(f.ModuleAsgi, f.WorkingDir, f.VenvPath, f.Lifespan == "on", f.logger)
			if err != nil {
				return err
			}
		} else {
			f.app, err = NewPythonWorkerGroup("asgi", f.ModuleAsgi, f.WorkingDir, f.VenvPath, f.Lifespan, workers)
			if err != nil {
				return err
			}
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

type PythonWorker struct {
	Interface  string
	App        string
	WorkingDir string
	Venv       string
	Lifespan   string
	Socket     *os.File

	Cmd       *exec.Cmd
	Transport *http.Transport
	Proxy     *httputil.ReverseProxy
}

func NewPythonWorker(iface, app, workingDir, venv, lifespan string) (*PythonWorker, error) {
	socket, err := os.CreateTemp("", "caddysnake-worker.sock")
	if err != nil {
		return nil, err
	}
	w := &PythonWorker{
		Interface:  iface,
		App:        app,
		WorkingDir: workingDir,
		Venv:       venv,
		Lifespan:   lifespan,
		Socket:     socket,
	}
	err = w.Start()
	return w, err
}

func (w *PythonWorker) Start() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}

	w.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", w.Socket.Name())
		},
	}
	w.Proxy = &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.Out.URL.Scheme = "http"
			req.Out.URL.Host = w.Socket.Name()
		},
		Transport: w.Transport,
	}
	w.Cmd = exec.Command(
		self,
		"python-worker",
		"--interface",
		w.Interface,
		"--app",
		w.App,
		"--working-dir",
		w.WorkingDir,
		"--venv",
		w.Venv,
		"--lifespan",
		w.Lifespan,
		"--socket",
		w.Socket.Name(),
	)
	w.Cmd.Stdout = os.Stdout
	w.Cmd.Stderr = os.Stderr

	return w.Cmd.Start()
}

func (w *PythonWorker) Cleanup() error {
	var err error
	if w.Cmd != nil && w.Cmd.Process != nil {
		w.Cmd.Process.Signal(syscall.SIGTERM)
		_, err = w.Cmd.Process.Wait()
		if err != nil {
			return err
		}
	}
	if w.Socket != nil {
		w.Socket.Close()
		os.Remove(w.Socket.Name())
	}
	return nil
}

func (w *PythonWorker) HandleRequest(rw http.ResponseWriter, req *http.Request) error {
	w.Proxy.ServeHTTP(rw, req)
	return nil
}

func cmdPythonWorker(fs caddycmd.Flags) (int, error) {
	iface := fs.String("interface")
	app := fs.String("app")
	workingDir := fs.String("working-dir")
	venv := fs.String("venv")
	lifespan := fs.String("lifespan")
	socket := fs.String("socket")

	var handler AppServer
	var err error

	initPythonMainThread()

	switch iface {
	case "wsgi":
		initWsgi()
		handler, err = NewWsgi(app, workingDir, venv)
		if err != nil {
			return caddy.ExitCodeFailedStartup, err
		}
	case "asgi":
		initAsgi()
		handler, err = NewAsgi(app, workingDir, venv, lifespan == "on", zap.NewNop())
		if err != nil {
			return caddy.ExitCodeFailedStartup, err
		}
	default:
		return caddy.ExitCodeFailedStartup, errors.New("invalid interface: " + iface)
	}
	defer handler.Cleanup()

	if _, err := os.Stat(socket); err == nil {
		os.Remove(socket)
	}

	// Listen on the Unix domain socket
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}
	defer listener.Close()

	// Define a simple HTTP handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handler.HandleRequest(w, r)
	})

	// Serve HTTP over the Unix socket
	err = http.Serve(listener, nil)
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}

	return 0, nil
}

type PythonWorkerGroup struct {
	Workers    []*PythonWorker
	RoundRobin int
}

func NewPythonWorkerGroup(iface, app, workingDir, venv, lifespan string, count int) (*PythonWorkerGroup, error) {
	errs := make([]error, count)
	workers := make([]*PythonWorker, count)
	for i := 0; i < count; i++ {
		workers[i], errs[i] = NewPythonWorker(iface, app, workingDir, venv, lifespan)
	}
	wg := &PythonWorkerGroup{
		Workers:    workers,
		RoundRobin: 0,
	}
	if err := errors.Join(errs...); err != nil {
		return nil, errors.Join(wg.Cleanup(), err)
	}
	return wg, nil
}

func (wg *PythonWorkerGroup) Cleanup() error {
	errs := make([]error, len(wg.Workers))
	for i, worker := range wg.Workers {
		errs[i] = worker.Cleanup()
	}
	return errors.Join(errs...)
}

func (wg *PythonWorkerGroup) HandleRequest(rw http.ResponseWriter, req *http.Request) error {
	wg.Workers[wg.RoundRobin].HandleRequest(rw, req)
	wg.RoundRobin = (wg.RoundRobin + 1) % len(wg.Workers)
	return nil
}

func init() {
	caddy.RegisterModule(CaddySnake{})
	httpcaddyfile.RegisterHandlerDirective("python", parsePythonDirective)
	caddycmd.RegisterCommand(caddycmd.Command{
		Name:  "python-worker",
		Usage: "[--interface asgi|wsgi] [--app <module>] [--working-dir <dir>] [--venv <dir>] [--lifespan on|off] [--socket <socket>]",
		Short: "Spins up a Python worker",
		Long: `
A Python worker designed for ASGI and WSGI apps.
`,
		CobraFunc: func(cmd *cobra.Command) {
			cmd.Flags().StringP("interface", "i", "", "Interface to use: asgi|wsgi")
			cmd.Flags().StringP("app", "a", "", "App module to be imported")
			cmd.Flags().StringP("working-dir", "w", "", "The working directory")
			cmd.Flags().StringP("venv", "v", "", "The venv directory")
			cmd.Flags().StringP("lifespan", "l", "off", "The lifespan: on|off")
			cmd.Flags().StringP("socket", "s", "", "The socket to bind to")
			cmd.RunE = caddycmd.WrapCommandFuncForCobra(cmdPythonWorker)
		},
	})
}

// findSitePackagesInVenv searches for the site-packages directory in a given venv.
// It returns the absolute path to the site-packages directory if found, or an error otherwise.
func findSitePackagesInVenv(venvPath string) (string, error) {
	var sitePackagesPath string
	if runtime.GOOS == "windows" {
		sitePackagesPath = filepath.Join(venvPath, "Lib\\site-packages")
	} else {
		libPath := filepath.Join(venvPath, "lib")
		pythonDir, err := findPythonDirectory(libPath)
		if err != nil {
			return "", err
		}
		sitePackagesPath = filepath.Join(libPath, pythonDir, "site-packages")
	}
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
