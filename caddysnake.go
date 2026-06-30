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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// Hop-internal headers: set only by the Go module after stripping client-supplied
// values. The Python worker uses these for REMOTE_ADDR / ASGI client.
const (
	caddySnakeRemoteAddrHeader = "Caddy-Snake-Remote-Addr"
	caddySnakeRemotePortHeader = "Caddy-Snake-Remote-Port"
)

// setPythonWorkerOutboundHeaders configures the outbound request to the Python
// worker: target URL, standard X-Forwarded-* (preserve inbound X-Forwarded-For
// chain, then append this hop per [httputil.ProxyRequest.SetXForwarded]), and
// trusted Caddy-Snake-Remote-* from the inbound RemoteAddr.
func setPythonWorkerOutboundHeaders(pr *httputil.ProxyRequest, dialAddr string) {
	pr.Out.URL.Scheme = "http"
	pr.Out.URL.Host = dialAddr
	pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
	pr.SetXForwarded()
	pr.Out.Header.Del(caddySnakeRemoteAddrHeader)
	pr.Out.Header.Del(caddySnakeRemotePortHeader)
	host, port, err := net.SplitHostPort(pr.In.RemoteAddr)
	if err != nil {
		host = pr.In.RemoteAddr
		port = "0"
	}
	pr.Out.Header.Set(caddySnakeRemoteAddrHeader, host)
	pr.Out.Header.Set(caddySnakeRemotePortHeader, port)
}

// AppServer defines the interface to interacting with a WSGI or ASGI server
type AppServer interface {
	Cleanup() error
	HandleRequest(w http.ResponseWriter, r *http.Request) error
}

// CaddySnake module that communicates with a Python app
type CaddySnake struct {
	ModuleWsgi string            `json:"module_wsgi,omitempty"`
	ModuleAsgi string            `json:"module_asgi,omitempty"`
	ModuleEsgi string            `json:"module_esgi,omitempty"`
	Runtime    string            `json:"runtime,omitempty"`
	Lifespan   string            `json:"lifespan,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	VenvPath   string            `json:"venv_path,omitempty"`
	Workers    string            `json:"workers,omitempty"`
	Autoreload string            `json:"autoreload,omitempty"`
	PythonPath string            `json:"python_path,omitempty"`
	EnvFiles   []string          `json:"env_files,omitempty"`
	EnvVars    map[string]string `json:"env_vars,omitempty"`
	logger     *zap.Logger
	app        AppServer
	cacheSrv   *cacheServer
}

// effectivePythonRuntime returns the runtime string passed to the Python worker.
// When Runtime is empty: sync for WSGI, gevent for ESGI, uvloop for ASGI.
func effectivePythonRuntime(iface, runtime string) string {
	if iface == "asgi" && runtime == "libuv" {
		runtime = "uvloop" // legacy alias; configuration name is uvloop
	}
	if runtime != "" {
		return runtime
	}
	if iface == "asgi" {
		return "uvloop"
	}
	if iface == "esgi" {
		return "gevent"
	}
	return "sync"
}

func validatePythonRuntime(iface, runtime string) error {
	eff := effectivePythonRuntime(iface, runtime)
	switch iface {
	case "wsgi":
		if eff != "sync" && eff != "gevent" {
			return fmt.Errorf("wsgi runtime must be sync or gevent, got %q", eff)
		}
	case "esgi":
		if eff != "gevent" {
			return fmt.Errorf("esgi runtime must be gevent, got %q", eff)
		}
	case "asgi":
		if eff != "native" && eff != "uvloop" {
			return fmt.Errorf("asgi runtime must be native or uvloop, got %q", eff)
		}
	default:
		return fmt.Errorf("unknown python interface %q", iface)
	}
	return nil
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
				case "module_esgi":
					if !d.Args(&f.ModuleEsgi) {
						return d.Errf("expected exactly one argument for module_esgi")
					}
				case "module_wsgi":
					if !d.Args(&f.ModuleWsgi) {
						return d.Errf("expected exactly one argument for module_wsgi")
					}
				case "runtime":
					if !d.Args(&f.Runtime) {
						return d.Errf("expected exactly one argument for runtime")
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
				case "autoreload":
					f.Autoreload = "on"
				case "python_path":
					if !d.Args(&f.PythonPath) {
						return d.Errf("expected exactly one argument for python_path")
					}
				case "env_file":
					var path string
					if !d.Args(&path) {
						return d.Errf("expected exactly one argument for env_file")
					}
					f.EnvFiles = append(f.EnvFiles, path)
				case "env_var":
					var name, value string
					if !d.Args(&name, &value) {
						return d.Errf("expected exactly two arguments for env_var: VARNAME value")
					}
					if err := validateEnvVarName(name); err != nil {
						return d.Errf("invalid env_var name %q: %v", name, err)
					}
					if f.EnvVars == nil {
						f.EnvVars = make(map[string]string)
					}
					f.EnvVars[name] = value
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

	cs, err := startCacheServer()
	if err != nil {
		return fmt.Errorf("in-process cache: %w", err)
	}
	f.cacheSrv = cs
	cacheAddr := cs.Addr()
	success := false
	defer func() {
		if !success && f.cacheSrv != nil {
			_ = f.cacheSrv.Close()
			f.cacheSrv = nil
		}
	}()

	workers, _ := strconv.Atoi(f.Workers)
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	isDynamic := containsPlaceholder(f.ModuleWsgi) || containsPlaceholder(f.ModuleAsgi) ||
		containsPlaceholder(f.ModuleEsgi) ||
		containsPlaceholder(f.WorkingDir) || containsPlaceholder(f.VenvPath) ||
		envFilesContainPlaceholder(f.EnvFiles) || envMapContainsPlaceholder(f.EnvVars)

	if isDynamic {
		if err = f.provisionDynamic(workers, cacheAddr); err != nil {
			return err
		}
		success = true
		return nil
	}

	pythonBin := resolvePythonInterpreter(f.PythonPath, f.VenvPath)

	envFiles := cloneEnvFiles(f.EnvFiles)
	envVars := cloneEnvVars(f.EnvVars)

	if f.ModuleWsgi != "" {
		rt := effectivePythonRuntime("wsgi", f.Runtime)
		f.app, err = NewPythonWorkerGroup("wsgi", f.ModuleWsgi, f.WorkingDir, f.VenvPath, f.Lifespan, rt, workers, pythonBin, cacheAddr, envFiles, envVars)
		if err != nil {
			return err
		}
		if f.Lifespan != "" {
			f.logger.Warn("lifespan attribute is ignored in WSGI mode", zap.String("lifespan", f.Lifespan))
		}
		f.logger.Info("serving wsgi app", zap.String("module_wsgi", f.ModuleWsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath), zap.String("python", pythonBin), zap.String("runtime", rt))
	} else if f.ModuleAsgi != "" {
		rt := effectivePythonRuntime("asgi", f.Runtime)
		f.app, err = NewPythonWorkerGroup("asgi", f.ModuleAsgi, f.WorkingDir, f.VenvPath, f.Lifespan, rt, workers, pythonBin, cacheAddr, envFiles, envVars)
		if err != nil {
			return err
		}
		f.logger.Info("serving asgi app", zap.String("module_asgi", f.ModuleAsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath), zap.String("python", pythonBin), zap.String("runtime", rt))
	} else if f.ModuleEsgi != "" {
		rt := effectivePythonRuntime("esgi", f.Runtime)
		f.app, err = NewPythonWorkerGroup("esgi", f.ModuleEsgi, f.WorkingDir, f.VenvPath, "", rt, workers, pythonBin, cacheAddr, envFiles, envVars)
		if err != nil {
			return err
		}
		if f.Lifespan != "" {
			f.logger.Warn("lifespan is for ASGI only; ignored in ESGI mode", zap.String("lifespan", f.Lifespan))
		}
		f.logger.Info("serving esgi app", zap.String("module_esgi", f.ModuleEsgi), zap.String("working_dir", f.WorkingDir), zap.String("venv_path", f.VenvPath), zap.String("python", pythonBin), zap.String("runtime", rt))
	} else {
		return errors.New("a wsgi, asgi, or esgi app must be specified")
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
			rt := effectivePythonRuntime("wsgi", f.Runtime)
			factory = func() (AppServer, error) {
				return NewPythonWorkerGroup("wsgi", f.ModuleWsgi, f.WorkingDir, f.VenvPath, f.Lifespan, rt, workers, pythonBin, cacheAddr, envFiles, envVars)
			}
		} else if f.ModuleAsgi != "" {
			rt := effectivePythonRuntime("asgi", f.Runtime)
			factory = func() (AppServer, error) {
				return NewPythonWorkerGroup("asgi", f.ModuleAsgi, f.WorkingDir, f.VenvPath, f.Lifespan, rt, workers, pythonBin, cacheAddr, envFiles, envVars)
			}
		} else {
			rt := effectivePythonRuntime("esgi", f.Runtime)
			factory = func() (AppServer, error) {
				return NewPythonWorkerGroup("esgi", f.ModuleEsgi, f.WorkingDir, f.VenvPath, "", rt, workers, pythonBin, cacheAddr, envFiles, envVars)
			}
		}

		// Keep Caddy running on reload errors; failed app serves 503 until recovery.
		f.app, err = NewAutoreloadableApp(f.app, absDir, factory, f.logger, nil)
		if err != nil {
			return fmt.Errorf("autoreload: %w", err)
		}
	}

	success = true
	return nil
}

// provisionDynamic sets up the module in dynamic mode where Caddy placeholders
// in module_wsgi/module_asgi/module_esgi, working_dir, or venv are resolved per-request.
func (f *CaddySnake) provisionDynamic(workers int, cacheAddr string) error {
	autoreload := f.Autoreload == "on"
	pythonPath := f.PythonPath
	envFilePatterns := cloneEnvFiles(f.EnvFiles)
	envVarPatterns := cloneEnvVars(f.EnvVars)

	if f.ModuleWsgi != "" {
		lifespan := f.Lifespan
		rt := effectivePythonRuntime("wsgi", f.Runtime)
		factory := func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			pythonBin := resolvePythonInterpreter(pythonPath, venv)
			return NewPythonWorkerGroup("wsgi", module, dir, venv, lifespan, rt, workers, pythonBin, cacheAddr, envFiles, envVars)
		}
		var err error
		f.app, err = NewDynamicApp(f.ModuleWsgi, f.WorkingDir, f.VenvPath, envFilePatterns, envVarPatterns, factory, f.logger, autoreload, nil)
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
		rt := effectivePythonRuntime("asgi", f.Runtime)
		factory := func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			pythonBin := resolvePythonInterpreter(pythonPath, venv)
			return NewPythonWorkerGroup("asgi", module, dir, venv, lifespan, rt, workers, pythonBin, cacheAddr, envFiles, envVars)
		}
		var err error
		f.app, err = NewDynamicApp(f.ModuleAsgi, f.WorkingDir, f.VenvPath, envFilePatterns, envVarPatterns, factory, f.logger, autoreload, nil)
		if err != nil {
			return err
		}
		f.logger.Info("serving dynamic asgi app",
			zap.String("module_asgi", f.ModuleAsgi),
			zap.String("working_dir", f.WorkingDir),
			zap.String("venv_path", f.VenvPath),
		)
	} else if f.ModuleEsgi != "" {
		rt := effectivePythonRuntime("esgi", f.Runtime)
		factory := func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			pythonBin := resolvePythonInterpreter(pythonPath, venv)
			return NewPythonWorkerGroup("esgi", module, dir, venv, "", rt, workers, pythonBin, cacheAddr, envFiles, envVars)
		}
		var err error
		f.app, err = NewDynamicApp(f.ModuleEsgi, f.WorkingDir, f.VenvPath, envFilePatterns, envVarPatterns, factory, f.logger, autoreload, nil)
		if err != nil {
			return err
		}
		if f.Lifespan != "" {
			f.logger.Warn("lifespan is for ASGI only; ignored in dynamic ESGI mode", zap.String("lifespan", f.Lifespan))
		}
		f.logger.Info("serving dynamic esgi app",
			zap.String("module_esgi", f.ModuleEsgi),
			zap.String("working_dir", f.WorkingDir),
			zap.String("venv_path", f.VenvPath),
		)
	} else {
		return errors.New("a wsgi, asgi, or esgi app must be specified for dynamic mode")
	}
	return nil
}

// Validate implements caddy.Validator.
func (m *CaddySnake) Validate() error {
	n := 0
	if m.ModuleWsgi != "" {
		n++
	}
	if m.ModuleAsgi != "" {
		n++
	}
	if m.ModuleEsgi != "" {
		n++
	}
	if n != 1 {
		return errors.New("exactly one of module_wsgi, module_asgi, or module_esgi is required")
	}
	var iface string
	switch {
	case m.ModuleWsgi != "":
		iface = "wsgi"
	case m.ModuleAsgi != "":
		iface = "asgi"
	default:
		iface = "esgi"
	}
	if err := validatePythonRuntime(iface, m.Runtime); err != nil {
		return err
	}
	if m.Workers != "" {
		w, err := strconv.Atoi(m.Workers)
		if err != nil || w < 0 {
			return fmt.Errorf("invalid workers value: %s", m.Workers)
		}
	}
	if m.Lifespan != "" && m.Lifespan != "on" && m.Lifespan != "off" {
		return fmt.Errorf("lifespan must be 'on' or 'off', got: %s", m.Lifespan)
	}
	if err := validateEnvVars(m.EnvVars); err != nil {
		return err
	}
	return nil
}

// Cleanup frees resources uses by module
func (m *CaddySnake) Cleanup() error {
	var err error
	if m != nil && m.app != nil {
		m.logger.Info("cleaning up module")
		err = m.app.Cleanup()
	}
	if m != nil && m.cacheSrv != nil {
		if cerr := m.cacheSrv.Close(); cerr != nil && err == nil {
			err = cerr
		}
		m.cacheSrv = nil
	}
	return err
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (f CaddySnake) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if err := f.app.HandleRequest(w, r); err != nil {
		return err
	}
	return nil
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

// writeCaddysnakePyBundle writes the embedded worker implementation as worker_main.py
// inside a private temp directory so user apps can import the PyPI `caddysnake` package.
func writeCaddysnakePyBundle() (scriptPath, bundleDir string, err error) {
	bundleDir, err = os.MkdirTemp("", "caddysnake-worker-*")
	if err != nil {
		return "", "", err
	}
	if runtime.GOOS != "windows" {
		if chErr := os.Chmod(bundleDir, 0700); chErr != nil {
			os.RemoveAll(bundleDir)
			return "", "", chErr
		}
	}
	scriptPath = filepath.Join(bundleDir, "worker_main.py")
	if wrErr := os.WriteFile(scriptPath, []byte(caddysnake_py), 0600); wrErr != nil {
		os.RemoveAll(bundleDir)
		return "", "", wrErr
	}
	return scriptPath, bundleDir, nil
}

// proxyBufferPool implements httputil.BufferPool using sync.Pool to reduce GC pressure.
type proxyBufferPool struct {
	pool sync.Pool
}

func (p *proxyBufferPool) Get() []byte {
	b := p.pool.Get()
	if b == nil {
		return make([]byte, 32*1024)
	}
	return *b.(*[]byte)
}

func (p *proxyBufferPool) Put(b []byte) {
	p.pool.Put(&b)
}

var sharedProxyBufferPool = &proxyBufferPool{}

type PythonWorker struct {
	Interface  string
	App        string
	WorkingDir string
	Venv       string
	Lifespan   string
	Runtime    string
	PythonBin  string
	Socket     *os.File
	SockDir    string // private directory containing the socket (Unix only)
	ScriptPath string
	DialNet    string // "unix" or "tcp"
	DialAddr   string // socket path or host:port
	CacheAddr  string // CADDYSNAKE_CACHE_ADDR: unix://path (Unix) or 127.0.0.1:port (Windows); empty = omit env
	EnvFiles   []string
	EnvVars    map[string]string

	Cmd       *exec.Cmd
	Transport *http.Transport
	Proxy     *httputil.ReverseProxy
}

func NewPythonWorker(iface, app, workingDir, venv, lifespan, pyRuntime, pythonBin, scriptPath, cacheAddr string, envFiles []string, envVars map[string]string) (*PythonWorker, error) {
	var socket *os.File
	var sockDir string
	var err error
	if runtime.GOOS == "windows" {
		socket, err = os.CreateTemp("", "caddysnake-worker.port*")
	} else {
		sockDir, err = os.MkdirTemp("", "caddysnake-*")
		if err != nil {
			return nil, err
		}
		if chErr := os.Chmod(sockDir, 0700); chErr != nil {
			os.RemoveAll(sockDir)
			return nil, chErr
		}
		socket, err = os.Create(filepath.Join(sockDir, "worker.sock"))
	}
	if err != nil {
		if sockDir != "" {
			os.RemoveAll(sockDir)
		}
		return nil, err
	}
	path := socket.Name()
	socket.Close()

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
		Runtime:    pyRuntime,
		PythonBin:  pythonBin,
		Socket:     socket,
		SockDir:    sockDir,
		ScriptPath: scriptPath,
		DialNet:    dialNet,
		DialAddr:   dialAddr,
		CacheAddr:  cacheAddr,
		EnvFiles:   cloneEnvFiles(envFiles),
		EnvVars:    cloneEnvVars(envVars),
	}
	err = w.Start()
	return w, err
}

func (w *PythonWorker) Start() error {
	w.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return w.dialWithRetry(ctx)
		},
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	w.Proxy = &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			setPythonWorkerOutboundHeaders(req, w.DialAddr)
		},
		Transport:  w.Transport,
		BufferPool: sharedProxyBufferPool,
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
	if w.Runtime != "" {
		args = append(args, "--runtime", w.Runtime)
	}

	w.Cmd = exec.Command(w.PythonBin, args...)
	w.Cmd.Stdout = os.Stdout
	w.Cmd.Stderr = os.Stderr
	fileVars, err := loadEnvFiles(w.WorkingDir, w.EnvFiles)
	if err != nil {
		return err
	}
	w.Cmd.Env = buildWorkerEnv(os.Environ(), fileVars, w.EnvVars, workerInternalEnv(w.Interface, w.CacheAddr)...)
	setSysProcAttr(w.Cmd)

	if err := w.Cmd.Start(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		port, err := waitForPortFile(w.Socket.Name(), 10*time.Second)
		if err != nil {
			_ = w.Cmd.Process.Kill()
			_ = w.Cmd.Wait()
			return fmt.Errorf("waiting for Python worker port file: %w", err)
		}
		w.DialAddr = "127.0.0.1:" + strconv.Itoa(port)
	} else if err := waitForUnixSocket(w.Socket.Name(), 10*time.Second); err != nil {
		_ = w.Cmd.Process.Kill()
		_ = w.Cmd.Wait()
		return fmt.Errorf("waiting for Python worker socket: %w", err)
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

func waitForUnixSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var dialer net.Dialer
	for time.Now().Before(deadline) {
		conn, err := dialer.DialContext(context.Background(), "unix", path)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("unix socket %s not ready within %v", path, timeout)
}

// dialWithRetry attempts to establish a connection with retry logic
func (w *PythonWorker) dialWithRetry(ctx context.Context) (net.Conn, error) {
	const maxRetries = 5
	const baseDelay = 100 * time.Millisecond

	var dialer net.Dialer
	for attempt := 0; attempt < maxRetries; attempt++ {
		conn, err := dialer.DialContext(ctx, w.DialNet, w.DialAddr)
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
		// On Windows, Signal(SIGTERM) is not supported; only Kill works.
		// Send SIGTERM on Unix for graceful shutdown (ASGI lifespan), Kill on Windows.
		if runtime.GOOS == "windows" {
			_ = w.Cmd.Process.Kill()
		} else {
			_ = w.Cmd.Process.Signal(syscall.SIGTERM)
		}
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
			_ = w.Cmd.Process.Kill()
			<-done
		}
	}
	if w.Socket != nil {
		w.Socket.Close()
		os.Remove(w.Socket.Name())
		if w.SockDir != "" {
			os.RemoveAll(w.SockDir)
		}
	}
	return nil
}

func (w *PythonWorker) HandleRequest(rw http.ResponseWriter, req *http.Request) error {
	w.Proxy.ServeHTTP(rw, req)
	return nil
}

type PythonWorkerGroup struct {
	Workers    []*PythonWorker
	roundRobin atomic.Uint64
	BundleDir  string
	ScriptPath string // path to worker_main.py inside BundleDir
}

func NewPythonWorkerGroup(iface, app, workingDir, venv, lifespan, runtime string, count int, pythonBin, cacheAddr string, envFiles []string, envVars map[string]string) (*PythonWorkerGroup, error) {
	scriptPath, bundleDir, err := writeCaddysnakePyBundle()
	if err != nil {
		return nil, fmt.Errorf("failed to write worker bundle: %w", err)
	}

	errs := make([]error, count)
	workers := make([]*PythonWorker, count)
	for i := 0; i < count; i++ {
		workers[i], errs[i] = NewPythonWorker(iface, app, workingDir, venv, lifespan, runtime, pythonBin, scriptPath, cacheAddr, envFiles, envVars)
	}
	wg := &PythonWorkerGroup{
		Workers:    workers,
		BundleDir:  bundleDir,
		ScriptPath: scriptPath,
	}
	if err := errors.Join(errs...); err != nil {
		return nil, errors.Join(wg.Cleanup(), err)
	}
	return wg, nil
}

func (wg *PythonWorkerGroup) Cleanup() error {
	if wg == nil {
		return nil
	}
	errs := make([]error, len(wg.Workers))
	for i, worker := range wg.Workers {
		if worker != nil {
			errs[i] = worker.Cleanup()
		}
	}
	if wg.BundleDir != "" {
		_ = os.RemoveAll(wg.BundleDir)
	}
	return errors.Join(errs...)
}

func (wg *PythonWorkerGroup) HandleRequest(rw http.ResponseWriter, req *http.Request) error {
	n := wg.roundRobin.Add(1)
	idx := int(n % uint64(len(wg.Workers)))
	return wg.Workers[idx].HandleRequest(rw, req)
}

func init() {
	caddy.RegisterModule(CaddySnake{})
	httpcaddyfile.RegisterHandlerDirective("python", parsePythonDirective)
	httpcaddyfile.RegisterDirectiveOrder("python", httpcaddyfile.Before, "route")
	caddycmd.RegisterCommand(caddycmd.Command{
		Name: "python-server",
		Usage: "--server-type wsgi|asgi|esgi --app <module> " +
			"[--domain <example.com>] [--listen <addr>] [--workers <count>] " +
			"[--python-path <path>] [--working-dir <path>] [--venv <path>] " +
			"[--static-path <path>] [--static-route <route>] " +
			"[--runtime <name>] " +
			"[--debug] [--access-logs] [--autoreload]",
		Short: "Spins up a Python server",
		Long: `
A Python WSGI or ASGI server designed for apps and frameworks.

You can specify a custom socket address using the '--listen' option. You can also specify the number of workers to spawn.

Providing a domain name with the '--domain' flag enables HTTPS and sets the listener to the appropriate secure port.
Ensure DNS A/AAAA records are correctly set up if using a public domain for secure connections.
`,
		CobraFunc: func(cmd *cobra.Command) {
			cmd.Flags().StringP("server-type", "t", "", "Required. The type of server to use: wsgi|asgi|esgi")
			cmd.Flags().StringP("app", "a", "", "Required. App module to be imported")
			cmd.Flags().StringP("domain", "d", "", "Domain name at which to serve the files")
			cmd.Flags().StringP("listen", "l", "", "The address to which to bind the listener")
			cmd.Flags().StringP("workers", "w", "0", "The number of workers to spawn")
			cmd.Flags().String("python-path", "", "Path to the Python interpreter")
			cmd.Flags().String("working-dir", "", "Working directory for the Python app")
			cmd.Flags().String("venv", "", "Path to a Python virtual environment to use")
			cmd.Flags().String("static-path", "", "Path to a static directory to serve: path/to/static")
			cmd.Flags().String("static-route", "/static", "Route to serve the static directory: /static")
			cmd.Flags().Bool("debug", false, "Enable debug logs")
			cmd.Flags().Bool("access-logs", false, "Enable access logs")
			cmd.Flags().Bool("autoreload", false, "Watch .py files and reload on changes")
			cmd.Flags().String("lifespan", "off", "Enable ASGI lifespan support (ignored in WSGI mode)")
			cmd.Flags().String("runtime", "", "Worker runtime (wsgi: sync|gevent; esgi: gevent only; asgi: native|uvloop); defaults: sync for WSGI, gevent for ESGI, uvloop for ASGI")
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
	workingDir := fs.String("working-dir")
	venv := fs.String("venv")
	lifespan := fs.String("lifespan")
	runtimeFlag := fs.String("runtime")

	if serverType == "" {
		return caddy.ExitCodeFailedStartup, errors.New("--server-type is required")
	}
	if serverType != "wsgi" && serverType != "asgi" && serverType != "esgi" {
		return caddy.ExitCodeFailedStartup, fmt.Errorf("invalid --server-type %q (want wsgi, asgi, or esgi)", serverType)
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
	} else if serverType == "asgi" {
		pythonHandler.ModuleAsgi = app
	} else {
		pythonHandler.ModuleEsgi = app
	}
	if venv != "" {
		pythonHandler.VenvPath = venv
	} else if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		pythonHandler.VenvPath = venv
	}

	pythonHandler.Workers = workers
	pythonHandler.PythonPath = pythonPath
	if autoreload {
		pythonHandler.Autoreload = "on"
	}
	pythonHandler.WorkingDir = workingDir
	pythonHandler.Lifespan = lifespan
	pythonHandler.Runtime = runtimeFlag

	if err := pythonHandler.Validate(); err != nil {
		return caddy.ExitCodeFailedStartup, err
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
	entries, err := os.ReadDir(libPath)
	if err != nil {
		return "", fmt.Errorf("unable to read venv lib directory: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "python3") {
			return e.Name(), nil
		}
	}
	return "", errors.New("unable to find a python3.* directory in the venv")
}
