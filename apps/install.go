package apps

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/spf13/cobra"
)

// Installing as a service that starts by itself: systemd on Linux, launchd on
// macOS, the service manager on Windows.
//
// Two kinds of install:
//
//   - system (default): starts with the machine. Needs administrator rights;
//     on Linux and macOS the command asks for them with sudo, on Windows it
//     has to be run from a terminal opened as administrator.
//   - user (--user): starts when the person logs in. No administrator rights.
//     Linux and macOS only.
//
// Where things go (see serviceLayout):
//
//	Linux system   unit /etc/systemd/system, settings /etc/caddysnake,
//	               data /var/lib/caddysnake, own account "caddysnake"
//	Linux user     unit ~/.config/systemd/user, settings ~/.config/caddysnake,
//	               data ~/.caddysnake/apps
//	macOS system   /Library/LaunchDaemons, settings /usr/local/etc/caddysnake,
//	               data /usr/local/var/caddysnake, runs as the installing person
//	macOS user     ~/Library/LaunchAgents, settings ~/.config/caddysnake,
//	               data ~/.caddysnake/apps
//	Windows        service "caddysnake-apps" running as the built-in low-rights
//	               account LocalService; settings, data and logs under
//	               %ProgramData%\caddysnake, programs in %ProgramFiles%\caddysnake
//
// Paths with spaces (such as "Application Support") are avoided on purpose.
//
// Cautions built in: nothing the person may have edited is overwritten (an
// existing settings file is kept), --dry-run shows everything first, and
// uninstall leaves settings and data in place unless asked to purge them.

const (
	serviceName     = "caddysnake-apps"
	launchdLabel    = "caddysnake.apps"
	serviceAccount  = "caddysnake"
	unixProgDir     = "/usr/local/lib/caddysnake"
	bundledBinary   = "caddysnake-cli"
	fallbackPython  = "3.13" // uv downloads this when no system Python is usable
	defaultPathDirs = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

// Windows: the service runs as LocalService, a built-in account with few
// rights. Accounts are named by their fixed identifiers, because their names
// change with the language Windows is set to.
const (
	winServiceAccount = `NT AUTHORITY\LocalService`
	winSIDAdmins      = "*S-1-5-32-544"
	winSIDSystem      = "*S-1-5-18"
	winSIDService     = "*S-1-5-19"
)

// serviceLayout is where one kind of install puts things.
type serviceLayout struct {
	UnitPath   string // "" on Windows: the service lives in the service manager
	ConfigPath string
	DataDir    string
	HomeDir    string // the service's own folder (Linux system installs)
	LogPath    string // launchd and Windows; systemd logs to the journal
	ProgDir    string // where a system install copies the programs
}

// installedBinaryFor is the server program of a system install.
func installedBinaryFor(goos string) string {
	if goos == "windows" {
		return winJoin(winProgDir(), "caddysnake.exe")
	}
	return unixProgDir + "/" + bundledBinary
}

var errNoWindowsUserInstall = errors.New("on Windows the service is installed for the whole computer: leave out --user and run this from a terminal opened as administrator")

func layoutFor(goos string, userInstall bool, home string) (serviceLayout, error) {
	switch {
	case goos == "linux" && userInstall:
		return serviceLayout{
			UnitPath:   filepath.Join(home, ".config", "systemd", "user", serviceName+".service"),
			ConfigPath: userConfigPath(),
			DataDir:    filepath.Join(home, ".caddysnake", "apps"),
		}, nil
	case goos == "linux":
		return serviceLayout{
			UnitPath:   "/etc/systemd/system/" + serviceName + ".service",
			ConfigPath: systemConfigPathFor(goos),
			DataDir:    "/var/lib/caddysnake/apps",
			HomeDir:    "/var/lib/caddysnake",
			ProgDir:    unixProgDir,
		}, nil
	case goos == "darwin" && userInstall:
		return serviceLayout{
			UnitPath:   filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
			ConfigPath: userConfigPath(),
			DataDir:    filepath.Join(home, ".caddysnake", "apps"),
			LogPath:    filepath.Join(home, "Library", "Logs", serviceName+".log"),
		}, nil
	case goos == "darwin":
		return serviceLayout{
			UnitPath:   "/Library/LaunchDaemons/" + launchdLabel + ".plist",
			ConfigPath: systemConfigPathFor(goos),
			DataDir:    "/usr/local/var/caddysnake/apps",
			LogPath:    "/usr/local/var/log/" + serviceName + ".log",
			ProgDir:    unixProgDir,
		}, nil
	case goos == "windows" && userInstall:
		return serviceLayout{}, errNoWindowsUserInstall
	case goos == "windows":
		root := winDataRoot()
		return serviceLayout{
			ConfigPath: systemConfigPathFor(goos),
			DataDir:    winJoin(root, "apps"),
			HomeDir:    root,
			LogPath:    winJoin(root, "logs", serviceName+".log"),
			ProgDir:    winProgDir(),
		}, nil
	}
	return serviceLayout{}, fmt.Errorf("installing as a service is supported on Linux, macOS and Windows, not on %s", goos)
}

// installOptions is what the person asked for.
type installOptions struct {
	OS       string // "linux" or "darwin"; "" means this machine
	User     bool
	Settings Settings
	// Programs resolved before asking for administrator rights, because sudo
	// resets PATH.
	Binary string
	UV     string
	// RunAs is the person a macOS system service runs as, and UID their id.
	RunAs string
	UID   string
}

// writeFile is one file the install creates.
type writeFile struct {
	Path    string
	Content string
	Mode    os.FileMode
	// KeepExisting leaves a file that is already there untouched.
	KeepExisting bool
	// Owner is "account:group" (or "account") to hand the file to, "" to leave it.
	Owner string
}

// copyFile is one program the install copies into place.
type copyFile struct{ From, To string }

// installPlan is everything an install does, in order. Building it changes
// nothing, so it can be shown (--dry-run) and tested.
type installPlan struct {
	User       bool
	UnitPath   string
	ConfigPath string
	DataDir    string
	LogPath    string
	// Lenient commands run first and may fail without stopping the install
	// (stopping or unloading a service that is not there yet). An old service
	// is stopped before anything is replaced: Windows does not let a running
	// program be overwritten.
	Lenient [][]string
	// WaitGone is polled after the lenient commands until it fails, meaning
	// the old service has finished stopping. launchd unloads in the
	// background, and loading the new definition too early is refused.
	WaitGone []string
	Dirs     []string   // folders created if missing
	Before   [][]string // commands run next (account, folders)
	Copies   []copyFile
	Files    []writeFile
	After    [][]string // commands run last (reload, enable, start)
	Status   string     // how to check on the service
	Logs     string     // how to read its output
	Notes    []string
}

func systemctl(user bool, args ...string) []string {
	cmd := []string{"systemctl"}
	if user {
		cmd = append(cmd, "--user")
	}
	return append(cmd, args...)
}

// launchdDomain is where launchd keeps the service: the machine, or one
// person's login session.
func launchdDomain(user bool, uid string) string {
	if user {
		return "gui/" + uid
	}
	return "system"
}

// usablePython reports whether a Python path can be used by a service that
// cannot read home folders: an absolute path outside them.
func usablePython(goos, path string) bool {
	if goos == "windows" {
		// A drive path such as C:\Python313\python.exe, outside C:\Users.
		if len(path) < 3 || path[1] != ':' {
			return false
		}
		return !strings.Contains(strings.ToLower(path), `\users\`)
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for _, private := range []string{"/home/", "/root/", "/Users/"} {
		if strings.HasPrefix(path, private) {
			return false
		}
	}
	return true
}

// winServiceCommand is the command line Windows runs for the service.
func winServiceCommand(binary, configPath string) string {
	return `"` + binary + `" python-apps --config "` + configPath + `"`
}

func systemdUnit(user bool, binary, configPath, home string, pathDirs []string, writable []string) string {
	var b strings.Builder
	b.WriteString("# Written by `caddysnake apps install`. Change settings in " + configPath + ",\n")
	b.WriteString("# then restart the service. Reinstalling rewrites this file.\n")
	b.WriteString("[Unit]\nDescription=caddy-snake apps\nDocumentation=https://caddy-snake.readthedocs.io/en/latest/docs/apps\n")
	b.WriteString("After=network-online.target\nWants=network-online.target\n\n")
	b.WriteString("[Service]\nType=exec\n")
	if !user {
		b.WriteString("User=" + serviceAccount + "\nGroup=" + serviceAccount + "\n")
	}
	fmt.Fprintf(&b, "ExecStart=%s python-apps --config %s\n", binary, configPath)
	fmt.Fprintf(&b, "Environment=PATH=%s\n", strings.Join(append(pathDirs, defaultPathDirs), ":"))
	if !user {
		fmt.Fprintf(&b, "Environment=HOME=%s\n", home)
	}
	// SIGINT is the clean stop: it shuts the built-in database down too.
	b.WriteString("KillSignal=SIGINT\nTimeoutStopSec=90\nRestart=on-failure\nRestartSec=5\nLimitNOFILE=1048576\n")
	if !user {
		b.WriteString("\n# Allow ports 80 and 443 without running as root.\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n")
		b.WriteString("\n# Confine the service to its own folders.\nNoNewPrivileges=true\nProtectSystem=strict\nProtectHome=true\nPrivateTmp=true\n")
		b.WriteString("ProtectKernelTunables=true\nProtectKernelModules=true\nProtectControlGroups=true\nRestrictSUIDSGID=true\n")
		b.WriteString("ReadWritePaths=" + strings.Join(writable, " ") + "\n")
	}
	target := "multi-user.target"
	if user {
		target = "default.target"
	}
	b.WriteString("\n[Install]\nWantedBy=" + target + "\n")
	return b.String()
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// launchdPlist is the macOS equivalent of the systemd unit.
func launchdPlist(binary, configPath, logPath, runAs string, pathDirs []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<!-- Written by `caddysnake apps install`. Change settings in " + xmlText(configPath) + ", then restart the service. Reinstalling rewrites this file. -->\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	key := func(name, value string) { fmt.Fprintf(&b, "\t<key>%s</key>\n\t%s\n", name, value) }
	str := func(s string) string { return "<string>" + xmlText(s) + "</string>" }
	key("Label", str(launchdLabel))
	key("ProgramArguments", "<array>\n\t\t"+strings.Join([]string{str(binary), str("python-apps"), str("--config"), str(configPath)}, "\n\t\t")+"\n\t</array>")
	// Homebrew's folders hold Postgres and Python on most Macs.
	path := strings.Join(append(pathDirs, "/opt/homebrew/bin", defaultPathDirs), ":")
	key("EnvironmentVariables", "<dict>\n\t\t<key>PATH</key>\n\t\t"+str(path)+"\n\t</dict>")
	if runAs != "" {
		key("UserName", str(runAs))
	}
	key("RunAtLoad", "<true/>")
	// Restart if it stops by itself with an error, not when it is stopped on purpose.
	key("KeepAlive", "<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>")
	key("ThrottleInterval", "<integer>5</integer>")
	key("ExitTimeOut", "<integer>90</integer>")
	key("ProcessType", str("Background"))
	key("StandardOutPath", str(logPath))
	key("StandardErrorPath", str(logPath))
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// buildInstallPlan works out what an install would do. It changes nothing.
func buildInstallPlan(opts installOptions) (*installPlan, error) {
	s := opts.Settings
	if opts.Binary == "" {
		return nil, errors.New("cannot find the caddysnake program to install")
	}
	goos := opts.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	home, err := os.UserHomeDir()
	if err != nil && opts.User {
		return nil, err
	}
	layout, err := layoutFor(goos, opts.User, home)
	if err != nil {
		return nil, err
	}
	// Refuse to install something the server itself would refuse to start.
	if !s.RequireLogin && !s.OpenToNetwork && (s.Domain != "" || (s.Listen != "" && !isLoopbackListen(s.Listen))) {
		return nil, errOpenToEveryone
	}
	if s.DataDir == "" {
		s.DataDir = layout.DataDir
	}
	plan := &installPlan{User: opts.User, UnitPath: layout.UnitPath, ConfigPath: layout.ConfigPath, DataDir: s.DataDir, LogPath: layout.LogPath}
	binary := opts.Binary
	var pathDirs []string

	if opts.User {
		if opts.UV != "" {
			pathDirs = append(pathDirs, filepath.Dir(opts.UV))
		}
	} else {
		// A system service must not depend on files in somebody's home folder.
		binary = installedBinaryFor(goos)
		plan.Copies = append(plan.Copies, copyFile{From: opts.Binary, To: binary})
		if opts.UV != "" {
			uv := layout.ProgDir + "/uv"
			if goos == "windows" {
				uv = winJoin(layout.ProgDir, "uv.exe")
			}
			plan.Copies = append(plan.Copies, copyFile{From: opts.UV, To: uv})
		} else if goos != "windows" || usablePython(goos, s.PythonPath) {
			plan.Notes = append(plan.Notes, "uv was not found, so apps' packages will be installed with pip, which is slower.")
		}
		pathDirs = append(pathDirs, layout.ProgDir)
	}

	configFile := writeFile{Path: layout.ConfigPath, Mode: 0o600, KeepExisting: true}
	unit := writeFile{Path: layout.UnitPath, Mode: 0o644}

	switch goos {
	case "linux":
		writable := []string{layout.HomeDir}
		if !opts.User {
			if !usablePython(goos, s.PythonPath) {
				switch {
				case opts.UV == "":
					// Without uv nothing can download a Python, so use the system one.
					s.PythonPath = "/usr/bin/python3"
				default:
					if s.PythonPath != "" {
						plan.Notes = append(plan.Notes, fmt.Sprintf(
							"Python at %s is inside a home folder the service cannot read, so Python %s will be used (uv downloads it on first use).", s.PythonPath, fallbackPython))
					}
					s.PythonPath = fallbackPython
				}
			}
			plan.Before = [][]string{
				{"sh", "-c", fmt.Sprintf("id -u %[1]s >/dev/null 2>&1 || useradd --system --user-group --home-dir %[2]s --shell /usr/sbin/nologin %[1]s", serviceAccount, layout.HomeDir)},
				{"install", "-d", "-m", "0755", filepath.Dir(layout.ConfigPath), layout.ProgDir},
				{"install", "-d", "-m", "0750", "-o", serviceAccount, "-g", serviceAccount, layout.HomeDir, s.DataDir},
			}
			// The settings may hold a database password: readable by root and the service only.
			configFile.Mode = 0o640
			configFile.Owner = "root:" + serviceAccount
			if !strings.HasPrefix(s.DataDir, layout.HomeDir+"/") && s.DataDir != layout.HomeDir {
				writable = append(writable, s.DataDir)
			}
		} else {
			plan.Notes = append(plan.Notes,
				"The service runs while you are logged in. To keep it running after you log out: sudo loginctl enable-linger "+os.Getenv("USER"))
		}
		unit.Content = systemdUnit(opts.User, binary, layout.ConfigPath, layout.HomeDir, pathDirs, writable)
		plan.After = [][]string{
			systemctl(opts.User, "daemon-reload"),
			systemctl(opts.User, "enable", serviceName+".service"),
			systemctl(opts.User, "restart", serviceName+".service"),
		}
		plan.Status = quoteCommand(systemctl(opts.User, "status", serviceName))
		plan.Logs = "journalctl " + map[bool]string{true: "--user ", false: ""}[opts.User] + "-u " + serviceName + " -f"

	case "darwin":
		runAs := ""
		if !opts.User {
			if opts.RunAs == "" || opts.RunAs == "root" {
				return nil, errors.New("run the install as yourself (not as root): on macOS the service runs as the person who installs it")
			}
			runAs = opts.RunAs
			plan.Before = [][]string{
				{"install", "-d", "-m", "0755", filepath.Dir(layout.ConfigPath), layout.ProgDir, filepath.Dir(layout.LogPath)},
				{"install", "-d", "-m", "0750", "-o", runAs, s.DataDir},
				{"install", "-m", "0640", "-o", runAs, "/dev/null", layout.LogPath},
			}
			configFile.Owner = runAs
			unit.Owner = "root:wheel"
			plan.Notes = append(plan.Notes, "The service starts with the Mac and runs as "+runAs+".")
		} else {
			plan.Notes = append(plan.Notes, "The service starts when you log in.")
		}
		unit.Content = launchdPlist(binary, layout.ConfigPath, layout.LogPath, runAs, pathDirs)
		domain := launchdDomain(opts.User, opts.UID)
		// Reinstalling: unload the old definition first. Fails harmlessly on a first install.
		plan.Lenient = [][]string{{"launchctl", "bootout", domain + "/" + launchdLabel}}
		plan.WaitGone = []string{"launchctl", "print", domain + "/" + launchdLabel}
		plan.After = [][]string{
			{"launchctl", "bootstrap", domain, layout.UnitPath},
			{"launchctl", "enable", domain + "/" + launchdLabel},
		}
		plan.Status = "launchctl print " + domain + "/" + launchdLabel
		plan.Logs = "tail -f " + layout.LogPath

	case "windows":
		// The service cannot read personal folders, where Python usually is.
		if !usablePython(goos, s.PythonPath) {
			if opts.UV == "" {
				return nil, errors.New("the service cannot use a Python inside a personal folder. Install uv (it downloads a Python for the service), or install Python for all users and pass --python-path")
			}
			if s.PythonPath != "" {
				plan.Notes = append(plan.Notes, fmt.Sprintf(
					"Python at %s is inside a personal folder the service cannot read, so Python %s will be used (uv downloads it on first use).", s.PythonPath, fallbackPython))
			}
			s.PythonPath = fallbackPython
		}
		if s.LogFile == "" {
			s.LogFile = layout.LogPath
		}
		plan.LogPath = s.LogFile
		logDir := s.LogFile[:strings.LastIndexAny(s.LogFile, `\/`)+1]
		logDir = strings.TrimRight(logDir, `\/`)
		plan.Dirs = []string{layout.HomeDir, layout.ProgDir, s.DataDir, logDir}
		full, change, read := ":(OI)(CI)F", ":(OI)(CI)M", ":(OI)(CI)RX"
		create := []string{"sc.exe", "create", serviceName, "binPath=", winServiceCommand(binary, layout.ConfigPath)}
		// Stop a service that is already there, and make sure one exists. Both
		// fail harmlessly: on a first install, and when installing again.
		plan.Lenient = [][]string{{"net.exe", "stop", serviceName}, create}
		plan.After = [][]string{
			// Settings may hold a database password: administrators and the
			// service only. The service may change its data and logs, nothing else.
			{"icacls.exe", layout.HomeDir, "/inheritance:r", "/grant:r", winSIDAdmins + full, winSIDSystem + full, winSIDService + read},
			{"icacls.exe", s.DataDir, "/grant", winSIDAdmins + full, winSIDSystem + full, winSIDService + change},
			{"icacls.exe", logDir, "/grant", winSIDAdmins + full, winSIDSystem + full, winSIDService + change},
			{"sc.exe", "config", serviceName, "binPath=", winServiceCommand(binary, layout.ConfigPath),
				"start=", "delayed-auto", "obj=", winServiceAccount, "DisplayName=", "caddy-snake apps"},
			{"sc.exe", "description", serviceName, "Small internal apps, built and shared with an AI assistant."},
			// Start it again if it stops by itself.
			{"sc.exe", "failure", serviceName, "reset=", "86400", "actions=", "restart/5000/restart/5000/restart/60000"},
			{"sc.exe", "start", serviceName},
		}
		plan.Status = "sc.exe query " + serviceName
		plan.Logs = `powershell -c "Get-Content -Wait '` + s.LogFile + `'"`
		plan.Notes = append(plan.Notes, "The service starts with Windows and runs as the built-in account LocalService, which has few rights.")
	}

	config, err := encodeSettings(s)
	if err != nil {
		return nil, err
	}
	configFile.Content = config
	plan.Files = []writeFile{configFile}
	if unit.Path != "" {
		plan.Files = append(plan.Files, unit)
	}
	if s.RequireLogin {
		who := "sudo caddysnake apps"
		switch {
		case goos == "windows":
			who = "(in a terminal opened as administrator) caddysnake apps"
		case opts.User || goos == "darwin":
			who = "caddysnake apps"
		}
		plan.Notes = append(plan.Notes, "Sign-in is on. Add the first person with: "+who+" user add <name> --admin")
	}
	return plan, nil
}

func quoteCommand(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n'\"|&;<>()$`\\*?") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// describe prints the plan without doing anything.
func (p *installPlan) describe(w io.Writer) {
	fmt.Fprintln(w, "This would run and write the following. Nothing has been changed.")
	for _, c := range p.Lenient {
		fmt.Fprintln(w, "\n$ "+quoteCommand(c))
	}
	for _, d := range p.Dirs {
		fmt.Fprintln(w, "\ncreate folder "+d)
	}
	for _, c := range p.Before {
		fmt.Fprintln(w, "\n$ "+quoteCommand(c))
	}
	for _, c := range p.Copies {
		fmt.Fprintf(w, "\ncopy %s -> %s\n", c.From, c.To)
	}
	for _, f := range p.Files {
		kept := ""
		if f.KeepExisting {
			kept = " (kept as it is if it already exists)"
		}
		fmt.Fprintf(w, "\n--- %s%s\n%s", f.Path, kept, f.Content)
	}
	for _, c := range p.After {
		fmt.Fprintln(w, "\n$ "+quoteCommand(c))
	}
	for _, n := range p.Notes {
		fmt.Fprintln(w, "\nNote: "+n)
	}
}

func runCommand(args []string) error {
	// What the command prints is only shown when it fails: the Windows tools
	// report every success at length.
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", quoteCommand(args), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runQuietly(args []string) {
	_ = exec.Command(args[0], args[1:]...).Run()
}

// stopWait is how long a stopping service gets: its own stop timeout plus a margin.
const stopWait = 110 * time.Second

// waitUntilFails runs a command repeatedly until it exits with an error, and
// reports whether that happened within the time allowed.
func waitUntilFails(args []string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		if exec.Command(args[0], args[1:]...).Run() != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func copyProgram(from, to string) error {
	if from == to {
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	// Write beside the target and rename, so a running service never sees a
	// half-written program.
	tmp := to + ".new"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, to)
}

// apply carries the plan out.
func (p *installPlan) apply(w io.Writer) error {
	for _, c := range p.Lenient {
		runQuietly(c)
	}
	if len(p.WaitGone) > 0 && !waitUntilFails(p.WaitGone, stopWait) {
		return errors.New("the previous service did not stop in time; try again in a moment")
	}
	for _, d := range p.Dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, c := range p.Before {
		if err := runCommand(c); err != nil {
			return err
		}
	}
	for _, c := range p.Copies {
		if err := copyProgram(c.From, c.To); err != nil {
			return fmt.Errorf("copying %s: %w", c.From, err)
		}
	}
	for _, f := range p.Files {
		if f.KeepExisting {
			if _, err := os.Stat(f.Path); err == nil {
				fmt.Fprintf(w, "Kept your existing settings in %s.\n", f.Path)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.Path, []byte(f.Content), f.Mode); err != nil {
			return err
		}
		if err := os.Chmod(f.Path, f.Mode); err != nil {
			return err
		}
		if f.Owner != "" {
			if err := runCommand([]string{"chown", f.Owner, f.Path}); err != nil {
				return err
			}
		}
	}
	if p.User {
		if err := os.MkdirAll(p.DataDir, 0o750); err != nil {
			return err
		}
		if p.LogPath != "" {
			if err := os.MkdirAll(filepath.Dir(p.LogPath), 0o755); err != nil {
				return err
			}
		}
	}
	for _, c := range p.After {
		if err := runCommand(c); err != nil {
			return err
		}
	}
	return nil
}

// requireServiceManager checks that this machine can run the service.
func requireServiceManager() error {
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			return errors.New("systemctl was not found: this machine does not use systemd")
		}
		return nil
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err != nil {
			return errors.New("launchctl was not found")
		}
		return nil
	case "windows":
		if _, err := exec.LookPath("sc.exe"); err != nil {
			return errors.New("sc.exe was not found")
		}
		return nil
	}
	return fmt.Errorf("installing as a service is supported on Linux, macOS and Windows, not on %s (use --dry-run to see what it would do)", runtime.GOOS)
}

// elevate re-runs this command with administrator rights. sudo asks for the
// password on the terminal if it needs one.
func elevate(extra ...string) error {
	if runtime.GOOS == "windows" {
		return errors.New("administrator rights are needed: open a terminal with \"Run as administrator\" and run this command again")
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("administrator rights are needed: run this command as root")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Administrator rights are needed for a system service. You may be asked for your password.")
	args := append([]string{self}, os.Args[1:]...)
	args = append(args, extra...)
	cmd := exec.Command(sudo, args...) // #nosec G702 -- this same command with the same arguments, run again through sudo
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func lookProgram(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// installer is the person installing: with sudo, the one who ran it.
func installer() (name, uid string) {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil {
			return u.Username, u.Uid
		}
	}
	if u, err := user.Current(); err == nil {
		return u.Username, u.Uid
	}
	return "", strconv.Itoa(os.Getuid())
}

func installCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Run the server as a service that starts by itself",
		Long: `Installs caddy-snake apps as a service: systemd on Linux, launchd on macOS,
the service manager on Windows.

By default it is a system service that starts with the machine. This needs
administrator rights: on Linux and macOS you are asked for your password if
needed, on Windows run it from a terminal opened as administrator.

  Linux    runs as its own account; settings in /etc/caddysnake/apps.toml,
           data in /var/lib/caddysnake/apps
  macOS    runs as you; settings in /usr/local/etc/caddysnake/apps.toml,
           data in /usr/local/var/caddysnake/apps
  Windows  runs as LocalService; settings in
           %ProgramData%\caddysnake\apps.toml, data in
           %ProgramData%\caddysnake\apps

With --user (Linux and macOS) it is your own service and starts when you log
in: settings in ~/.config/caddysnake/apps.toml, data in ~/.caddysnake/apps, no
administrator rights.

The server's own flags (--listen, --domain, --require-login, ...) are written
to the settings file. An existing settings file is never overwritten.
Use --dry-run to see everything first.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fs := caddycmd.Flags{FlagSet: cmd.Flags()}
			userInstall, _ := cmd.Flags().GetBool("user")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			opts := installOptions{User: userInstall}
			opts.RunAs, opts.UID = installer()
			text := map[string]*string{
				"listen": &opts.Settings.Listen, "domain": &opts.Settings.Domain, "public-url": &opts.Settings.PublicURL,
				"data-dir": &opts.Settings.DataDir, "database-url": &opts.Settings.DatabaseURL, "python-path": &opts.Settings.PythonPath,
			}
			for name, target := range text {
				*target = fs.String(name)
			}
			opts.Settings.RequireLogin, _ = cmd.Flags().GetBool("require-login")
			opts.Settings.OpenToNetwork, _ = cmd.Flags().GetBool("open-to-network")

			// Resolve programs now: sudo resets PATH, so the elevated run is told
			// where they are.
			opts.UV, _ = cmd.Flags().GetString("uv-path")
			if opts.UV == "" {
				opts.UV = lookProgram("uv")
			}
			if opts.Settings.PythonPath == "" {
				opts.Settings.PythonPath = lookProgram(defaultPythonFor(runtime.GOOS))
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			opts.Binary = self

			plan, err := buildInstallPlan(opts)
			if err != nil {
				return err
			}
			if dryRun {
				plan.describe(os.Stdout)
				return nil
			}
			if err := requireServiceManager(); err != nil {
				return err
			}
			if !userInstall && !isAdmin() {
				var extra []string
				if !cmd.Flags().Changed("uv-path") && opts.UV != "" {
					extra = append(extra, "--uv-path", opts.UV)
				}
				if !cmd.Flags().Changed("python-path") && opts.Settings.PythonPath != "" {
					extra = append(extra, "--python-path", opts.Settings.PythonPath)
				}
				return elevate(extra...)
			}
			if opts.Settings.DatabaseURL == "" && findPostgresBin() == "" {
				return errors.New(installHint)
			}
			if err := plan.apply(os.Stdout); err != nil {
				return err
			}
			fmt.Printf("\nInstalled and started.\n  Settings:  %s\n  Data:      %s\n  Status:    %s\n  Logs:      %s\n",
				plan.ConfigPath, plan.DataDir, plan.Status, plan.Logs)
			for _, n := range plan.Notes {
				fmt.Println("\nNote: " + n)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.Bool("user", false, "Install for your own account, without administrator rights")
	f.Bool("dry-run", false, "Show what would be done and written, and change nothing")
	f.String("listen", "", "Address to listen on (default "+defaultListen+")")
	f.String("domain", "", "Domain name to serve with automatic HTTPS")
	f.String("public-url", "", "The address people use to reach the server")
	f.String("python-path", "", "Python used to build each app's environment")
	f.Bool("require-login", false, "Ask everyone to sign in")
	f.Bool("open-to-network", false, "Allow listening beyond this computer without sign-in")
	f.String("uv-path", "", "Where the uv program is (found automatically)")
	return cmd
}

// uninstallSteps is what removing the service involves on this kind of machine.
func uninstallSteps(goos string, userInstall bool, home, uid string) (stop [][]string, remove, purged []string, err error) {
	layout, err := layoutFor(goos, userInstall, home)
	if err != nil {
		return nil, nil, nil, err
	}
	if layout.UnitPath != "" {
		remove = []string{layout.UnitPath}
	}
	purged = []string{layout.ConfigPath, layout.DataDir}
	if !userInstall {
		remove = append(remove, layout.ProgDir)
		purged = []string{filepath.Dir(layout.ConfigPath), filepath.Dir(layout.DataDir)}
	}
	switch goos {
	case "windows":
		// net stop returns once the service has stopped.
		stop = [][]string{{"net.exe", "stop", serviceName}, {"sc.exe", "delete", serviceName}}
		purged = []string{layout.HomeDir}
	case "linux":
		stop = [][]string{systemctl(userInstall, "disable", "--now", serviceName+".service")}
	case "darwin":
		stop = [][]string{{"launchctl", "bootout", launchdDomain(userInstall, uid) + "/" + launchdLabel}}
		if layout.LogPath != "" {
			purged = append(purged, layout.LogPath)
		}
	}
	return stop, remove, purged, nil
}

func uninstallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the service; settings and data are kept",
		Long: `Stops the service and removes it from systemd, launchd or Windows.

Settings and data are left in place, so installing again brings everything
back. Add --purge to remove them too: every app and all its data is deleted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			userInstall, _ := cmd.Flags().GetBool("user")
			purge, _ := cmd.Flags().GetBool("purge")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			home, _ := os.UserHomeDir()
			_, uid := installer()
			stop, remove, purged, err := uninstallSteps(runtime.GOOS, userInstall, home, uid)
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Println("This would do the following. Nothing has been changed.")
				for _, c := range stop {
					fmt.Println("$ " + quoteCommand(c))
				}
				for _, p := range remove {
					fmt.Println("remove " + p)
				}
				if purge {
					for _, p := range purged {
						fmt.Println("remove " + p + "   (settings and all app data)")
					}
				}
				return nil
			}
			if err := requireServiceManager(); err != nil {
				return err
			}
			if !userInstall && !isAdmin() {
				return elevate()
			}
			for _, c := range stop {
				// The service may already be stopped or gone; keep going.
				runQuietly(c)
			}
			if runtime.GOOS == "darwin" {
				// launchd stops it in the background; wait so the data is not removed under it.
				waitUntilFails([]string{"launchctl", "print", launchdDomain(userInstall, uid) + "/" + launchdLabel}, stopWait)
			}
			for _, p := range remove {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
			}
			if runtime.GOOS == "linux" {
				runQuietly(systemctl(userInstall, "daemon-reload"))
			}
			if purge {
				for _, p := range purged {
					if err := os.RemoveAll(p); err != nil {
						return err
					}
				}
				fmt.Println("Removed the service, its settings and all app data.")
				return nil
			}
			fmt.Printf("Removed the service. Settings and data were kept:\n  %s\n", strings.Join(purged, "\n  "))
			return nil
		},
	}
	cmd.Flags().Bool("user", false, "Remove the service installed with --user")
	cmd.Flags().Bool("purge", false, "Also remove settings and all app data")
	cmd.Flags().Bool("dry-run", false, "Show what would be removed, and change nothing")
	return cmd
}
