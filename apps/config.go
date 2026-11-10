package apps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/caddyserver/certmagic"
)

// Where settings come from, strongest first:
//
//  1. command-line flags
//  2. one configuration file
//  3. built-in defaults
//
// The configuration file is the first of these that applies:
//
//  1. --config <path>
//  2. $CADDYSNAKE_APPS_CONFIG
//  3. the person's own file: $XDG_CONFIG_HOME/caddysnake/apps.toml
//     (~/.config/caddysnake/apps.toml)
//  4. the machine's file: /etc/caddysnake/apps.toml on Linux,
//     /usr/local/etc/caddysnake/apps.toml on macOS,
//     %ProgramData%\caddysnake\apps.toml on Windows
//
// A file that exists but cannot be read is an error, never skipped: silently
// falling back to defaults would start a second, empty server next to the
// real one.
const (
	envConfigPath     = "CADDYSNAKE_APPS_CONFIG"
	configFileName    = "apps.toml"
	configDirName     = "caddysnake"
	configFileComment = "# caddy-snake apps settings. Flags on the command line override this file.\n# Documentation: https://caddy-snake.readthedocs.io/en/latest/docs/apps\n\n"
)

// Settings is every option of the server, as written in the configuration file.
type Settings struct {
	Listen        string `toml:"listen,omitempty"`
	Domain        string `toml:"domain,omitempty"`
	PublicURL     string `toml:"public_url,omitempty"`
	DataDir       string `toml:"data_dir,omitempty"`
	DatabaseURL   string `toml:"database_url,omitempty"`
	PythonPath    string `toml:"python_path,omitempty"`
	RequireLogin  bool   `toml:"require_login"`
	OpenToNetwork bool   `toml:"open_to_network,omitempty"`
	MaxApps       int    `toml:"max_apps,omitzero"`
	// AppMemoryLimit is a pointer so that an explicit "" (no limit) can be told
	// apart from "not set" (use the default).
	AppMemoryLimit *string `toml:"app_memory_limit,omitempty"`
	Debug          bool    `toml:"debug,omitempty"`
	AccessLogs     bool    `toml:"access_logs,omitempty"`
	// LogFile sends the server's output to a file. A Windows service has
	// nowhere else to write it.
	LogFile string `toml:"log_file,omitempty"`
}

// systemConfigPathFor is the machine-wide configuration file on an operating
// system. macOS keeps third-party settings out of /etc, under /usr/local.
func systemConfigPathFor(goos string) string {
	switch goos {
	case "darwin":
		return "/usr/local/etc/caddysnake/apps.toml"
	case "windows":
		return winJoin(winDataRoot(), configFileName)
	}
	return "/etc/caddysnake/apps.toml"
}

// userConfigPath is the configuration file of the person running the command.
// It is the same on Linux and macOS: command-line tools on both keep their
// settings under ~/.config, and it avoids paths with spaces. On Windows it is
// under %AppData%, where programs keep a person's settings.
func userConfigPath() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("AppData"); dir != "" {
			return filepath.Join(dir, configDirName, configFileName)
		}
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, configDirName, configFileName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", configDirName, configFileName)
}

// findConfigFile returns the configuration file to use, or "" when there is
// none. explicit is the --config flag.
func findConfigFile(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv(envConfigPath); env != "" {
		return env, nil
	}
	for _, candidate := range []string{userConfigPath(), systemConfigPathFor(runtime.GOOS)} {
		if candidate == "" {
			continue
		}
		_, err := os.Stat(candidate)
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checking for settings at %s: %w", candidate, err)
		}
	}
	return "", nil
}

// loadSettings reads a configuration file. Unknown names are an error, so a
// misspelt setting is noticed instead of ignored.
func loadSettings(path string) (Settings, error) {
	var s Settings
	meta, err := toml.DecodeFile(path, &s)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return s, fmt.Errorf("the settings file %s cannot be read by this user; run the command with sudo, or as the user the server runs as", path)
		}
		return s, fmt.Errorf("reading settings from %s: %w", path, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for _, key := range unknown {
			names = append(names, key.String())
		}
		sort.Strings(names)
		return s, fmt.Errorf("%s has settings this version does not know: %s", path, strings.Join(names, ", "))
	}
	return s, nil
}

// encodeSettings renders a configuration file.
func encodeSettings(s Settings) (string, error) {
	var b strings.Builder
	b.WriteString(configFileComment)
	if err := toml.NewEncoder(&b).Encode(s); err != nil {
		return "", err
	}
	return b.String(), nil
}

// settingsFromFlags merges the configuration file (if any) with the flags that
// were actually given. It returns the file that was used, "" if none.
func settingsFromFlags(fs caddycmd.Flags) (Settings, string, error) {
	explicit := ""
	if fs.Lookup("config") != nil {
		explicit = fs.String("config")
	}
	path, err := findConfigFile(explicit)
	if err != nil {
		return Settings{}, "", err
	}
	var s Settings
	if path != "" {
		if s, err = loadSettings(path); err != nil {
			return s, path, err
		}
	}
	given := func(name string) bool { return fs.Lookup(name) != nil && fs.Changed(name) }
	text := map[string]*string{
		"listen": &s.Listen, "domain": &s.Domain, "public-url": &s.PublicURL, "data-dir": &s.DataDir,
		"database-url": &s.DatabaseURL, "python-path": &s.PythonPath, "log-file": &s.LogFile,
	}
	for name, target := range text {
		if given(name) {
			*target = fs.String(name)
		}
	}
	flags := map[string]*bool{
		"require-login": &s.RequireLogin, "open-to-network": &s.OpenToNetwork, "debug": &s.Debug, "access-logs": &s.AccessLogs,
	}
	for name, target := range flags {
		if given(name) {
			*target = fs.Bool(name)
		}
	}
	if given("max-apps") {
		n, err := strconv.Atoi(fs.String("max-apps"))
		if err != nil || n <= 0 {
			return s, path, fmt.Errorf("--max-apps must be a positive number, got %q", fs.String("max-apps"))
		}
		s.MaxApps = n
	}
	if given("app-memory-limit") {
		limit := fs.String("app-memory-limit")
		s.AppMemoryLimit = &limit
	}
	return s, path, nil
}

// errOpenToEveryone explains why a server reachable from the network is
// refused without sign-in.
var errOpenToEveryone = errors.New(
	"refusing to start: this would let anyone who can reach the server create and run apps on it.\n" +
		"  - To make people sign in, turn on require-login and create a user with `python-apps user add <name> --admin`.\n" +
		"  - If the server is only reachable on a trusted private network, turn on open-to-network.\n" +
		"  - Or leave out listen/domain to serve this computer only.")

// serverConfig applies defaults and safety checks and returns what the server
// runs with.
func (s Settings) serverConfig() (ServerConfig, error) {
	cfg := Config{DataDir: s.DataDir, DatabaseURL: s.DatabaseURL, PythonBin: s.PythonPath, RequireLogin: s.RequireLogin}
	if err := cfg.resolve(); err != nil {
		return ServerConfig{}, err
	}
	listen := s.Listen
	if listen == "" {
		listen = defaultListen
		if s.Domain != "" {
			listen = ":" + strconv.Itoa(certmagic.HTTPSPort)
		}
	}
	cfg.PublicURL = strings.TrimSuffix(s.PublicURL, "/")
	if cfg.PublicURL == "" {
		cfg.PublicURL = derivePublicURL(listen, s.Domain)
	}
	if !s.RequireLogin && !s.OpenToNetwork && (s.Domain != "" || !isLoopbackListen(listen)) {
		return ServerConfig{}, errOpenToEveryone
	}
	memory := defaultAppMemoryLimit
	if s.AppMemoryLimit != nil {
		memory = *s.AppMemoryLimit
	}
	maxApps := ""
	if s.MaxApps > 0 {
		maxApps = strconv.Itoa(s.MaxApps)
	}
	return ServerConfig{
		Config:         cfg,
		Listen:         listen,
		Domain:         s.Domain,
		MaxApps:        maxApps,
		AppMemoryLimit: memory,
		Debug:          s.Debug,
		AccessLogs:     s.AccessLogs,
		LogFile:        s.LogFile,
	}, nil
}
