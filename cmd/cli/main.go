package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
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
)

func main() {
	caddycmd.RegisterCommand(caddycmd.Command{
		Name:  "wsgi-server",
		Usage: "[--domain <example.com>] [--app <module>] [--listen <addr>]",
		Short: "Spins up a Python wsgi server",
		Long: `
A Python WSGI server designed for development, demonstrations, and lightweight production use.

You can specify a custom socket address using the '--listen' option.

Providing a domain name with the '--domain' flag enables HTTPS and sets the listener to the appropriate secure port.
Ensure DNS A/AAAA records are correctly set up if using a public domain for secure connections.
`,
		CobraFunc: func(cmd *cobra.Command) {
			cmd.Flags().StringP("domain", "d", "", "Domain name at which to serve the files")
			cmd.Flags().StringP("app", "a", "", "App module to be imported")
			cmd.Flags().StringP("listen", "l", "", "The address to which to bind the listener")
			cmd.Flags().Bool("debug", false, "Enable debug logs")
			cmd.Flags().Bool("access-logs", false, "Enable access logs")
			cmd.RunE = caddycmd.WrapCommandFuncForCobra(cmdWsgiServer)
		},
	})
	caddycmd.Main()
}

// cmdWsgiServer is freely inspired from the php-server command of the Frankenphp project (MIT License)
func cmdWsgiServer(fs caddycmd.Flags) (int, error) {
	caddy.TrapSignals()

	domain := fs.String("domain")
	app := fs.String("app")
	listen := fs.String("listen")
	debug := fs.Bool("debug")
	accessLogs := fs.Bool("access-logs")

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

	pythonHandler := caddysnake.CaddySnake{
		ModuleWsgi: app,
	}
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		pythonHandler.VenvPath = venv
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

	subroute := caddyhttp.Subroute{
		Routes: caddyhttp.RouteList{mainRoute},
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
