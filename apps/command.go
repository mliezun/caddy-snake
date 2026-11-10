package apps

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/encode"
	"github.com/caddyserver/caddy/v2/modules/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/term"

	caddysnake "github.com/mliezun/caddy-snake"
)

const (
	defaultListen         = "127.0.0.1:9080"
	defaultAppMemoryLimit = "512MiB"
	envAccessKey          = "CADDYSNAKE_APPS_KEY"
)

func registerCommand() {
	caddycmd.RegisterCommand(caddycmd.Command{
		Name: "python-apps",
		Usage: "[--config <file>] [--listen <addr>] [--domain <example.com>] [--public-url <url>] " +
			"[--data-dir <path>] [--database-url <url>] [--python-path <path>] " +
			"[--require-login] [--open-to-network] " +
			"[--max-apps <count>] [--app-memory-limit <size>] " +
			"[--debug] [--access-logs]",
		Short: "Runs a small platform for internal Python apps",
		Long: `
Runs a server where people, through their AI assistant, create small internal
web apps, save versions of them and publish them. Each app is served at
/<app-name>/ and gets its own Postgres database.

Assistants connect to <public-url>/_apps/mcp (an MCP server). The same actions
are available over HTTP at /_apps/api/tools and with "python-apps call".

By default the server listens on this computer only and anyone who can reach
it can use it. To serve other people, pass --require-login (and create users
with "python-apps user add"), or pass --open-to-network if the network itself
is trusted, such as a private company network.

Settings can also be kept in a file (see --config); flags override it.

Postgres: unless --database-url is given, a private database is created in the
data folder and started and stopped with the server. This needs the Postgres
programs to be installed.

Subcommands:
  user add|remove|list|password   manage who can sign in
  key create|revoke               access keys for assistants and scripts
  call <tool> [json]              run a tool against a running server
  install | uninstall             run the server as a service that starts by itself
  guide                           print the instructions given to assistants`,
		CobraFunc: func(cmd *cobra.Command) {
			pf := cmd.PersistentFlags()
			pf.String("config", "", "Settings file (default: your own apps.toml, then the machine's; see the documentation)")
			pf.String("data-dir", "", "Folder for app files, history and the built-in database (default ~/.caddysnake/apps)")
			pf.String("database-url", "", "Use this existing Postgres server (postgresql://user:password@host:5432/db) in place of the built-in one")

			f := cmd.Flags()
			f.String("listen", "", "Address to listen on (default "+defaultListen+", or :443 with --domain)")
			f.String("domain", "", "Domain name to serve with automatic HTTPS")
			f.String("public-url", "", "The address people use to reach this server, for links (default derived from --listen or --domain)")
			f.String("python-path", "", "Python interpreter used to build each app's environment (default python3; python on Windows)")
			f.Bool("require-login", false, "Ask everyone to sign in with a username and password")
			f.Bool("open-to-network", false, "Allow listening beyond this computer without logins (only on a trusted private network)")
			f.String("max-apps", "", "How many apps may be awake at the same time (default 128)")
			f.String("app-memory-limit", defaultAppMemoryLimit, "Memory ceiling per app; an app over it is restarted (\"\" for none)")
			f.Bool("debug", false, "Enable debug logs")
			f.Bool("access-logs", false, "Enable access logs")
			f.String("log-file", "", "Write the server's output to this file")
			cmd.RunE = caddycmd.WrapCommandFuncForCobra(serve)

			cmd.AddCommand(userCommand(), keyCommand(), callCommand(), installCommand(), uninstallCommand(), &cobra.Command{
				Use:   "guide",
				Short: "Print the instructions given to AI assistants",
				Args:  cobra.NoArgs,
				Run:   func(*cobra.Command, []string) { fmt.Println(Guide) },
			})
		},
	})
}

func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func derivePublicURL(listen, domain string) string {
	if domain != "" {
		return "https://" + domain
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://localhost:9080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "127.0.0.1" || host == "::1" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// ServerConfig is everything needed to build the Caddy configuration.
type ServerConfig struct {
	Config
	Listen         string
	Domain         string
	MaxApps        string
	AppMemoryLimit string
	Debug          bool
	AccessLogs     bool
	LogFile        string
}

// BuildCaddyConfig returns the Caddy configuration that serves the platform:
// one route with the gate handler followed by a python handler that loads
// each app by the first path segment.
func BuildCaddyConfig(sc ServerConfig) (*caddy.Config, error) {
	// Forward slashes on every system: Caddy reads a backslash in front of a
	// placeholder as "not a placeholder", which is what a Windows path would
	// put there. Windows accepts forward slashes.
	appsRoot := filepath.ToSlash(filepath.Join(sc.DataDir, "apps"))
	segment := "{http.request.uri.path.0}"
	python := caddysnake.CaddySnake{
		ModuleAuto:     "main:app",
		WorkingDir:     appsRoot + "/" + segment + "/current",
		VenvPath:       appsRoot + "/" + segment + "/current/.venv",
		EnvFiles:       []string{appsRoot + "/" + segment + "/settings.env"},
		MountPrefix:    "/" + segment,
		Workers:        "1",
		MaxDynamicApps: sc.MaxApps,
		MemoryLimit:    sc.AppMemoryLimit,
	}
	if err := python.Validate(); err != nil {
		return nil, err
	}
	gzip, err := caddy.GetModule("http.encoders.gzip")
	if err != nil {
		return nil, err
	}
	zstd, err := caddy.GetModule("http.encoders.zstd")
	if err != nil {
		return nil, err
	}
	route := caddyhttp.Route{
		HandlersRaw: []json.RawMessage{
			caddyconfig.JSONModuleObject(encode.Encode{
				EncodingsRaw: caddy.ModuleMap{
					"zstd": caddyconfig.JSON(zstd.New(), nil),
					"gzip": caddyconfig.JSON(gzip.New(), nil),
				},
				Prefer: []string{"zstd", "gzip"},
			}, "handler", "encode", nil),
			caddyconfig.JSONModuleObject(Handler{Config: sc.Config}, "handler", "python_apps", nil),
			caddyconfig.JSONModuleObject(python, "handler", "python", nil),
		},
	}
	if sc.Domain != "" {
		route.MatcherSetsRaw = []caddy.ModuleMap{{"host": caddyconfig.JSON(caddyhttp.MatchHost{sc.Domain}, nil)}}
	}
	server := &caddyhttp.Server{
		ReadHeaderTimeout: caddy.Duration(10 * time.Second),
		IdleTimeout:       caddy.Duration(60 * time.Second),
		MaxHeaderBytes:    1024 * 16,
		Routes:            caddyhttp.RouteList{route},
		Listen:            []string{sc.Listen},
	}
	if sc.AccessLogs {
		server.Logs = &caddyhttp.ServerLogConfig{}
	}
	persist := false
	cfg := &caddy.Config{
		Admin: &caddy.AdminConfig{Disabled: true, Config: &caddy.ConfigSettings{Persist: &persist}},
		AppsRaw: caddy.ModuleMap{
			"http": caddyconfig.JSON(caddyhttp.App{Servers: map[string]*caddyhttp.Server{"apps": server}}, nil),
		},
	}
	if sc.Debug || sc.LogFile != "" {
		base := caddy.BaseLog{}
		if sc.Debug {
			base.Level = zapcore.DebugLevel.CapitalString()
		}
		if sc.LogFile != "" {
			// The file is rotated by Caddy when it grows large.
			base.WriterRaw = caddyconfig.JSONModuleObject(logging.FileWriter{Filename: sc.LogFile}, "output", "file", nil)
		}
		cfg.Logging = &caddy.Logging{Logs: map[string]*caddy.CustomLog{"default": {BaseLog: base}}}
	}
	return cfg, nil
}

// configFromFlags returns where the data lives, for the subcommands that work
// on it directly. It honours the configuration file like the server does.
func configFromFlags(fs caddycmd.Flags) (Config, error) {
	settings, _, err := settingsFromFlags(fs)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{DataDir: settings.DataDir, DatabaseURL: settings.DatabaseURL}
	return cfg, cfg.resolve()
}

func serve(fs caddycmd.Flags) (int, error) {
	settings, configPath, err := settingsFromFlags(fs)
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}
	sc, err := settings.serverConfig()
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}
	cfg := sc.Config
	caddyCfg, err := BuildCaddyConfig(sc)
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}
	// Stop cleanly on Ctrl-C so the built-in database is shut down too.
	caddy.TrapSignals()
	if err := caddy.Run(caddyCfg); err != nil {
		return caddy.ExitCodeFailedStartup, err
	}
	if configPath == "" {
		configPath = "none"
	}
	fmt.Fprintf(os.Stderr, "\ncaddy-snake apps is running.\n  Open:               %s/\n  Assistant (MCP):    %s%s/mcp\n  Data folder:        %s\n  Settings file:      %s\n\n",
		cfg.PublicURL, cfg.PublicURL, platformPrefix, cfg.DataDir, configPath)
	select {}
}

// --- subcommands that work on the data folder directly ----------------------

func withPlatform(cmd *cobra.Command, fn func(ctx context.Context, p *Platform) error) error {
	cfg, err := configFromFlags(caddycmd.Flags{FlagSet: cmd.Flags()})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p, err := Open(ctx, cfg, zap.NewNop())
	if err != nil {
		return err
	}
	defer p.Close()
	return fn(ctx, p)
}

func readPassword(fromStdin bool) (string, error) {
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password was given on standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "Password: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Again:    ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(first, second) {
		return "", errors.New("the two passwords do not match")
	}
	return string(first), nil
}

func userCommand() *cobra.Command {
	user := &cobra.Command{Use: "user", Short: "Manage who can sign in"}
	add := &cobra.Command{
		Use:   "add <username>",
		Short: "Add a person (asks for a password)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			admin, _ := cmd.Flags().GetBool("admin")
			stdin, _ := cmd.Flags().GetBool("password-stdin")
			password, err := readPassword(stdin)
			if err != nil {
				return err
			}
			return withPlatform(cmd, func(ctx context.Context, p *Platform) error {
				if err := p.AddUser(ctx, args[0], password, admin); err != nil {
					return err
				}
				fmt.Printf("Added %s.\n", args[0])
				return nil
			})
		},
	}
	add.Flags().Bool("admin", false, "Make this person an administrator")
	add.Flags().Bool("password-stdin", false, "Read the password from standard input")
	password := &cobra.Command{
		Use:   "password <username>",
		Short: "Set a new password for a person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, _ := cmd.Flags().GetBool("password-stdin")
			pw, err := readPassword(stdin)
			if err != nil {
				return err
			}
			return withPlatform(cmd, func(ctx context.Context, p *Platform) error {
				if err := p.SetPassword(ctx, args[0], pw); err != nil {
					return err
				}
				fmt.Printf("Password changed for %s.\n", args[0])
				return nil
			})
		},
	}
	password.Flags().Bool("password-stdin", false, "Read the password from standard input")
	remove := &cobra.Command{
		Use:   "remove <username>",
		Short: "Remove a person, their sessions and their access keys",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPlatform(cmd, func(ctx context.Context, p *Platform) error {
				if err := p.RemoveUser(ctx, args[0]); err != nil {
					return err
				}
				fmt.Printf("Removed %s.\n", args[0])
				return nil
			})
		},
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List everyone who can sign in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPlatform(cmd, func(ctx context.Context, p *Platform) error {
				users, err := p.Users(ctx)
				if err != nil {
					return err
				}
				for _, u := range users {
					role := ""
					if u.Admin {
						role = "  (administrator)"
					}
					fmt.Println(u.Username + role)
				}
				return nil
			})
		},
	}
	user.AddCommand(add, password, remove, list)
	return user
}

func keyCommand() *cobra.Command {
	key := &cobra.Command{Use: "key", Short: "Access keys for assistants and scripts"}
	create := &cobra.Command{
		Use:   "create <username>",
		Short: "Create an access key for a person; it is shown once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			return withPlatform(cmd, func(ctx context.Context, p *Platform) error {
				k, err := p.CreateAccessKey(ctx, args[0], name)
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr, "Give this key to the assistant as its Bearer token. It will not be shown again:")
				fmt.Println(k)
				return nil
			})
		},
	}
	create.Flags().String("name", "assistant", "A label to recognise the key by")
	revoke := &cobra.Command{
		Use:   "revoke <username>",
		Short: "Remove a person's access keys with the given name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			return withPlatform(cmd, func(ctx context.Context, p *Platform) error {
				n, err := p.RevokeAccessKeys(ctx, args[0], name)
				if err != nil {
					return err
				}
				fmt.Printf("Removed %d key(s).\n", n)
				return nil
			})
		},
	}
	revoke.Flags().String("name", "assistant", "The label of the keys to remove")
	key.AddCommand(create, revoke)
	return key
}

// callCommand talks to a running server over HTTP, like an assistant would.
func callCommand() *cobra.Command {
	call := &cobra.Command{
		Use:   "call <tool> [json-arguments]",
		Short: "Run a tool against a running server (see \"call list\")",
		Long: `Runs one tool against a running server and prints the JSON result.

  python-apps call list
  python-apps call create_app '{"name": "Team lunch orders"}'
  python-apps call get_logs '{"app": "team-lunch-orders"}'

If the server requires login, pass --key or set ` + envAccessKey + `.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			server, _ := cmd.Flags().GetString("server")
			key, _ := cmd.Flags().GetString("key")
			if key == "" {
				key = os.Getenv(envAccessKey)
			}
			base := strings.TrimSuffix(server, "/") + platformPrefix + "/api/tools"
			var req *http.Request
			var err error
			if args[0] == "list" {
				req, err = http.NewRequest(http.MethodGet, base, nil)
			} else {
				body := "{}"
				if len(args) == 2 {
					body = args[1]
				}
				if !json.Valid([]byte(body)) {
					return errors.New("the arguments must be JSON, for example '{\"app\": \"inventory\"}'")
				}
				req, err = http.NewRequest(http.MethodPost, base+"/"+args[0], strings.NewReader(body))
				if req != nil {
					req.Header.Set("Content-Type", "application/json")
				}
			}
			if err != nil {
				return err
			}
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
			if err != nil {
				return fmt.Errorf("could not reach the server at %s: %w", server, err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			var pretty bytes.Buffer
			if json.Indent(&pretty, data, "", "  ") == nil {
				data = pretty.Bytes()
			}
			fmt.Println(strings.TrimSpace(string(data)))
			if resp.StatusCode >= 400 {
				return fmt.Errorf("the server answered %s", resp.Status)
			}
			return nil
		},
	}
	call.Flags().String("server", "http://localhost:9080", "Address of the running server")
	call.Flags().String("key", "", "Access key (or set "+envAccessKey+")")
	return call
}
