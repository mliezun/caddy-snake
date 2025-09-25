package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/certmagic"
	caddysnake "github.com/mliezun/caddy-snake"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"

	// plug in Caddy modules here

	"github.com/caddyserver/caddy/v2/modules/caddyhttp/encode"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/encode/gzip"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/encode/zstd"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
)

func main() {
	caddycmd.RegisterCommand(caddycmd.Command{
		Name:  "python-server",
		Usage: "[--domain <example.com>] [--app <module>] [--listen <addr>] [--workers <count>] [--workers_runtime <runtime>] [--static-path <path>] [--static-route <route>] [--debug] [--access-logs]",
		Short: "Spins up a Python server",
		Long: `
A Python WSGI or ASGI server designed for apps and frameworks.

You can specify a custom socket address using the '--listen' option. You can also specify the number of workers to spawn and the runtime to use for the workers.

Providing a domain name with the '--domain' flag enables HTTPS and sets the listener to the appropriate secure port.
Ensure DNS A/AAAA records are correctly set up if using a public domain for secure connections.
`,
		CobraFunc: func(cmd *cobra.Command) {
			cmd.Flags().StringP("server-type", "t", "asgi", "The type of server to use: wsgi|asgi")
			cmd.Flags().StringP("domain", "d", "", "Domain name at which to serve the files")
			cmd.Flags().StringP("app", "a", "", "App module to be imported")
			cmd.Flags().StringP("listen", "l", "", "The address to which to bind the listener")
			cmd.Flags().StringP("workers", "w", "0", "The number of workers to spawn")
			cmd.Flags().StringP("workers-runtime", "r", "process", "The runtime to use for the workers: thread|process")
			cmd.Flags().String("static-path", "", "Path to a static directory to serve: path/to/static")
			cmd.Flags().String("static-route", "/static", "Route to serve the static directory: /static")
			cmd.Flags().Bool("debug", false, "Enable debug logs")
			cmd.Flags().Bool("access-logs", false, "Enable access logs")
			cmd.RunE = caddycmd.WrapCommandFuncForCobra(pythonServer)
		},
	})
	caddycmd.Main()
}

// pythonServer is inspired on the php-server command of the Frankenphp project (MIT License)
func pythonServer(fs caddycmd.Flags) (int, error) {
	caddy.TrapSignals()

	domain := fs.String("domain")
	app := fs.String("app")
	listen := fs.String("listen")
	workers := fs.String("workers")
	workersRuntime := fs.String("workers-runtime")
	debug := fs.Bool("debug")
	accessLogs := fs.Bool("access-logs")
	staticPath := fs.String("static-path")
	staticRoute := fs.String("static-route")
	serverType := fs.String("server-type")

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

	pythonHandler := caddysnake.CaddySnake{}
	if serverType == "wsgi" {
		pythonHandler.ModuleWsgi = app
	} else {
		pythonHandler.ModuleAsgi = app
	}
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		pythonHandler.VenvPath = venv
	}

	pythonHandler.Workers = workers
	pythonHandler.WorkersRuntime = workersRuntime

	// Create routes list
	routes := caddyhttp.RouteList{}

	// Add static file route if staticPath is provided
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

	// Add main Python route
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
