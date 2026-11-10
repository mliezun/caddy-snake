package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	caddysnake "github.com/mliezun/caddy-snake"
	"github.com/mliezun/caddy-snake/apps/db"
	"github.com/mliezun/caddy-snake/apps/db/appdb/appdbgen"
)

const (
	installTimeout     = 10 * time.Minute
	importCheckTimeout = 90 * time.Second
	maxDetailBytes     = 6000
)

// PublishResult describes what happened when a version was put live. A failed
// publish leaves the previously published version running.
type PublishResult struct {
	OK      bool     `json:"ok"`
	Slug    string   `json:"app"`
	Version string   `json:"version,omitempty"`
	URL     string   `json:"url,omitempty"`
	Steps   []string `json:"steps"`
	Error   string   `json:"error,omitempty"`
}

func (r *PublishResult) step(format string, args ...any) {
	r.Steps = append(r.Steps, fmt.Sprintf(format, args...))
}

func tail(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return "…" + s[len(s)-max:]
	}
	return s
}

// Publish puts a saved version of an app or draft live. version "" means the
// latest saved one.
func (p *Platform) Publish(ctx context.Context, slug, version, by string) (*PublishResult, error) {
	a, err := p.requireApp(ctx, slug)
	if err != nil {
		return nil, err
	}
	app, _ := SplitSlug(slug)
	defer p.lock(app)()
	return p.publishLocked(ctx, a, version, by), nil
}

// publishLocked runs the whole publish. The caller holds the app lock.
func (p *Platform) publishLocked(ctx context.Context, a *App, version, by string) *PublishResult {
	res := &PublishResult{Slug: a.Slug}
	err := p.doPublish(ctx, a, version, res)
	if err != nil {
		res.Error = err.Error()
		res.step("Stopped: nothing was changed for people using the app.")
		p.logs.Add(a.Slug, time.Now(), "caddy-snake", "publish failed: "+firstLine(err.Error()))
	} else {
		res.OK = true
		res.URL = p.URL(a.Slug)
		p.logs.Add(a.Slug, time.Now(), "caddy-snake", "published version "+res.Version)
	}
	p.store.RecordPublish(ctx, a.Slug, res.Version, by, res.OK, tail(res.Error, maxDetailBytes))
	p.Touch(a.Slug)
	return res
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (p *Platform) doPublish(ctx context.Context, a *App, version string, res *PublishResult) error {
	app, draft := SplitSlug(a.Slug)
	r := p.repo(app)
	if version == "" {
		head, err := r.Head(branchFor(draft))
		if err != nil {
			return err
		}
		if head == "" {
			return errors.New("there is no saved version to publish yet; save some files first")
		}
		version = head
	}
	res.Version = version

	releases := filepath.Join(p.appDir(a.Slug), "releases")
	if err := os.MkdirAll(releases, 0o750); err != nil {
		return err
	}
	// A fresh folder per publish: the running version is never modified in place.
	release := filepath.Join(releases, fmt.Sprintf("%s-%d", version, time.Now().UnixNano()))
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(release)
		}
	}()
	if err := os.MkdirAll(release, 0o750); err != nil {
		return err
	}
	if err := r.Export(version, release); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(release, "main.py")); err != nil {
		return errors.New("the app needs a file called main.py that defines a variable called `app`")
	}
	res.step("Copied the files of version %s.", version)

	source, out, err := p.buildEnvironment(ctx, release)
	if err != nil {
		return fmt.Errorf("installing the app's packages failed:\n%s", tail(out, maxDetailBytes))
	}
	if source != "" {
		res.step("Installed the packages listed in %s.", source)
	} else {
		res.step("Set up Python (the app lists no packages).")
	}

	env, err := p.appEnv(ctx, a)
	if err != nil {
		return err
	}
	if out, err := p.checkImport(ctx, release, env); err != nil {
		return fmt.Errorf("the app does not start:\n%s", tail(out, maxDetailBytes))
	}
	res.step("Checked that the app starts.")

	applied, err := applyAppMigrations(ctx, a.DatabaseURL, filepath.Join(release, "migrations"))
	if err != nil {
		return fmt.Errorf("a database change failed (earlier ones in this publish were kept): %w", err)
	}
	if len(applied) > 0 {
		res.step("Applied database changes: %s.", strings.Join(applied, ", "))
	}

	if err := switchCurrent(p.appDir(a.Slug), release); err != nil {
		return err
	}
	ok = true
	caddysnake.ReloadWorkingDir(p.appDir(a.Slug))
	if err := p.store.SetLive(ctx, a.Slug, version); err != nil {
		return err
	}
	a.LiveVersion = version
	p.setKnown(a.Slug, true)
	res.step("Switched the app to version %s.", version)
	p.pruneReleases(releases, release)
	return nil
}

// uvCommand returns how to invoke uv, or nil when it is not available.
func (p *Platform) uvCommand(ctx context.Context) []string {
	if path, err := exec.LookPath("uv"); err == nil {
		return []string{path}
	}
	if path := besideExecutable("uv"); path != "" {
		return []string{path}
	}
	if _, err := runTool(ctx, nil, p.cfg.PythonBin, "-m", "uv", "--version"); err == nil {
		return []string{p.cfg.PythonBin, "-m", "uv"}
	}
	return nil
}

func venvPython(release string) string {
	return venvPythonFor(runtime.GOOS, release)
}

// packageSource says where an app lists its packages: "pyproject.toml" when
// that file describes a project, else "requirements.txt" when there is one,
// else "". A pyproject.toml that only configures tools is not a package list.
func packageSource(release string) (string, error) {
	path := filepath.Join(release, "pyproject.toml")
	if _, err := os.Stat(path); err == nil {
		var py struct {
			Project map[string]any `toml:"project"`
		}
		if _, err := toml.DecodeFile(path, &py); err != nil {
			return "", fmt.Errorf("pyproject.toml cannot be read: %w", err)
		}
		if py.Project != nil {
			return "pyproject.toml", nil
		}
	}
	if _, err := os.Stat(filepath.Join(release, "requirements.txt")); err == nil {
		return "requirements.txt", nil
	}
	return "", nil
}

// listDependenciesScript prints the dependencies of pyproject.toml, one per
// line, for installs without uv.
const listDependenciesScript = `import tomllib
with open("pyproject.toml", "rb") as f:
    print("\n".join(tomllib.load(f).get("project", {}).get("dependencies", [])))
`

// buildEnvironment creates <release>/.venv with the app's packages and
// returns the file they were listed in ("" when the app lists none).
//
// Packages come from pyproject.toml ([project] dependencies; a uv.lock next to
// it is followed exactly) or from requirements.txt. With both present,
// pyproject.toml is used.
func (p *Platform) buildEnvironment(ctx context.Context, release string) (source, output string, err error) {
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	source, err = packageSource(release)
	if err != nil {
		return "", err.Error(), err
	}
	venv := filepath.Join(release, ".venv")
	requirements := filepath.Join(release, "requirements.txt")

	run := func(name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = release
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if uv := p.uvCommand(ctx); uv != nil {
		// uv shares one copy of each package file between environments.
		// Windows will not delete a file a running program has loaded,
		// through any of its names, so a shared file would stop one app
		// from being removed while another is running. Copy there.
		var linkMode []string
		if runtime.GOOS == "windows" {
			linkMode = []string{"--link-mode", "copy"}
		}
		if source == "pyproject.toml" {
			// The app is a folder of files, not a package to build and install.
			sync := []string{"sync", "--quiet", "--project", release, "--python", p.cfg.PythonBin, "--no-dev", "--no-install-project"}
			if _, err := os.Stat(filepath.Join(release, "uv.lock")); err == nil {
				// Follow the lock file exactly; refuse one that no longer matches.
				sync = append(sync, "--locked")
			}
			out, err := run(uv[0], append(uv[1:], append(sync, linkMode...)...)...)
			return source, out, err
		}
		out, err := run(uv[0], append(uv[1:], "venv", "--quiet", "--python", p.cfg.PythonBin, venv)...)
		if err != nil || source == "" {
			return source, out, err
		}
		install := append([]string{"pip", "install", "--quiet", "--python", venvPython(release), "-r", requirements}, linkMode...)
		more, err := run(uv[0], append(uv[1:], install...)...)
		return source, out + more, err
	}

	// Without uv: the standard tools. A lock file is not followed.
	out, err := run(p.cfg.PythonBin, "-m", "venv", venv)
	if err != nil || source == "" {
		return source, out, err
	}
	pip := []string{"-m", "pip", "install", "--quiet", "--disable-pip-version-check"}
	if source == "pyproject.toml" {
		listed, err := run(venvPython(release), "-c", listDependenciesScript)
		if err != nil {
			return source, out + listed, err
		}
		var deps []string
		for _, line := range strings.Split(listed, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				deps = append(deps, line)
			}
		}
		if len(deps) == 0 {
			return source, out, nil
		}
		more, err := run(venvPython(release), append(pip, deps...)...)
		return source, out + more, err
	}
	more, err := run(venvPython(release), append(pip, "-r", requirements)...)
	return source, out + more, err
}

const importCheckScript = `
import importlib, sys
sys.path.insert(0, ".")
module = importlib.import_module("main")
app = getattr(module, "app", None)
if app is None:
    sys.exit("main.py must define a variable called app (for example: app = Flask(__name__))")
if not callable(app):
    sys.exit("app in main.py must be a web application object (Flask, FastAPI, ...), got " + type(app).__name__)
`

// checkImport imports the app the way a worker would, so a broken version is
// caught before it replaces the running one.
func (p *Platform) checkImport(ctx context.Context, release string, env map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, importCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, venvPython(release), "-c", importCheckScript)
	cmd.Dir = release
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, "PYTHONUNBUFFERED=1", "APP_IMPORT_CHECK=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out) + "\nThe app took too long to start (over " + importCheckTimeout.String() + ").", ctx.Err()
	}
	return string(out), err
}

// applyAppMigrations runs the app's migrations/*.sql files that have not been
// applied to its database yet, in file-name order, each in its own transaction.
func applyAppMigrations(ctx context.Context, databaseURL, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)

	conn, err := openAppDB(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to the app's database: %w", err)
	}
	defer conn.Close()
	if err := db.PrepareAppDatabase(ctx, conn); err != nil {
		return nil, err
	}
	q := appdbgen.New(conn)
	before, err := q.ListAppliedAppMigrations(ctx)
	if err != nil {
		return nil, err
	}
	done := make(map[string]bool, len(before))
	for _, name := range before {
		done[name] = true
	}
	var applied []string
	for _, name := range names {
		if done[name] {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return applied, err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migrations/%s: %w", name, err)
		}
		if err := q.WithTx(tx).RecordAppMigration(ctx, name); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migrations/%s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return applied, fmt.Errorf("migrations/%s: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}

// pruneReleases keeps the newest KeepReleases release folders (always
// including the live one).
func (p *Platform) pruneReleases(releases, live string) {
	entries, err := os.ReadDir(releases)
	if err != nil {
		return
	}
	type rel struct {
		path string
		mod  time.Time
	}
	var all []rel
	for _, e := range entries {
		if info, err := e.Info(); err == nil && e.IsDir() {
			all = append(all, rel{filepath.Join(releases, e.Name()), info.ModTime()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	kept := 0
	for _, r := range all {
		if r.path == live || kept < p.cfg.KeepReleases-1 {
			if r.path != live {
				kept++
			}
			continue
		}
		// Requests still running on an old release keep their loaded code in
		// memory; removing the folder a little later is safe.
		path := r.path
		time.AfterFunc(2*time.Minute, func() { _ = os.RemoveAll(path) })
	}
}

// RollBack makes an earlier version the latest again (as a new version, so
// history stays linear) and publishes it.
func (p *Platform) RollBack(ctx context.Context, slug, version, by string) (*PublishResult, error) {
	a, err := p.requireApp(ctx, slug)
	if err != nil {
		return nil, err
	}
	if version == "" {
		return nil, errors.New("say which version to go back to (see list_versions)")
	}
	app, draft := SplitSlug(slug)
	defer p.lock(app)()
	r := p.repo(app)
	branch := branchFor(draft)
	old, err := r.Files(version, nil)
	if err != nil {
		return nil, err
	}
	head, err := r.Head(branch)
	if err != nil {
		return nil, err
	}
	current, err := r.Files(head, nil)
	if err != nil {
		return nil, err
	}
	changes := map[string][]byte{}
	for name, data := range old {
		changes[name] = data
	}
	for name := range current {
		if _, keep := old[name]; !keep {
			changes[name] = nil
		}
	}
	if _, err := r.Save(branch, changes, "Go back to version "+version, by); err != nil {
		return nil, err
	}
	res := p.publishLocked(ctx, a, "", by)
	if res.OK {
		res.Steps = append(res.Steps, "Note: database changes made by later versions were not undone.")
	}
	return res, nil
}
