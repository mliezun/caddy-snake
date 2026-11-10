package apps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// What differs between operating systems, in one place. The functions that
// take goos are pure, so the Windows behaviour is tested on any machine.

// exeName is the file name of a program on this machine.
func exeName(name string) string {
	return exeNameFor(runtime.GOOS, name)
}

func exeNameFor(goos, name string) string {
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

// defaultPythonFor is the Python looked up when none is configured. On Windows
// "python3" is usually the Microsoft Store placeholder, not a real Python.
func defaultPythonFor(goos string) string {
	if goos == "windows" {
		return "python"
	}
	return "python3"
}

// venvPythonFor is the interpreter inside an app's environment.
func venvPythonFor(goos, release string) string {
	if goos == "windows" {
		return filepath.Join(release, ".venv", "Scripts", "python.exe")
	}
	return filepath.Join(release, ".venv", "bin", "python")
}

// winJoin joins path parts with backslashes whatever machine this runs on.
func winJoin(parts ...string) string {
	return strings.Join(parts, `\`)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Windows folders: settings, data and logs are kept under ProgramData, the
// programs under Program Files.
func winDataRoot() string { return winJoin(envOr("ProgramData", `C:\ProgramData`), configDirName) }
func winProgDir() string  { return winJoin(envOr("ProgramFiles", `C:\Program Files`), configDirName) }

// postgresBinPatterns lists where the Postgres programs are usually installed.
func postgresBinPatterns(goos string) []string {
	if goos == "windows" {
		var out []string
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), `C:\Program Files`} {
			if root != "" {
				out = append(out, filepath.Join(root, "PostgreSQL", "*", "bin"))
			}
		}
		return out
	}
	return []string{
		"/usr/lib/postgresql/*/bin",
		"/usr/pgsql-*/bin",
		"/usr/local/pgsql/bin",
		"/opt/homebrew/opt/postgresql*/bin",
		"/usr/local/opt/postgresql*/bin",
		"/Applications/Postgres.app/Contents/Versions/latest/bin",
	}
}

// besideExecutable returns the path of a program that sits in the same folder
// as this one, or "" if there is none. An installed service keeps uv there.
func besideExecutable(name string) string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(self), exeName(name))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}

// switchCurrent points <appDir>/current at the new release.
//
// On Linux and macOS it is a symbolic link replaced in one step. Windows only
// lets administrators create symbolic links, so there it is a junction (a
// folder link anyone may create); a junction cannot be replaced in one step,
// so the old one is moved aside first, which takes a moment in which only
// apps that are not loaded yet can notice.
func switchCurrent(appDir, release string) error {
	current := filepath.Join(appDir, "current")
	tmp := filepath.Join(appDir, "current.new")
	if runtime.GOOS != "windows" {
		rel, err := filepath.Rel(appDir, release)
		if err != nil {
			return err
		}
		_ = os.Remove(tmp)
		if err := os.Symlink(rel, tmp); err != nil {
			return err
		}
		return os.Rename(tmp, current)
	}
	old := filepath.Join(appDir, "current.old")
	_ = os.Remove(tmp)
	_ = os.Remove(old)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", tmp, release).CombinedOutput(); err != nil {
		return fmt.Errorf("linking the new version: %w: %s", err, strings.TrimSpace(string(out)))
	}
	hadOld := false
	if _, err := os.Lstat(current); err == nil {
		if err := os.Rename(current, old); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("replacing the running version: %w", err)
		}
		hadOld = true
	}
	if err := os.Rename(tmp, current); err != nil {
		if hadOld {
			// Put the running version back: a failed publish changes nothing.
			_ = os.Rename(old, current)
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("switching to the new version: %w", err)
	}
	// Removes the link only, never the release folder it pointed at.
	_ = os.Remove(old)
	return nil
}

// removeWait is how long removeAll keeps trying: a little longer than an app
// that is being unloaded gets to finish its requests.
var removeWait = 45 * time.Second

// removeAll deletes a folder and everything in it. Windows refuses to delete
// a file that is still open: by an app that is just being unloaded, or by a
// virus scanner looking at a file that was written a moment ago. Those let go
// by themselves, so the removal is tried again for a while before giving up.
// On Linux and macOS the first try succeeds.
func removeAll(path string) error {
	deadline := time.Now().Add(removeWait)
	for {
		err := os.RemoveAll(path)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}
