// Caddy plugin to serve Python apps.
package caddysnake

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/certmagic"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp/encode"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/encode/gzip"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/encode/zstd"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
)

//go:embed caddysnake.py
var caddysnake_py string

// AppServer defines the interface to interacting with a WSGI or ASGI server
type AppServer interface {
	Cleanup() error
	HandleRequest(w http.ResponseWriter, r *http.Request) error
}

// CaddySnake module that communicates with a Python app
type CaddySnake struct {
	ModuleWsgi     string `json:"module_wsgi,omitempty"`
	ModuleAsgi     string `json:"module_asgi,omitempty"`
	Lifespan       string `json:"lifespan,omitempty"`
	WorkingDir     string `json:"working_dir,omitempty"`
	VenvPath       string `json:"venv_path,omitempty"`
	Workers        string `json:"workers,omitempty"`
	WorkersRuntime string `json:"workers_runtime,omitempty"`
	Autoreload     string `json:"autoreload,omitempty"`
	PythonPath     string `json:"python_path,omitempty"`
	logger         *zap.Logger
	app            AppServer
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
				case "workers_runtime":
					if !d.Args(&f.WorkersRuntime) || (f.WorkersRuntime != "thread" && f.WorkersRuntime != "process") {
						return d.Errf("expected exactly one argument for workers_runtime: thread|process")
					}
				case "autoreload":
					f.Autoreload = "on"
				case "python_path":
					if !d.Args(&f.PythonPath) {
						return d.Errf("expected exactly one argument for python_path")
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

	isDynamic := containsPlaceholder(f.ModuleWsgi) || containsPlaceholder(f.ModuleAsgi) ||
		containsPlaceholder(f.WorkingDir) || containsPlaceholder(f.VenvPath)

	if isDynamic {
		return f.provisionDynamic(workers)
	}

	pythonBin := resolvePythonInterpreter(f.PythonPath, f.VenvPath)

	if f.ModuleWsgi != "" {
		f.app, err = NewPythonWorkerGroup("wsgi", f.ModuleWsgi, f.WorkingDir, f.VenvPath, f.Lifespan, workers, pythonBin)
		if err != nil {
			return err
		}
		if f.Lifespan != "" {
			f.logger.Warn("lifespan attribute is ignored in WSGI mode", zap.String("lifespan", f.Lifespan))
		}
		f.logger.Info("serving wsgi app", zap.String("module_wsgi", f.ModuleWsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath), zap.String("python", pythonBin))
	} else if f.ModuleAsgi != "" {
		f.app, err = NewPythonWorkerGroup("asgi", f.ModuleAsgi, f.WorkingDir, f.VenvPath, f.Lifespan, workers, pythonBin)
		if err != nil {
			return err
		}
		f.logger.Info("serving asgi app", zap.String("module_asgi", f.ModuleAsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath), zap.String("python", pythonBin))
	} else {
		return errors.New("asgi or wsgi app needs to be specified")
	}

	if f.Autoreload == "on" {
		watchDir := f.WorkingDir
		if watchDir == "" {
			watchDir = "."
		}
		absDir, absErr := filepath.Abs(watchDir)
		if absErr != nil {
			return fmt.Errorf("autoreload: %w", absErr)
		}

		var factory func() (AppServer, error)
		if f.ModuleWsgi != "" {
			factory = func() (AppServer, error) {
				return NewPythonWorkerGroup("wsgi", f.ModuleWsgi, f.WorkingDir, f.VenvPath, f.Lifespan, workers, pythonBin)
			}
		} else {
			factory = func() (AppServer, error) {
				return NewPythonWorkerGroup("asgi", f.ModuleAsgi, f.WorkingDir, f.VenvPath, f.Lifespan, workers, pythonBin)
			}
		}

		f.app, err = NewAutoreloadableApp(f.app, absDir, factory, f.logger)
		if err != nil {
			return fmt.Errorf("autoreload: %w", err)
		}
	}

	return nil
}

// provisionDynamic sets up the module in dynamic mode where Caddy placeholders
// in module_wsgi/module_asgi, working_dir, or venv are resolved per-request.
func (f *CaddySnake) provisionDynamic(workers int) error {
	autoreload := f.Autoreload == "on"
	pythonPath := f.PythonPath

	if f.ModuleWsgi != "" {
		lifespan := f.Lifespan
		factory := func(module, dir, venv string) (AppServer, error) {
			pythonBin := resolvePythonInterpreter(pythonPath, venv)
			return NewPythonWorkerGroup("wsgi", module, dir, venv, lifespan, workers, pythonBin)
		}
		var err error
		f.app, err = NewDynamicApp(f.ModuleWsgi, f.WorkingDir, f.VenvPath, factory, f.logger, autoreload)
		if err != nil {
			return err
		}
		if f.Lifespan != "" {
			f.logger.Warn("lifespan attribute is ignored in WSGI mode", zap.String("lifespan", f.Lifespan))
		}
		f.logger.Info("serving dynamic wsgi app",
			zap.String("module_wsgi", f.ModuleWsgi),
			zap.String("working_dir", f.WorkingDir),
			zap.String("venv_path", f.VenvPath),
		)
	} else if f.ModuleAsgi != "" {
		lifespan := f.Lifespan
		factory := func(module, dir, venv string) (AppServer, error) {
			pythonBin := resolvePythonInterpreter(pythonPath, venv)
			return NewPythonWorkerGroup("asgi", module, dir, venv, lifespan, workers, pythonBin)
		}
		var err error
		f.app, err = NewDynamicApp(f.ModuleAsgi, f.WorkingDir, f.VenvPath, factory, f.logger, autoreload)
		if err != nil {
			return err
		}
		f.logger.Info("serving dynamic asgi app",
			zap.String("module_asgi", f.ModuleAsgi),
			zap.String("working_dir", f.WorkingDir),
			zap.String("venv_path", f.VenvPath),
		)
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

// resolvePythonInterpreter determines the Python interpreter to use.
// Priority: explicit python_path > venv/bin/python > system python3.
func resolvePythonInterpreter(pythonPath, venvPath string) string {
	if pythonPath != "" {
		return pythonPath
	}

	if venvPath != "" {
		var binDir string
		if runtime.GOOS == "windows" {
			binDir = "Scripts"
		} else {
			binDir = "bin"
		}
		venvPython := filepath.Join(venvPath, binDir, "python3")
		if runtime.GOOS == "windows" {
			venvPython = filepath.Join(venvPath, binDir, "python.exe")
		}
		if _, err := os.Stat(venvPython); err == nil {
			return venvPython
		}
		venvPython2 := filepath.Join(venvPath, binDir, "python")
		if _, err := os.Stat(venvPython2); err == nil {
			return venvPython2
		}
	}

	return "python3"
}

// writeCaddysnakePy writes the embedded caddysnake.py to a temp file and returns the path.
func writeCaddysnakePy() (string, error) {
	f, err := os.CreateTemp("", "caddysnake-*.py")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(caddysnake_py); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

type PythonWorker struct {
	Interface  string
	App        string
	WorkingDir string
	Venv       string
	Lifespan   string
	PythonBin  string
	Socket     *os.File
	ScriptPath string
	DialNet    string // "unix" or "tcp"
	DialAddr   string // socket path or host:port

	Cmd       *exec.Cmd
	Transport *http.Transport
	Proxy     *httputil.ReverseProxy
}

func NewPythonWorker(iface, app, workingDir, venv, lifespan, pythonBin, scriptPath string) (*PythonWorker, error) {
	var socket *os.File
	var err error
	if runtime.GOOS == "windows" {
		socket, err = os.CreateTemp("", "caddysnake-worker.port*")
	} else {
		socket, err = os.CreateTemp("", "caddysnake-worker.sock")
	}
	if err != nil {
		return nil, err
	}
	path := socket.Name()
	socket.Close() // close so Python can write (Windows) or replace with socket (Unix)

	dialNet := "unix"
	dialAddr := path
	if runtime.GOOS == "windows" {
		dialNet = "tcp"
		dialAddr = "" // set after Python writes port to path
	}

	w := &PythonWorker{
		Interface:  iface,
		App:        app,
		WorkingDir: workingDir,
		Venv:       venv,
		Lifespan:   lifespan,
		PythonBin:  pythonBin,
		Socket:     socket,
		ScriptPath: scriptPath,
		DialNet:    dialNet,
		DialAddr:   dialAddr,
	}
	err = w.Start()
	return w, err
}

func (w *PythonWorker) Start() error {
	w.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return w.dialWithRetry(ctx)
		},
	}
	w.Proxy = &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.Out.URL.Scheme = "http"
			req.Out.URL.Host = w.DialAddr
		},
		Transport: w.Transport,
	}

	workingDir := w.WorkingDir
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}

	args := []string{
		w.ScriptPath,
		"--interface", w.Interface,
		"--app", w.App,
		"--socket", w.Socket.Name(),
	}
	if workingDir != "" {
		args = append(args, "--working-dir", workingDir)
	}
	if w.Venv != "" {
		args = append(args, "--venv", w.Venv)
	}
	if w.Lifespan != "" {
		args = append(args, "--lifespan", w.Lifespan)
	}

	w.Cmd = exec.Command(w.PythonBin, args...)
	w.Cmd.Stdout = os.Stdout
	w.Cmd.Stderr = os.Stderr
	w.Cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	setSysProcAttr(w.Cmd)

	if err := w.Cmd.Start(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		port, err := waitForPortFile(w.Socket.Name(), 10*time.Second)
		if err != nil {
			w.Cmd.Process.Kill()
			_ = w.Cmd.Wait()
			return fmt.Errorf("waiting for Python worker port file: %w", err)
		}
		w.DialAddr = "127.0.0.1:" + strconv.Itoa(port)
	}
	return nil
}

// waitForPortFile polls the given file path until it contains a valid port number.
func waitForPortFile(path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			port, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && port > 0 && port < 65536 {
				return port, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0, fmt.Errorf("port file %s not ready within %v", path, timeout)
}

// dialWithRetry attempts to establish a connection with retry logic
func (w *PythonWorker) dialWithRetry(ctx context.Context) (net.Conn, error) {
	const maxRetries = 5
	const baseDelay = 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		conn, err := net.Dial(w.DialNet, w.DialAddr)
		if err == nil {
			return conn, nil
		}

		if attempt == maxRetries-1 {
			return nil, fmt.Errorf("failed to connect after %d attempts: %w", maxRetries, err)
		}

		delay := baseDelay * time.Duration(1<<attempt)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, fmt.Errorf("unexpected error in dialWithRetry")
}

func (w *PythonWorker) Cleanup() error {
	if w.Transport != nil {
		w.Transport.CloseIdleConnections()
	}
	if w.Cmd != nil && w.Cmd.Process != nil {
		w.Cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() {
			_, err := w.Cmd.Process.Wait()
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				return err
			}
		case <-time.After(5 * time.Second):
			w.Cmd.Process.Kill()
			<-done
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

type PythonWorkerGroup struct {
	Workers    []*PythonWorker
	RoundRobin int
	ScriptPath string
}

func NewPythonWorkerGroup(iface, app, workingDir, venv, lifespan string, count int, pythonBin string) (*PythonWorkerGroup, error) {
	scriptPath, err := writeCaddysnakePy()
	if err != nil {
		return nil, fmt.Errorf("failed to write caddysnake.py: %w", err)
	}

	errs := make([]error, count)
	workers := make([]*PythonWorker, count)
	for i := 0; i < count; i++ {
		workers[i], errs[i] = NewPythonWorker(iface, app, workingDir, venv, lifespan, pythonBin, scriptPath)
	}
	wg := &PythonWorkerGroup{
		Workers:    workers,
		RoundRobin: 0,
		ScriptPath: scriptPath,
	}
	if err := errors.Join(errs...); err != nil {
		return nil, errors.Join(wg.Cleanup(), err)
	}
	return wg, nil
}

func (wg *PythonWorkerGroup) Cleanup() error {
	errs := make([]error, len(wg.Workers))
	for i, worker := range wg.Workers {
		if worker != nil {
			errs[i] = worker.Cleanup()
		}
	}
	if wg.ScriptPath != "" {
		os.Remove(wg.ScriptPath)
	}
	return errors.Join(errs...)
}

func (wg *PythonWorkerGroup) HandleRequest(rw http.ResponseWriter, req *http.Request) error {
	wg.RoundRobin = (wg.RoundRobin + 1) % len(wg.Workers)
	wg.Workers[wg.RoundRobin].HandleRequest(rw, req)
	return nil
}

func init() {
	caddy.RegisterModule(CaddySnake{})
	httpcaddyfile.RegisterHandlerDirective("python", parsePythonDirective)
	caddycmd.RegisterCommand(caddycmd.Command{
		Name:  "python-server",
		Usage: "--server-type wsgi|asgi --app <module> [--domain <example.com>] [--listen <addr>] [--workers <count>] [--python-path <path>] [--static-path <path>] [--static-route <route>] [--debug] [--access-logs] [--autoreload]",
		Short: "Spins up a Python server",
		Long: `
A Python WSGI or ASGI server designed for apps and frameworks.

You can specify a custom socket address using the '--listen' option. You can also specify the number of workers to spawn.

Providing a domain name with the '--domain' flag enables HTTPS and sets the listener to the appropriate secure port.
Ensure DNS A/AAAA records are correctly set up if using a public domain for secure connections.
`,
		CobraFunc: func(cmd *cobra.Command) {
			cmd.Flags().StringP("server-type", "t", "", "Required. The type of server to use: wsgi|asgi")
			cmd.Flags().StringP("app", "a", "", "Required. App module to be imported")
			cmd.Flags().StringP("domain", "d", "", "Domain name at which to serve the files")
			cmd.Flags().StringP("listen", "l", "", "The address to which to bind the listener")
			cmd.Flags().StringP("workers", "w", "0", "The number of workers to spawn")
			cmd.Flags().String("python-path", "", "Path to the Python interpreter")
			cmd.Flags().String("static-path", "", "Path to a static directory to serve: path/to/static")
			cmd.Flags().String("static-route", "/static", "Route to serve the static directory: /static")
			cmd.Flags().Bool("debug", false, "Enable debug logs")
			cmd.Flags().Bool("access-logs", false, "Enable access logs")
			cmd.Flags().Bool("autoreload", false, "Watch .py files and reload on changes")
			cmd.RunE = caddycmd.WrapCommandFuncForCobra(pythonServer)
		},
	})
}

// pythonServer is inspired on the php-server command of the Frankenphp project (MIT License)
func pythonServer(fs caddycmd.Flags) (int, error) {
	caddy.TrapSignals()

	domain := fs.String("domain")
	app := fs.String("app")
	listen := fs.String("listen")
	workers := fs.String("workers")
	debug := fs.Bool("debug")
	accessLogs := fs.Bool("access-logs")
	autoreload := fs.Bool("autoreload")
	staticPath := fs.String("static-path")
	staticRoute := fs.String("static-route")
	serverType := fs.String("server-type")
	pythonPath := fs.String("python-path")

	if serverType == "" {
		return caddy.ExitCodeFailedStartup, errors.New("--server-type is required")
	}
	if app == "" {
		return caddy.ExitCodeFailedStartup, errors.New("--app is required")
	}

	gzip, err := caddy.GetModule("http.encoders.gzip")
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}

	zstd, err := caddy.GetModule("http.encoders.zstd")
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}

	encodings := caddy.ModuleMap{
		"zstd": caddyconfig.JSON(zstd.New(), nil),
		"gzip": caddyconfig.JSON(gzip.New(), nil),
	}
	prefer := []string{"zstd", "gzip"}

	pythonHandler := CaddySnake{}
	if serverType == "wsgi" {
		pythonHandler.ModuleWsgi = app
	} else {
		pythonHandler.ModuleAsgi = app
	}
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		pythonHandler.VenvPath = venv
	}

	pythonHandler.Workers = workers
	pythonHandler.PythonPath = pythonPath
	if autoreload {
		pythonHandler.Autoreload = "on"
	}

	routes := caddyhttp.RouteList{}

	if staticPath != "" {
		if strings.HasSuffix(staticRoute, "/") {
			staticRoute = staticRoute + "*"
		} else if !strings.HasSuffix(staticRoute, "/*") {
			staticRoute = staticRoute + "/*"
		}
		staticRoute := caddyhttp.Route{
			MatcherSetsRaw: []caddy.ModuleMap{
				{
					"path": caddyconfig.JSON(caddyhttp.MatchPath{staticRoute}, nil),
				},
			},
			HandlersRaw: []json.RawMessage{
				caddyconfig.JSONModuleObject(encode.Encode{
					EncodingsRaw: encodings,
					Prefer:       prefer,
				}, "handler", "encode", nil),
				caddyconfig.JSON(map[string]interface{}{
					"handler": "file_server",
					"root":    staticPath,
				}, nil),
			},
		}
		routes = append(routes, staticRoute)
	}

	mainRoute := caddyhttp.Route{
		MatcherSetsRaw: []caddy.ModuleMap{
			{
				"path": caddyconfig.JSON(caddyhttp.MatchPath{"/*"}, nil),
			},
		},
		HandlersRaw: []json.RawMessage{
			caddyconfig.JSONModuleObject(encode.Encode{
				EncodingsRaw: encodings,
				Prefer:       prefer,
			}, "handler", "encode", nil),
			caddyconfig.JSONModuleObject(pythonHandler, "handler", "python", nil),
		},
	}
	routes = append(routes, mainRoute)

	subroute := caddyhttp.Subroute{
		Routes: routes,
	}

	route := caddyhttp.Route{
		HandlersRaw: []json.RawMessage{caddyconfig.JSONModuleObject(subroute, "handler", "subroute", nil)},
	}

	if domain != "" {
		route.MatcherSetsRaw = []caddy.ModuleMap{
			{
				"host": caddyconfig.JSON(caddyhttp.MatchHost{domain}, nil),
			},
		}
	}

	server := &caddyhttp.Server{
		ReadHeaderTimeout: caddy.Duration(10 * time.Second),
		IdleTimeout:       caddy.Duration(30 * time.Second),
		MaxHeaderBytes:    1024 * 10,
		Routes:            caddyhttp.RouteList{route},
	}
	if listen == "" {
		if domain == "" {
			listen = ":9080"
		} else {
			listen = ":" + strconv.Itoa(certmagic.HTTPSPort)
		}
	}
	server.Listen = []string{listen}

	if accessLogs {
		server.Logs = &caddyhttp.ServerLogConfig{}
	}

	httpApp := caddyhttp.App{
		Servers: map[string]*caddyhttp.Server{"srv0": server},
	}

	var f bool
	cfg := &caddy.Config{
		Admin: &caddy.AdminConfig{
			Disabled: false,
			Config: &caddy.ConfigSettings{
				Persist: &f,
			},
		},
		AppsRaw: caddy.ModuleMap{
			"http": caddyconfig.JSON(httpApp, nil),
		},
	}

	if debug {
		cfg.Logging = &caddy.Logging{
			Logs: map[string]*caddy.CustomLog{
				"default": {
					BaseLog: caddy.BaseLog{Level: zapcore.DebugLevel.CapitalString()},
				},
			},
		}
	}

	if err := caddy.Run(cfg); err != nil {
		return caddy.ExitCodeFailedStartup, err
	}

	log.Printf("Serving Python app on %s", listen)

	select {}
}

// findSitePackagesInVenv searches for the site-packages directory in a given venv.
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
			return errors.New("python directory found")
		}
		return nil
	})
	if !found || pythonDir == "" {
		return "", errors.New("unable to find a python3.* directory in the venv")
	}
	return pythonDir, nil
}
