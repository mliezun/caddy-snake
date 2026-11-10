package apps

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/spf13/pflag"
)

// serverFlags builds a flag set like the python-apps command's, then parses args.
func serverFlags(t *testing.T, args ...string) caddycmd.Flags {
	t.Helper()
	fs := pflag.NewFlagSet("python-apps", pflag.ContinueOnError)
	for _, name := range []string{"config", "data-dir", "database-url", "listen", "domain", "public-url", "python-path", "max-apps", "log-file"} {
		fs.String(name, "", "")
	}
	fs.String("app-memory-limit", defaultAppMemoryLimit, "")
	for _, name := range []string{"require-login", "open-to-network", "debug", "access-logs"} {
		fs.Bool(name, false, "")
	}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return caddycmd.Flags{FlagSet: fs}
}

// isolateConfig points every configuration location at empty temp folders.
func isolateConfig(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(envConfigPath, "")
	return home
}

// unixPaths skips a test that checks Linux and macOS locations, which are
// built with this machine's path rules.
func unixPaths(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("checks Linux and macOS paths")
	}
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFileLocations(t *testing.T) {
	unixPaths(t)
	home := isolateConfig(t)
	if got := userConfigPath(); got != filepath.Join(home, ".config", "caddysnake", "apps.toml") {
		t.Fatalf("userConfigPath = %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if got := userConfigPath(); got != filepath.Join(home, "xdg", "caddysnake", "apps.toml") {
		t.Fatalf("userConfigPath with XDG_CONFIG_HOME = %q", got)
	}
	// A relative XDG_CONFIG_HOME is invalid by the specification and ignored.
	t.Setenv("XDG_CONFIG_HOME", "relative/dir")
	if got := userConfigPath(); got != filepath.Join(home, ".config", "caddysnake", "apps.toml") {
		t.Fatalf("relative XDG_CONFIG_HOME was used: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")

	// Order: --config, then the environment variable, then the person's file.
	userFile := userConfigPath()
	writeConfig(t, userFile, `listen = "127.0.0.1:1111"`)
	if got, _ := findConfigFile(""); got != userFile {
		t.Fatalf("findConfigFile = %q, want the person's file", got)
	}
	t.Setenv(envConfigPath, "/somewhere/from-env.toml")
	if got, _ := findConfigFile(""); got != "/somewhere/from-env.toml" {
		t.Fatalf("environment variable not honoured: %q", got)
	}
	if got, _ := findConfigFile("/explicit.toml"); got != "/explicit.toml" {
		t.Fatalf("--config not honoured: %q", got)
	}
}

func TestSettingsFileAndFlagPrecedence(t *testing.T) {
	home := isolateConfig(t)
	file := filepath.Join(home, "apps.toml")
	writeConfig(t, file, `
listen = "127.0.0.1:7000"
public_url = "http://apps.internal:7000"
data_dir = '`+filepath.Join(home, "data")+`'
require_login = true
max_apps = 40
app_memory_limit = "256MiB"
`)
	s, used, err := settingsFromFlags(serverFlags(t, "--config", file))
	if err != nil {
		t.Fatal(err)
	}
	if used != file || s.Listen != "127.0.0.1:7000" || !s.RequireLogin || s.MaxApps != 40 || *s.AppMemoryLimit != "256MiB" {
		t.Fatalf("settings from file = %+v (file %q)", s, used)
	}
	sc, err := s.serverConfig()
	if err != nil {
		t.Fatal(err)
	}
	if sc.Listen != "127.0.0.1:7000" || sc.PublicURL != "http://apps.internal:7000" || sc.MaxApps != "40" || sc.AppMemoryLimit != "256MiB" || !sc.RequireLogin {
		t.Fatalf("server config = %+v", sc)
	}

	// A flag that was given wins; flags left at their default do not override the file.
	s, _, err = settingsFromFlags(serverFlags(t, "--config", file, "--listen", "127.0.0.1:9999", "--app-memory-limit", ""))
	if err != nil {
		t.Fatal(err)
	}
	if s.Listen != "127.0.0.1:9999" || !s.RequireLogin || s.MaxApps != 40 || *s.AppMemoryLimit != "" {
		t.Fatalf("flags did not merge over the file correctly: %+v", s)
	}

	// No file anywhere: defaults.
	s, used, err = settingsFromFlags(serverFlags(t))
	if err != nil || used != "" {
		t.Fatalf("no file: used %q err %v", used, err)
	}
	sc, err = s.serverConfig()
	if err != nil || sc.Listen != defaultListen || sc.AppMemoryLimit != defaultAppMemoryLimit || sc.PublicURL != "http://localhost:9080" {
		t.Fatalf("defaults = %+v, err %v", sc, err)
	}
}

func TestSettingsFileMistakesAreErrors(t *testing.T) {
	home := isolateConfig(t)
	file := filepath.Join(home, "apps.toml")

	writeConfig(t, file, `listn = "127.0.0.1:7000"`)
	if _, _, err := settingsFromFlags(serverFlags(t, "--config", file)); err == nil || !strings.Contains(err.Error(), "listn") {
		t.Fatalf("misspelt setting was not reported: %v", err)
	}
	writeConfig(t, file, `listen = `)
	if _, _, err := settingsFromFlags(serverFlags(t, "--config", file)); err == nil {
		t.Fatal("invalid TOML accepted")
	}
	if _, _, err := settingsFromFlags(serverFlags(t, "--config", filepath.Join(home, "missing.toml"))); err == nil {
		t.Fatal("a named settings file that does not exist must be an error")
	}
	if _, _, err := settingsFromFlags(serverFlags(t, "--max-apps", "many")); err == nil {
		t.Fatal("non-numeric --max-apps accepted")
	}

	// The network guardrail applies to settings from a file too.
	writeConfig(t, file, `listen = "0.0.0.0:9080"`)
	s, _, err := settingsFromFlags(serverFlags(t, "--config", file))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.serverConfig(); err == nil {
		t.Fatal("a file that opens the server to the network without sign-in was accepted")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	limit := ""
	in := Settings{Listen: "127.0.0.1:9080", DataDir: "/var/lib/caddysnake/apps", RequireLogin: true, MaxApps: 12, AppMemoryLimit: &limit}
	text, err := encodeSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "apps.toml")
	writeConfig(t, file, text)
	out, err := loadSettings(file)
	if err != nil {
		t.Fatalf("written settings do not load: %v\n%s", err, text)
	}
	if out.Listen != in.Listen || out.DataDir != in.DataDir || !out.RequireLogin || out.MaxApps != 12 || out.AppMemoryLimit == nil || *out.AppMemoryLimit != "" {
		t.Fatalf("round trip = %+v\n%s", out, text)
	}
	if !strings.Contains(text, "require_login = true") {
		t.Fatalf("require_login should always be written:\n%s", text)
	}
}

func TestSystemInstallPlan(t *testing.T) {
	unixPaths(t)
	plan, err := buildInstallPlan(installOptions{
		OS:     "linux",
		Binary: "/home/ana/.venv/lib/python3.13/site-packages/caddysnake-cli",
		UV:     "/home/ana/.venv/bin/uv",
		Settings: Settings{
			Domain: "apps.example.com", RequireLogin: true, PythonPath: "/home/ana/.venv/bin/python3",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ConfigPath != "/etc/caddysnake/apps.toml" || plan.UnitPath != "/etc/systemd/system/caddysnake-apps.service" || plan.DataDir != "/var/lib/caddysnake/apps" {
		t.Fatalf("paths = %q %q %q", plan.ConfigPath, plan.UnitPath, plan.DataDir)
	}
	// Programs are copied out of the home folder, which the service cannot read.
	if len(plan.Copies) != 2 || plan.Copies[0].To != "/usr/local/lib/caddysnake/caddysnake-cli" || plan.Copies[1].To != "/usr/local/lib/caddysnake/uv" {
		t.Fatalf("copies = %+v", plan.Copies)
	}
	config, unit := plan.Files[0], plan.Files[1]
	if !config.KeepExisting || config.Mode != 0o640 || config.Owner != "root:caddysnake" {
		t.Fatalf("settings file must be kept if present and private to root and the service: %+v", config)
	}
	for _, want := range []string{`domain = "apps.example.com"`, "require_login = true", `data_dir = "/var/lib/caddysnake/apps"`, `python_path = "3.13"`} {
		if !strings.Contains(config.Content, want) {
			t.Errorf("settings are missing %s:\n%s", want, config.Content)
		}
	}
	if strings.Contains(config.Content, "max_apps") {
		t.Errorf("unset settings should not be written:\n%s", config.Content)
	}
	if strings.Contains(config.Content, "/home/ana") {
		t.Errorf("settings point into a home folder:\n%s", config.Content)
	}
	for _, want := range []string{
		"User=caddysnake",
		"ExecStart=/usr/local/lib/caddysnake/caddysnake-cli python-apps --config /etc/caddysnake/apps.toml",
		"Environment=PATH=/usr/local/lib/caddysnake:",
		"KillSignal=SIGINT",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"ReadWritePaths=/var/lib/caddysnake\n",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit.Content, want) {
			t.Errorf("unit is missing %q:\n%s", want, unit.Content)
		}
	}
	if unit.KeepExisting {
		t.Error("the unit file is ours and should be rewritten on reinstall")
	}
	joined := ""
	for _, c := range plan.Before {
		joined += quoteCommand(c) + "\n"
	}
	if !strings.Contains(joined, "useradd --system") || !strings.Contains(joined, "id -u caddysnake") {
		t.Errorf("the service account is not created only when missing:\n%s", joined)
	}
	if got := quoteCommand(plan.After[len(plan.After)-1]); got != "systemctl restart caddysnake-apps.service" {
		t.Errorf("last step = %q", got)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "user add <name> --admin") {
		t.Errorf("no reminder to add the first person: %v", plan.Notes)
	}

	// A data folder elsewhere is made writable for the confined service.
	plan, err = buildInstallPlan(installOptions{OS: "linux", Binary: "/x/caddysnake-cli", Settings: Settings{DataDir: "/srv/apps-data"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Files[1].Content, "ReadWritePaths=/var/lib/caddysnake /srv/apps-data") {
		t.Errorf("custom data folder is not writable in the unit:\n%s", plan.Files[1].Content)
	}
	// Without uv there is nothing to download a Python with: use the system one.
	if !strings.Contains(plan.Files[0].Content, `python_path = "/usr/bin/python3"`) {
		t.Errorf("no usable Python chosen without uv:\n%s", plan.Files[0].Content)
	}
	// A system Python that the service can read is kept.
	plan, _ = buildInstallPlan(installOptions{OS: "linux", Binary: "/x/caddysnake-cli", UV: "/usr/bin/uv", Settings: Settings{PythonPath: "/usr/bin/python3.13"}})
	if !strings.Contains(plan.Files[0].Content, `python_path = "/usr/bin/python3.13"`) {
		t.Errorf("a readable system Python was replaced:\n%s", plan.Files[0].Content)
	}
}

func TestUserInstallPlan(t *testing.T) {
	unixPaths(t)
	home := isolateConfig(t)
	plan, err := buildInstallPlan(installOptions{OS: "linux", User: true, Binary: "/opt/venv/caddysnake-cli", UV: "/opt/venv/bin/uv"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ConfigPath != filepath.Join(home, ".config", "caddysnake", "apps.toml") ||
		plan.UnitPath != filepath.Join(home, ".config", "systemd", "user", "caddysnake-apps.service") ||
		plan.DataDir != filepath.Join(home, ".caddysnake", "apps") {
		t.Fatalf("paths = %q %q %q", plan.ConfigPath, plan.UnitPath, plan.DataDir)
	}
	if len(plan.Copies) != 0 || len(plan.Before) != 0 {
		t.Fatalf("a user install must not copy programs or run setup commands: %+v %+v", plan.Copies, plan.Before)
	}
	unit := plan.Files[1].Content
	for _, want := range []string{"ExecStart=/opt/venv/caddysnake-cli python-apps --config " + plan.ConfigPath, "Environment=PATH=/opt/venv/bin:", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("user unit is missing %q:\n%s", want, unit)
		}
	}
	for _, unwanted := range []string{"User=", "ProtectHome", "AmbientCapabilities"} {
		if strings.Contains(unit, unwanted) {
			t.Errorf("user unit should not contain %q", unwanted)
		}
	}
	if got := quoteCommand(plan.After[1]); got != "systemctl --user enable caddysnake-apps.service" {
		t.Errorf("enable step = %q", got)
	}

	var out bytes.Buffer
	plan.describe(&out)
	if !strings.Contains(out.String(), "Nothing has been changed") || !strings.Contains(out.String(), plan.UnitPath) {
		t.Errorf("dry run output:\n%s", out.String())
	}
}

func TestInstallRefusesAnOpenNetworkServer(t *testing.T) {
	for _, s := range []Settings{{Listen: "0.0.0.0:9080"}, {Domain: "apps.example.com"}} {
		if _, err := buildInstallPlan(installOptions{OS: "linux", Binary: "/x/caddysnake-cli", Settings: s}); err == nil {
			t.Errorf("install accepted %+v without sign-in", s)
		}
	}
	if _, err := buildInstallPlan(installOptions{OS: "linux", Binary: "/x/caddysnake-cli", Settings: Settings{Listen: "0.0.0.0:9080", OpenToNetwork: true}}); err != nil {
		t.Errorf("open-to-network was refused: %v", err)
	}
	if _, err := buildInstallPlan(installOptions{}); err == nil {
		t.Error("an install without a program to install was accepted")
	}
}

func TestApplyKeepsExistingSettings(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "etc", "apps.toml")
	unit := filepath.Join(dir, "unit", "caddysnake-apps.service")
	writeConfig(t, config, "listen = \"127.0.0.1:1234\"\n")
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("old unit"), 0o644); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, "src-program")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := &installPlan{
		User:    true,
		DataDir: filepath.Join(dir, "data"),
		Copies:  []copyFile{{From: program, To: filepath.Join(dir, "dst-program")}},
		Files: []writeFile{
			{Path: config, Content: "listen = \"0.0.0.0:1\"\n", Mode: 0o600, KeepExisting: true},
			{Path: unit, Content: "new unit", Mode: 0o644},
		},
	}
	var out bytes.Buffer
	if err := plan.apply(&out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(config); string(got) != "listen = \"127.0.0.1:1234\"\n" {
		t.Fatalf("existing settings were overwritten: %q", got)
	}
	if got, _ := os.ReadFile(unit); string(got) != "new unit" {
		t.Fatalf("unit was not rewritten: %q", got)
	}
	// Windows has no executable bit.
	if info, err := os.Stat(filepath.Join(dir, "dst-program")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o755) {
		t.Fatalf("program not copied as executable: %v", err)
	}
	if _, err := os.Stat(plan.DataDir); err != nil {
		t.Fatalf("data folder not created: %v", err)
	}
	if !strings.Contains(out.String(), "Kept your existing settings") {
		t.Fatalf("no message about kept settings: %q", out.String())
	}
}

func TestMacInstallPlans(t *testing.T) {
	unixPaths(t)
	home := isolateConfig(t)

	// For your own account: a LaunchAgent, nothing copied, no administrator rights.
	plan, err := buildInstallPlan(installOptions{OS: "darwin", User: true, UID: "501", Binary: "/opt/venv/caddysnake-cli", UV: "/opt/venv/bin/uv"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.UnitPath != filepath.Join(home, "Library", "LaunchAgents", "caddysnake.apps.plist") ||
		plan.ConfigPath != filepath.Join(home, ".config", "caddysnake", "apps.toml") ||
		plan.LogPath != filepath.Join(home, "Library", "Logs", "caddysnake-apps.log") {
		t.Fatalf("paths = %q %q %q", plan.UnitPath, plan.ConfigPath, plan.LogPath)
	}
	if len(plan.Copies) != 0 || len(plan.Before) != 0 {
		t.Fatalf("a user install must not copy programs or run setup commands")
	}
	plist := plan.Files[1].Content
	for _, want := range []string{
		"<string>caddysnake.apps</string>",
		"<string>/opt/venv/caddysnake-cli</string>",
		"<string>python-apps</string>",
		"<string>" + plan.ConfigPath + "</string>",
		"<string>/opt/venv/bin:/opt/homebrew/bin:",
		"<key>RunAtLoad</key>",
		"<key>SuccessfulExit</key>",
		"<string>" + plan.LogPath + "</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist is missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "UserName") {
		t.Error("a LaunchAgent must not set UserName")
	}
	if got := quoteCommand(plan.After[0]); got != "launchctl bootstrap gui/501 "+plan.UnitPath {
		t.Errorf("load step = %q", got)
	}
	if got := quoteCommand(plan.Lenient[0]); got != "launchctl bootout gui/501/caddysnake.apps" {
		t.Errorf("unload-before-reinstall step = %q", got)
	}

	// For the machine: a LaunchDaemon that runs as the person installing it.
	plan, err = buildInstallPlan(installOptions{OS: "darwin", RunAs: "ana", UID: "501", Binary: "/Users/ana/venv/caddysnake-cli", UV: "/Users/ana/venv/bin/uv",
		Settings: Settings{Domain: "apps.example.com", RequireLogin: true}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.UnitPath != "/Library/LaunchDaemons/caddysnake.apps.plist" || plan.ConfigPath != "/usr/local/etc/caddysnake/apps.toml" || plan.DataDir != "/usr/local/var/caddysnake/apps" {
		t.Fatalf("paths = %q %q %q", plan.UnitPath, plan.ConfigPath, plan.DataDir)
	}
	plist = plan.Files[1].Content
	for _, want := range []string{"<key>UserName</key>", "<string>ana</string>", "<string>/usr/local/lib/caddysnake/caddysnake-cli</string>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("daemon plist is missing %q:\n%s", want, plist)
		}
	}
	if plan.Files[0].Owner != "ana" || !plan.Files[0].KeepExisting || plan.Files[1].Owner != "root:wheel" {
		t.Errorf("ownership: settings %q, plist %q", plan.Files[0].Owner, plan.Files[1].Owner)
	}
	if got := quoteCommand(plan.After[0]); got != "launchctl bootstrap system /Library/LaunchDaemons/caddysnake.apps.plist" {
		t.Errorf("load step = %q", got)
	}
	for _, f := range plan.Files {
		if strings.Contains(f.Path, " ") {
			t.Errorf("path with a space: %q", f.Path)
		}
	}
	// As root there is nobody to run the service as.
	if _, err := buildInstallPlan(installOptions{OS: "darwin", RunAs: "root", Binary: "/x/caddysnake-cli"}); err == nil {
		t.Error("a macOS system install as root was accepted")
	}
	if _, err := buildInstallPlan(installOptions{OS: "plan9", Binary: "/x/caddysnake-cli"}); err == nil {
		t.Error("an install on an unsupported system was accepted")
	}

	stop, remove, purged, err := uninstallSteps("darwin", true, home, "501")
	if err != nil || quoteCommand(stop[0]) != "launchctl bootout gui/501/caddysnake.apps" || len(remove) != 1 || len(purged) != 3 {
		t.Errorf("mac user uninstall = %v %v %v %v", stop, remove, purged, err)
	}
	stop, remove, purged, _ = uninstallSteps("linux", false, home, "0")
	if quoteCommand(stop[0]) != "systemctl disable --now caddysnake-apps.service" || remove[1] != "/usr/local/lib/caddysnake" || purged[0] != "/etc/caddysnake" || purged[1] != "/var/lib/caddysnake" {
		t.Errorf("linux system uninstall = %v %v %v", stop, remove, purged)
	}
}

func TestWindowsInstallPlan(t *testing.T) {
	isolateConfig(t)
	t.Setenv("ProgramData", `D:\Data`)
	t.Setenv("ProgramFiles", `D:\Programs`)

	plan, err := buildInstallPlan(installOptions{OS: "windows", Binary: `C:\Users\ana\venv\caddysnake.exe`, UV: `C:\Users\ana\venv\uv.exe`,
		Settings: Settings{Domain: "apps.example.com", RequireLogin: true, PythonPath: `C:\Users\ana\AppData\Local\Programs\Python\python.exe`}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ConfigPath != `D:\Data\caddysnake\apps.toml` || plan.DataDir != `D:\Data\caddysnake\apps` ||
		plan.LogPath != `D:\Data\caddysnake\logs\caddysnake-apps.log` || plan.UnitPath != "" {
		t.Fatalf("paths = %q %q %q %q", plan.ConfigPath, plan.DataDir, plan.LogPath, plan.UnitPath)
	}
	// Programs are copied out of the personal folder; only the settings file is written.
	if len(plan.Copies) != 2 || plan.Copies[0].To != `D:\Programs\caddysnake\caddysnake.exe` || plan.Copies[1].To != `D:\Programs\caddysnake\uv.exe` {
		t.Fatalf("copies = %+v", plan.Copies)
	}
	if len(plan.Files) != 1 || !plan.Files[0].KeepExisting {
		t.Fatalf("files = %+v", plan.Files)
	}
	settings := plan.Files[0].Content
	for _, want := range []string{`python_path = "3.13"`, `logs\\caddysnake-apps.log`, `require_login = true`, `data_dir = `} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings are missing %q:\n%s", want, settings)
		}
	}
	// The old service is stopped before anything is replaced.
	if got := strings.Join(plan.Lenient[0], " "); got != "net.exe stop caddysnake-apps" {
		t.Errorf("first step = %q", got)
	}
	all := ""
	for _, c := range plan.After {
		all += strings.Join(c, " ") + "\n"
	}
	for _, want := range []string{
		`binPath= "D:\Programs\caddysnake\caddysnake.exe" python-apps --config "D:\Data\caddysnake\apps.toml"`,
		`obj= NT AUTHORITY\LocalService`,
		"start= delayed-auto",
		`icacls.exe D:\Data\caddysnake /inheritance:r /grant:r *S-1-5-32-544:(OI)(CI)F *S-1-5-18:(OI)(CI)F *S-1-5-19:(OI)(CI)RX`,
		`icacls.exe D:\Data\caddysnake\apps /grant`,
		"*S-1-5-19:(OI)(CI)M",
		"sc.exe failure caddysnake-apps",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("steps are missing %q:\n%s", want, all)
		}
	}
	if got := strings.Join(plan.After[len(plan.After)-1], " "); got != "sc.exe start caddysnake-apps" {
		t.Errorf("last step = %q", got)
	}

	// A Python outside personal folders is kept.
	plan, err = buildInstallPlan(installOptions{OS: "windows", Binary: `C:\x\caddysnake.exe`,
		Settings: Settings{PythonPath: `C:\Program Files\Python313\python.exe`}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Files[0].Content, `Python313`) {
		t.Errorf("an all-users Python was replaced:\n%s", plan.Files[0].Content)
	}
	// Without uv nothing can provide a Python the service can read.
	if _, err := buildInstallPlan(installOptions{OS: "windows", Binary: `C:\x\caddysnake.exe`,
		Settings: Settings{PythonPath: `C:\Users\ana\python.exe`}}); err == nil {
		t.Error("a Python in a personal folder was accepted without uv")
	}
	if _, err := buildInstallPlan(installOptions{OS: "windows", User: true, Binary: `C:\x\caddysnake.exe`}); err != errNoWindowsUserInstall {
		t.Errorf("--user on Windows: %v", err)
	}

	stop, remove, purged, err := uninstallSteps("windows", false, "", "")
	if err != nil || strings.Join(stop[0], " ") != "net.exe stop caddysnake-apps" || strings.Join(stop[1], " ") != "sc.exe delete caddysnake-apps" ||
		len(remove) != 1 || remove[0] != `D:\Programs\caddysnake` || len(purged) != 1 || purged[0] != `D:\Data\caddysnake` {
		t.Errorf("windows uninstall = %v %v %v %v", stop, remove, purged, err)
	}
}

func TestOperatingSystemDifferences(t *testing.T) {
	if exeNameFor("windows", "initdb") != "initdb.exe" || exeNameFor("windows", "uv.EXE") != "uv.EXE" || exeNameFor("linux", "initdb") != "initdb" {
		t.Error("program names")
	}
	if defaultPythonFor("windows") != "python" || defaultPythonFor("darwin") != "python3" {
		t.Error("default Python")
	}
	if got := venvPythonFor("windows", "r"); got != filepath.Join("r", ".venv", "Scripts", "python.exe") {
		t.Errorf("Windows environment Python = %q", got)
	}
	if got := venvPythonFor("linux", "r"); got != filepath.Join("r", ".venv", "bin", "python") {
		t.Errorf("environment Python = %q", got)
	}
	for path, want := range map[string]bool{
		`C:\Program Files\Python313\python.exe`: true,
		`C:\Users\ana\python.exe`:               false,
		`c:\USERS\ana\python.exe`:               false,
		`python`:                                false,
		``:                                      false,
	} {
		if usablePython("windows", path) != want {
			t.Errorf("usablePython(windows, %q) != %v", path, want)
		}
	}
	if usablePython("linux", "/home/ana/python") || !usablePython("linux", "/usr/bin/python3") {
		t.Error("usablePython on Linux")
	}
	if len(postgresBinPatterns("windows")) == 0 || !strings.Contains(postgresBinPatterns("windows")[0], "PostgreSQL") {
		t.Error("no Windows Postgres locations")
	}
}

func TestLogFileSetting(t *testing.T) {
	home := isolateConfig(t)
	logFile := filepath.Join(home, "server.log")
	s, _, err := settingsFromFlags(serverFlags(t, "--log-file", logFile, "--data-dir", filepath.Join(home, "data")))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.serverConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildCaddyConfig(sc)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging == nil || !strings.Contains(string(cfg.Logging.Logs["default"].WriterRaw), "server.log") {
		t.Fatalf("log file not configured: %+v", cfg.Logging)
	}
}
