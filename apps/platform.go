package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	caddysnake "github.com/mliezun/caddy-snake"
)

// Config is everything the platform needs to run.
type Config struct {
	// DataDir holds app files, version history and (by default) the database.
	DataDir string `json:"data_dir,omitempty"`
	// DatabaseURL points at an existing Postgres server. Empty means: start and
	// manage a private one inside DataDir.
	DatabaseURL string `json:"database_url,omitempty"`
	// PythonBin is the interpreter used to build each app's environment.
	PythonBin string `json:"python_bin,omitempty"`
	// PublicURL is the address people use to reach the server, for links.
	PublicURL string `json:"public_url,omitempty"`
	// RequireLogin makes everyone sign in with a username and password.
	RequireLogin bool `json:"require_login,omitempty"`
	// DraftTTL removes drafts nobody has opened or changed for this long.
	DraftTTL time.Duration `json:"draft_ttl,omitempty"`
	// KeepReleases is how many built versions stay on disk per app.
	KeepReleases int `json:"keep_releases,omitempty"`
}

func (c *Config) applyDefaults() error {
	if c.DataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("choosing a data folder: %w (pass --data-dir)", err)
		}
		c.DataDir = filepath.Join(home, ".caddysnake", "apps")
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return err
	}
	c.DataDir = abs
	if c.PythonBin == "" {
		c.PythonBin = defaultPythonFor(runtime.GOOS)
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:9080"
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	if c.DraftTTL == 0 {
		c.DraftTTL = 14 * 24 * time.Hour
	}
	if c.KeepReleases <= 0 {
		c.KeepReleases = 5
	}
	return nil
}

// resolve applies defaults, creates the data folders and resolves symlinks in
// the data path (macOS /var -> /private/var), so the paths handed to
// caddy-snake match the ones it reports back. It is safe to call repeatedly.
func (c *Config) resolve() error {
	if err := c.applyDefaults(); err != nil {
		return err
	}
	for _, dir := range []string{c.DataDir, filepath.Join(c.DataDir, "apps"), filepath.Join(c.DataDir, "repos")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating data folder: %w", err)
		}
	}
	if resolved, err := filepath.EvalSymlinks(c.DataDir); err == nil {
		c.DataDir = resolved
	}
	return nil
}

// Platform is the running system: database, version storage and publishing.
type Platform struct {
	cfg    Config
	logger *zap.Logger
	pg     *postgres
	store  *store
	logs   *logBook

	lockMu sync.Mutex
	locks  map[string]*sync.Mutex // one per app (drafts share their app's lock)

	knownMu sync.RWMutex
	known   map[string]bool // serving slug -> has a published version

	touchMu sync.Mutex
	touched map[string]struct{}

	stopObserve func()
	stop        chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

// Open starts the platform: the database, the platform's own tables, and the
// background jobs.
func Open(ctx context.Context, cfg Config, logger *zap.Logger) (*Platform, error) {
	if err := cfg.resolve(); err != nil {
		return nil, err
	}
	var pg *postgres
	var err error
	if cfg.DatabaseURL == "" {
		pg, err = startManagedPostgres(ctx, cfg.DataDir, logger)
	} else {
		pg, err = connectPostgres(ctx, cfg.DatabaseURL, logger)
	}
	if err != nil {
		return nil, err
	}
	p := &Platform{
		cfg:     cfg,
		logger:  logger,
		pg:      pg,
		store:   newStore(pg.admin),
		logs:    newLogBook(500),
		locks:   map[string]*sync.Mutex{},
		known:   map[string]bool{},
		touched: map[string]struct{}{},
		stop:    make(chan struct{}),
	}
	if err := p.store.migrate(ctx); err != nil {
		pg.Close()
		return nil, fmt.Errorf("preparing the platform's tables: %w", err)
	}
	all, err := p.store.ListAll(ctx)
	if err != nil {
		pg.Close()
		return nil, err
	}
	for _, a := range all {
		p.known[a.Slug] = a.LiveVersion != ""
	}
	p.stopObserve = caddysnake.ObserveWorkerOutput(p.observe)
	p.wg.Add(1)
	go p.background()
	return p, nil
}

// Close stops background jobs and the built-in database.
func (p *Platform) Close() {
	p.stopOnce.Do(func() {
		close(p.stop)
		p.wg.Wait()
		if p.stopObserve != nil {
			p.stopObserve()
		}
		p.flushTouched(context.Background())
		p.pg.Close()
	})
}

// Config returns the effective configuration.
func (p *Platform) Config() Config { return p.cfg }

func (p *Platform) appsRoot() string          { return filepath.Join(p.cfg.DataDir, "apps") }
func (p *Platform) appDir(slug string) string { return filepath.Join(p.appsRoot(), slug) }
func (p *Platform) repoDir(app string) string {
	return filepath.Join(p.cfg.DataDir, "repos", app+".git")
}
func (p *Platform) repo(app string) *repo { return &repo{dir: p.repoDir(app)} }

// URL returns the address of a served app or draft.
func (p *Platform) URL(slug string) string { return p.cfg.PublicURL + "/" + slug + "/" }

func (p *Platform) lock(app string) func() {
	p.lockMu.Lock()
	m := p.locks[app]
	if m == nil {
		m = &sync.Mutex{}
		p.locks[app] = m
	}
	p.lockMu.Unlock()
	m.Lock()
	return m.Unlock
}

// Known reports whether slug is an app or draft, and whether it is published.
func (p *Platform) Known(slug string) (exists, published bool) {
	p.knownMu.RLock()
	defer p.knownMu.RUnlock()
	published, exists = p.known[slug]
	return exists, published
}

func (p *Platform) setKnown(slug string, published bool) {
	p.knownMu.Lock()
	p.known[slug] = published
	p.knownMu.Unlock()
}

func (p *Platform) forget(slug string) {
	p.knownMu.Lock()
	delete(p.known, slug)
	p.knownMu.Unlock()
}

// Touch records that a slug was just used, so idle drafts can be expired.
func (p *Platform) Touch(slug string) {
	p.touchMu.Lock()
	p.touched[slug] = struct{}{}
	p.touchMu.Unlock()
}

func (p *Platform) flushTouched(ctx context.Context) {
	p.touchMu.Lock()
	slugs := make([]string, 0, len(p.touched))
	for s := range p.touched {
		slugs = append(slugs, s)
	}
	p.touched = map[string]struct{}{}
	p.touchMu.Unlock()
	p.store.Touch(ctx, slugs)
}

func (p *Platform) background() {
	defer p.wg.Done()
	flush := time.NewTicker(time.Minute)
	sweep := time.NewTicker(time.Hour)
	defer flush.Stop()
	defer sweep.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-flush.C:
			p.flushTouched(context.Background())
		case <-sweep.C:
			p.expireDrafts(context.Background())
		}
	}
}

func (p *Platform) expireDrafts(ctx context.Context) {
	p.flushTouched(ctx)
	all, err := p.store.ListAll(ctx)
	if err != nil {
		return
	}
	for _, a := range all {
		if a.IsDraft() && time.Since(a.LastUsedAt) > p.cfg.DraftTTL {
			p.logger.Info("removing a draft nobody has used", zap.String("draft", a.Slug))
			if err := p.DeleteApp(ctx, a.Slug); err != nil {
				p.logger.Warn("removing an unused draft", zap.String("draft", a.Slug), zap.Error(err))
			}
		}
	}
}

// --- logs -----------------------------------------------------------------

// slugFromDir extracts the serving slug from a working directory under the
// apps root (".../apps/<slug>/current" or ".../apps/<slug>/releases/<id>").
func (p *Platform) slugFromDir(dir string) string {
	root := p.appsRoot() + string(filepath.Separator)
	if !strings.HasPrefix(dir, root) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil && strings.HasPrefix(resolved, root) {
			dir = resolved
		} else {
			return ""
		}
	}
	rest := strings.TrimPrefix(dir, root)
	if i := strings.IndexByte(rest, filepath.Separator); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func (p *Platform) observe(out caddysnake.WorkerOutput) {
	// The worker's own start-up line is noise to someone reading an app's activity.
	if strings.Contains(out.Line, "server listening on ") {
		return
	}
	if slug := p.slugFromDir(out.WorkingDir); slug != "" {
		p.logs.Add(slug, out.Time, out.Stream, out.Line)
	}
}

// Logs returns the most recent output lines of an app or draft.
func (p *Platform) Logs(slug string, limit int) []LogLine { return p.logs.Tail(slug, limit) }

// --- settings ---------------------------------------------------------------

// Limits that match the column widths in db/migrations.
const (
	maxSettingKeyLen  = 100
	maxAppNameLen     = 100
	maxDescriptionLen = 500
	maxKeyNameLen     = 60
)

var settingKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// builtinSettingKeys are provided by the platform and cannot be overridden.
var builtinSettingKeys = map[string]bool{
	"DATABASE_URL": true, "APP_SLUG": true, "APP_NAME": true, "APP_BASE_PATH": true,
	"APP_URL": true, "APP_IS_DRAFT": true, "PYTHONUNBUFFERED": true,
}

func validateSettingKey(key string) error {
	if len(key) > maxSettingKeyLen {
		return fmt.Errorf("setting names can be at most %d characters", maxSettingKeyLen)
	}
	if !settingKeyRE.MatchString(key) {
		return fmt.Errorf("setting name %q must be uppercase letters, digits and underscores, starting with a letter (for example API_TOKEN)", key)
	}
	if builtinSettingKeys[key] {
		return fmt.Errorf("%s is set by the platform and cannot be changed", key)
	}
	if strings.HasPrefix(key, "CADDYSNAKE_") || strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") {
		return fmt.Errorf("setting name %q is reserved", key)
	}
	return nil
}

func (p *Platform) settingsPath(slug string) string {
	return filepath.Join(p.appDir(slug), "settings.env")
}

// appEnv is the full environment an app runs with.
func (p *Platform) appEnv(ctx context.Context, a *App) (map[string]string, error) {
	env, err := p.store.Settings(ctx, a.Slug)
	if err != nil {
		return nil, err
	}
	env["DATABASE_URL"] = a.DatabaseURL
	env["APP_SLUG"] = a.Slug
	env["APP_NAME"] = a.Name
	env["APP_BASE_PATH"] = "/" + a.Slug
	env["APP_URL"] = p.URL(a.Slug)
	env["APP_IS_DRAFT"] = "0"
	if a.IsDraft() {
		env["APP_IS_DRAFT"] = "1"
	}
	return env, nil
}

func (p *Platform) writeSettings(ctx context.Context, a *App) error {
	env, err := p.appEnv(ctx, a)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Written by caddy-snake apps. Change settings with the set_setting tool, not by hand.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, env[k])
	}
	if err := os.MkdirAll(p.appDir(a.Slug), 0o750); err != nil {
		return err
	}
	tmp := p.settingsPath(a.Slug) + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.settingsPath(a.Slug))
}

// SetSetting stores (or with nil removes) one setting and restarts the app so
// it picks the change up.
func (p *Platform) SetSetting(ctx context.Context, slug, key string, value *string) error {
	if err := validateSettingKey(key); err != nil {
		return err
	}
	if value != nil && strings.ContainsAny(*value, "\r\n") {
		return errors.New("setting values must be a single line")
	}
	a, err := p.requireApp(ctx, slug)
	if err != nil {
		return err
	}
	app, _ := SplitSlug(slug)
	defer p.lock(app)()
	if err := p.store.SetSetting(ctx, slug, key, value); err != nil {
		return err
	}
	if err := p.writeSettings(ctx, a); err != nil {
		return err
	}
	caddysnake.ReloadWorkingDir(p.appDir(slug))
	return nil
}

// SettingNames lists the names (never the values) of an app's own settings.
func (p *Platform) SettingNames(ctx context.Context, slug string) ([]string, error) {
	settings, err := p.store.Settings(ctx, slug)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func (p *Platform) requireApp(ctx context.Context, slug string) (*App, error) {
	if err := ValidateServingSlug(slug); err != nil {
		return nil, err
	}
	a, err := p.store.GetApp(ctx, slug)
	if errors.Is(err, errNotFound) {
		if _, draft := SplitSlug(slug); draft != "" {
			return nil, fmt.Errorf("there is no draft %q", slug)
		}
		return nil, fmt.Errorf("there is no app called %q", slug)
	}
	return a, err
}

// --- app lifecycle ----------------------------------------------------------

// CreateApp makes a new app from the starter template and publishes it, so its
// address works straight away.
func (p *Platform) CreateApp(ctx context.Context, name, description, by string) (*App, *PublishResult, error) {
	slug := Slugify(name)
	if slug == "" {
		return nil, nil, errors.New("give the app a name with at least one letter")
	}
	if n := len([]rune(strings.TrimSpace(name))); n > maxAppNameLen {
		return nil, nil, fmt.Errorf("the name is %d characters long; keep it under %d", n, maxAppNameLen)
	}
	if n := len([]rune(description)); n > maxDescriptionLen {
		return nil, nil, fmt.Errorf("the description is %d characters long; keep it under %d", n, maxDescriptionLen)
	}
	if len(slug) > maxAppSlugLen {
		slug = strings.Trim(slug[:maxAppSlugLen], "-")
	}
	if err := ValidateAppSlug(slug); err != nil {
		return nil, nil, err
	}
	defer p.lock(slug)()
	if _, err := p.store.GetApp(ctx, slug); err == nil {
		return nil, nil, fmt.Errorf("an app called %q already exists; pick another name or change that one", slug)
	} else if !errors.Is(err, errNotFound) {
		return nil, nil, err
	}
	dbURL, err := p.pg.CreateAppDatabase(ctx, slug)
	if err != nil {
		return nil, nil, err
	}
	a := &App{Slug: slug, Name: strings.TrimSpace(name), Description: description, DatabaseURL: dbURL, CreatedBy: by}
	cleanup := func() {
		_ = p.pg.DropAppDatabase(context.Background(), slug)
		_ = os.RemoveAll(p.repoDir(slug))
		_ = os.RemoveAll(p.appDir(slug))
		_ = p.store.DeleteApp(context.Background(), slug)
		p.forget(slug)
	}
	_ = os.RemoveAll(p.repoDir(slug))
	r, err := initRepo(p.repoDir(slug))
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err := r.Save(mainBranch, starterFiles(a.Name), "Start from the template", by); err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := p.store.InsertApp(ctx, a); err != nil {
		cleanup()
		return nil, nil, err
	}
	p.setKnown(slug, false)
	if err := p.writeSettings(ctx, a); err != nil {
		cleanup()
		return nil, nil, err
	}
	res := p.publishLocked(ctx, a, "", by)
	fresh, err := p.store.GetApp(ctx, slug)
	if err != nil {
		return nil, nil, err
	}
	return fresh, res, nil
}

// DeleteApp removes an app with all its drafts, or a single draft.
func (p *Platform) DeleteApp(ctx context.Context, slug string) error {
	a, err := p.requireApp(ctx, slug)
	if err != nil {
		return err
	}
	app, draft := SplitSlug(slug)
	defer p.lock(app)()
	return p.deleteLocked(ctx, a, app, draft)
}

func (p *Platform) deleteLocked(ctx context.Context, a *App, app, draft string) error {
	if draft == "" {
		drafts, err := p.store.ListDrafts(ctx, app)
		if err != nil {
			return err
		}
		for _, d := range drafts {
			_, dn := SplitSlug(d.Slug)
			if err := p.deleteLocked(ctx, d, app, dn); err != nil {
				return err
			}
		}
	}
	p.forget(a.Slug)
	caddysnake.ReloadWorkingDir(p.appDir(a.Slug))
	if err := removeAll(p.appDir(a.Slug)); err != nil {
		return fmt.Errorf("removing the app's files: %w", err)
	}
	if err := p.pg.DropAppDatabase(ctx, a.Slug); err != nil {
		return err
	}
	if draft == "" {
		if err := removeAll(p.repoDir(app)); err != nil {
			return fmt.Errorf("removing the app's history: %w", err)
		}
	} else {
		_ = p.repo(app).DeleteBranch(branchFor(draft))
	}
	p.logs.Drop(a.Slug)
	return p.store.DeleteApp(ctx, a.Slug)
}

// SaveFiles records file changes as a new version of an app (draft "") or of
// one of its drafts. It does not publish.
func (p *Platform) SaveFiles(ctx context.Context, app, draft string, changes map[string][]byte, message, by string) (string, error) {
	slug := app
	if draft != "" {
		slug = DraftSlug(app, draft)
	}
	if _, err := p.requireApp(ctx, slug); err != nil {
		return "", err
	}
	defer p.lock(app)()
	p.Touch(slug)
	return p.repo(app).Save(branchFor(draft), changes, message, by)
}

// ReadFiles returns the files of the latest saved version (or a given one).
func (p *Platform) ReadFiles(ctx context.Context, app, draft, version string, only []string) (string, map[string][]byte, error) {
	slug := app
	if draft != "" {
		slug = DraftSlug(app, draft)
	}
	if _, err := p.requireApp(ctx, slug); err != nil {
		return "", nil, err
	}
	defer p.lock(app)()
	r := p.repo(app)
	if version == "" {
		head, err := r.Head(branchFor(draft))
		if err != nil {
			return "", nil, err
		}
		version = head
	}
	if version == "" {
		return "", map[string][]byte{}, nil
	}
	files, err := r.Files(version, only)
	return version, files, err
}

// Versions lists saved versions of an app or draft, newest first.
func (p *Platform) Versions(ctx context.Context, slug string, limit int) ([]Version, error) {
	if _, err := p.requireApp(ctx, slug); err != nil {
		return nil, err
	}
	app, draft := SplitSlug(slug)
	defer p.lock(app)()
	return p.repo(app).Versions(branchFor(draft), limit)
}
