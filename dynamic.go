package caddysnake

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

var validModulePattern = regexp.MustCompile(`^[a-zA-Z_][\w.]*:[a-zA-Z_]\w*$`)

const (
	// defaultMaxDynamicApps caps cached DynamicApp tenants to bound memory/process use.
	defaultMaxDynamicApps = 128
	// defaultDynamicAppIdleTTL is how long an unused dynamic app may stay cached.
	defaultDynamicAppIdleTTL = 30 * time.Minute

	envMaxDynamicApps      = "CADDYSNAKE_MAX_DYNAMIC_APPS"
	envDynamicAppIdleTTL   = "CADDYSNAKE_DYNAMIC_APP_IDLE_TTL"
	dynamicAppCleanupGrace = 10 * time.Second
)

var errDynamicAppLimit = errors.New("dynamic app limit reached")

func parseMaxDynamicApps() int {
	if v := os.Getenv(envMaxDynamicApps); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxDynamicApps
}

func parseDynamicAppIdleTTL() time.Duration {
	if v := os.Getenv(envDynamicAppIdleTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil && d > 0 {
			return d
		}
	}
	return defaultDynamicAppIdleTTL
}

// hasDotDotSegment reports whether the raw (pre-normalization) path contains
// a ".." segment. Checking before filepath.Abs/Clean is important: those
// normalize traversal sequences away (e.g. "/srv/apps/../../etc" becomes
// "/etc"), which would let placeholder-injected values escape the intended
// directory while passing a check on the normalized result.
func hasDotDotSegment(path string) bool {
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if seg == ".." {
			return true
		}
	}
	return false
}

func validateResolvedValues(module, dir, venv string) error {
	if !validModulePattern.MatchString(module) {
		return fmt.Errorf("invalid module name: %q", module)
	}
	if dir != "" {
		if hasDotDotSegment(dir) {
			return fmt.Errorf("working directory contains path traversal: %q", dir)
		}
		if _, err := filepath.Abs(dir); err != nil {
			return fmt.Errorf("invalid working directory: %w", err)
		}
	}
	if venv != "" {
		if hasDotDotSegment(venv) {
			return fmt.Errorf("venv path contains path traversal: %q", venv)
		}
		if _, err := filepath.Abs(venv); err != nil {
			return fmt.Errorf("invalid venv path: %w", err)
		}
	}
	return nil
}

// containsPlaceholder checks if a string contains Caddy placeholders (e.g. {host.labels.0}).
func containsPlaceholder(s string) bool {
	return strings.Contains(s, "{") && strings.Contains(s, "}")
}

func validateResolvedEnvConfig(dir string, envFiles []string) error {
	for _, p := range envFiles {
		if p == "" {
			continue
		}
		if containsPlaceholder(p) {
			return fmt.Errorf("env_file path contains unresolved placeholder: %q", p)
		}
		if hasDotDotSegment(p) {
			return fmt.Errorf("env_file path contains path traversal: %q", p)
		}
		abs, err := resolveEnvFilePath(dir, p)
		if err != nil {
			return err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("env_file %q: %w", abs, err)
		}
		if info.IsDir() {
			return fmt.Errorf("env_file %q is a directory", abs)
		}
	}
	return nil
}

func envVarsCacheKey(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(';')
	}
	return b.String()
}

// appFactory is a function that creates a new AppServer for a resolved
// module, working directory, venv path, and env configuration.
type appFactory func(resolvedModule, resolvedDir, resolvedVenv string, envFiles []string, envVars map[string]string) (AppServer, error)

// appCreate tracks an in-flight factory call so concurrent requests for the
// same key wait for one create, while other keys are not blocked.
type appCreate struct {
	done chan struct{}
	app  AppServer
	err  error
}

// failedAppCreate caches a failed factory result briefly so repeated requests
// for a bad dynamic key do not fork/exec Python on every hit (DoS amplifier).
type failedAppCreate struct {
	err       error
	expiresAt time.Time
}

const dynamicCreateFailureTTL = 5 * time.Second

// DynamicApp implements AppServer by lazily importing Python apps based on
// Caddy placeholders resolved at request time. For example, when working_dir
// contains {host.labels.2}, each subdomain gets its own Python app instance
// imported from the corresponding directory.
//
// Live apps (cached + in-flight creates) are bounded by maxApps (default 128)
// and idleTTL (default 30m). Override with CADDYSNAKE_MAX_DYNAMIC_APPS and
// CADDYSNAKE_DYNAMIC_APP_IDLE_TTL. Apps with active requests are not idle/LRU
// evicted.
type DynamicApp struct {
	mu              sync.RWMutex
	apps            map[string]AppServer
	lastUsed        map[string]time.Time
	inUse           map[string]int    // active HandleRequest refs; blocks idle/LRU eviction
	appDirs         map[string]string // cache key -> resolved working dir (for eviction/autoreload)
	inflight        map[string]*appCreate
	failed          map[string]failedAppCreate
	closed          bool
	modulePattern   string
	workingDir      string
	venvPath        string
	envFilePatterns []string
	envVarPatterns  map[string]string
	factory         appFactory
	logger          *zap.Logger
	maxApps         int
	idleTTL         time.Duration
	now             func() time.Time // overridable in tests

	// Autoreload fields
	autoreload          bool
	watcher             *fsnotify.Watcher
	dirToKeys           map[string][]string // abs working dir -> cache keys that use it
	stopCh              chan struct{}
	exitOnReloadFailure func(code int) // if set and autoreload, process exits when app creation fails
}

// NewDynamicApp creates a DynamicApp that resolves placeholders from
// modulePattern, workingDir, venvPath, env_file paths, and env_var values at
// request time and lazily creates Python app instances via the supplied factory.
// When autoreload is true, if exitOnReloadFailure is non-nil it is called with
// code 1 when app creation fails (e.g. app deleted), so the process can terminate.
//
// Optional maxApps overrides the env/default cache cap when provided and > 0
// (from Caddyfile max_dynamic_apps / --max-dynamic-apps).
func NewDynamicApp(modulePattern, workingDir, venvPath string, envFilePatterns []string, envVarPatterns map[string]string, factory appFactory, logger *zap.Logger, autoreload bool, exitOnReloadFailure func(code int), maxApps ...int) (*DynamicApp, error) {
	maxDynamicApps := parseMaxDynamicApps()
	if len(maxApps) > 0 {
		if maxApps[0] < 0 {
			return nil, fmt.Errorf("max dynamic apps must be non-negative")
		}
		if maxApps[0] > 0 {
			maxDynamicApps = maxApps[0]
		}
	}
	d := &DynamicApp{
		apps:                make(map[string]AppServer),
		lastUsed:            make(map[string]time.Time),
		inUse:               make(map[string]int),
		appDirs:             make(map[string]string),
		inflight:            make(map[string]*appCreate),
		failed:              make(map[string]failedAppCreate),
		modulePattern:       modulePattern,
		workingDir:          workingDir,
		venvPath:            venvPath,
		envFilePatterns:     cloneEnvFiles(envFilePatterns),
		envVarPatterns:      cloneEnvVars(envVarPatterns),
		factory:             factory,
		logger:              logger,
		maxApps:             maxDynamicApps,
		idleTTL:             parseDynamicAppIdleTTL(),
		now:                 time.Now,
		autoreload:          autoreload,
		exitOnReloadFailure: exitOnReloadFailure,
	}
	if autoreload {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return nil, err
		}
		d.watcher = watcher
		d.dirToKeys = make(map[string][]string)
		d.stopCh = make(chan struct{})
		go d.watchForChanges()
		logger.Info("autoreload enabled for dynamic app")
	}

	return d, nil
}

// pruneExpiredFailuresLocked removes expired negative-cache entries.
// Caller must hold d.mu for writing.
func (d *DynamicApp) pruneExpiredFailuresLocked(now time.Time) {
	for key, f := range d.failed {
		if !now.Before(f.expiresAt) {
			delete(d.failed, key)
		}
	}
}

// rememberFailedCreateLocked records a failed create and drops expired entries.
// Caller must hold d.mu for writing.
func (d *DynamicApp) rememberFailedCreateLocked(key string, err error) {
	now := time.Now()
	d.pruneExpiredFailuresLocked(now)
	d.failed[key] = failedAppCreate{err: err, expiresAt: now.Add(dynamicCreateFailureTTL)}
}

// resolve uses the Caddy replacer from the request context to substitute
// placeholders in the module pattern, working directory, venv path, env files,
// and env_var values.
func (d *DynamicApp) resolve(r *http.Request) (key, module, dir, venv string, envFiles []string, envVars map[string]string) {
	module = d.modulePattern
	dir = d.workingDir
	venv = d.venvPath
	envFiles = cloneEnvFiles(d.envFilePatterns)
	envVars = cloneEnvVars(d.envVarPatterns)

	if repl, ok := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer); ok && repl != nil {
		module = repl.ReplaceAll(module, "")
		dir = repl.ReplaceAll(dir, "")
		venv = repl.ReplaceAll(venv, "")
		for i, p := range envFiles {
			envFiles[i] = repl.ReplaceAll(p, "")
		}
		for name, value := range envVars {
			envVars[name] = repl.ReplaceAll(value, "")
		}
	}

	key = module + "|" + dir + "|" + venv + "|" + strings.Join(envFiles, ",") + "|" + envVarsCacheKey(envVars)
	return
}

// getOrCreateApp returns an existing app for the given key, or creates one
// using the factory if it doesn't exist yet. The factory runs outside the
// exclusive lock so a slow or indefinite start_timeout for one tenant cannot
// stall other dynamic keys (same pattern as AutoreloadableApp.reload).
func (d *DynamicApp) getOrCreateApp(key, module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
	if err := validateResolvedValues(module, dir, venv); err != nil {
		return nil, err
	}
	if err := validateResolvedEnvConfig(dir, envFiles); err != nil {
		return nil, err
	}
	for name, value := range envVars {
		if containsPlaceholder(value) {
			return nil, fmt.Errorf("env_var %q contains unresolved placeholder", name)
		}
	}

	now := d.now()

	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return nil, errors.New("dynamic app shutting down")
	}
	app, ok := d.apps[key]
	if ok {
		d.mu.RUnlock()
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return nil, errors.New("dynamic app shutting down")
		}
		if app2, still := d.apps[key]; still {
			d.lastUsed[key] = now
			d.mu.Unlock()
			return app2, nil
		}
		d.mu.Unlock()
		// key disappeared between locks; fall through to create path
	} else {
		if f, failed := d.failed[key]; failed && time.Now().Before(f.expiresAt) {
			err := f.err
			d.mu.RUnlock()
			return nil, err
		}
		d.mu.RUnlock()
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("dynamic app shutting down")
	}
	d.pruneExpiredFailuresLocked(time.Now())
	app, ok = d.apps[key]
	if ok {
		d.lastUsed[key] = now
		d.mu.Unlock()
		return app, nil
	}
	if f, failed := d.failed[key]; failed && time.Now().Before(f.expiresAt) {
		err := f.err
		d.mu.Unlock()
		return nil, err
	}
	if c, creating := d.inflight[key]; creating {
		d.mu.Unlock()
		<-c.done
		return c.app, c.err
	}
	// Reserve capacity before running the factory so concurrent first-time
	// tenants cannot spawn more live worker groups than maxApps.
	now = d.now()
	evicted := d.makeRoomLocked(key, now)
	if len(d.apps)+len(d.inflight) >= d.maxApps {
		d.mu.Unlock()
		d.cleanupAppsAsync(evicted)
		return nil, fmt.Errorf("%w: max %d cached apps (set %s)", errDynamicAppLimit, d.maxApps, envMaxDynamicApps)
	}
	c := &appCreate{done: make(chan struct{})}
	d.inflight[key] = c
	d.mu.Unlock()
	d.cleanupAppsAsync(evicted)

	d.logger.Info("dynamically importing python app",
		zap.String("module", module),
		zap.String("working_dir", dir),
		zap.String("venv", venv),
	)

	// Factory runs outside the lock; if it panics, still remove the inflight
	// entry and close done so waiters and Cleanup cannot hang forever.
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				d.mu.Lock()
				delete(d.inflight, key)
				c.app = nil
				c.err = fmt.Errorf("panic creating dynamic app: %v", r)
				d.rememberFailedCreateLocked(key, c.err)
				close(c.done)
				d.mu.Unlock()
				panic(r)
			}
		}()
		app, err = d.factory(module, dir, venv, cloneEnvFiles(envFiles), cloneEnvVars(envVars))
	}()

	d.mu.Lock()
	delete(d.inflight, key)
	if d.closed {
		d.mu.Unlock()
		if app != nil {
			_ = app.Cleanup()
		}
		if err == nil {
			err = errors.New("dynamic app shutting down")
		}
		c.app = nil
		c.err = err
		close(c.done)
		return nil, err
	}
	if err == nil {
		delete(d.failed, key)
		// Refresh after the out-of-lock factory so lastUsed uses wall clock
		// at insert time, not create-start.
		now = d.now()
		d.apps[key] = app
		d.lastUsed[key] = now
		d.appDirs[key] = dir
		if d.autoreload && dir != "" {
			d.startWatchingDir(dir, key)
		}
	} else {
		d.rememberFailedCreateLocked(key, err)
	}
	c.app = app
	c.err = err
	close(c.done)
	d.mu.Unlock()

	return app, err
}

// makeRoomLocked expires idle apps and, if still at capacity, evicts the
// least-recently-used entry (other than excludeKey). Apps with active
// requests are never evicted here. Caller holds d.mu.
func (d *DynamicApp) makeRoomLocked(excludeKey string, now time.Time) []AppServer {
	var evicted []AppServer
	for key, used := range d.lastUsed {
		if key == excludeKey || d.inUse[key] > 0 {
			continue
		}
		if now.Sub(used) >= d.idleTTL {
			if app := d.removeAppLocked(key); app != nil {
				evicted = append(evicted, app)
			}
		}
	}
	for len(d.apps) >= d.maxApps {
		lruKey := ""
		var lruTime time.Time
		for key, used := range d.lastUsed {
			if key == excludeKey || d.inUse[key] > 0 {
				continue
			}
			if lruKey == "" || used.Before(lruTime) {
				lruKey = key
				lruTime = used
			}
		}
		if lruKey == "" {
			break
		}
		if app := d.removeAppLocked(lruKey); app != nil {
			evicted = append(evicted, app)
			d.logger.Info("evicted dynamic python app (cache full)",
				zap.String("key", lruKey),
				zap.Int("max_apps", d.maxApps),
			)
		} else {
			break
		}
	}
	return evicted
}

// removeAppLocked deletes a cached app and its autoreload bookkeeping.
// Caller holds d.mu. Returns the app for async Cleanup.
func (d *DynamicApp) removeAppLocked(key string) AppServer {
	app, ok := d.apps[key]
	if !ok {
		return nil
	}
	delete(d.apps, key)
	delete(d.lastUsed, key)
	// Leave inUse intact so a still-running handler's releaseApp cannot
	// accidentally unpin a replacement app created for the same key.
	dir := d.appDirs[key]
	delete(d.appDirs, key)
	if d.autoreload && dir != "" {
		if absDir, err := filepath.Abs(dir); err == nil {
			d.untrackKeyLocked(absDir, key)
		}
	}
	return app
}

// releaseApp decrements the active-request refcount for key and refreshes
// lastUsed when the last request finishes so idle TTL starts after real use.
func (d *DynamicApp) releaseApp(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.inUse[key]
	if n <= 1 {
		delete(d.inUse, key)
		if _, ok := d.apps[key]; ok {
			d.lastUsed[key] = d.now()
		}
		return
	}
	d.inUse[key] = n - 1
}

func (d *DynamicApp) untrackKeyLocked(absDir, key string) {
	keys := d.dirToKeys[absDir]
	if len(keys) == 0 {
		return
	}
	out := keys[:0]
	for _, k := range keys {
		if k != key {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		delete(d.dirToKeys, absDir)
		return
	}
	d.dirToKeys[absDir] = out
}

func (d *DynamicApp) cleanupAppsAsync(apps []AppServer) {
	if len(apps) == 0 {
		return
	}
	go func() {
		time.Sleep(dynamicAppCleanupGrace)
		for _, app := range apps {
			if err := app.Cleanup(); err != nil {
				d.logger.Error("failed to cleanup old dynamic app", zap.Error(err))
			}
		}
	}()
}
func (d *DynamicApp) startWatchingDir(dir, key string) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		d.logger.Warn("autoreload: failed to resolve directory",
			zap.String("dir", dir),
			zap.Error(err),
		)
		return
	}

	if keys, ok := d.dirToKeys[absDir]; ok {
		for _, k := range keys {
			if k == key {
				return
			}
		}
		d.dirToKeys[absDir] = append(keys, key)
		return
	}

	d.dirToKeys[absDir] = []string{key}
	watchDirRecursive(d.watcher, absDir, d.logger)
}

// pathWithinDir reports whether path is dir itself or a file/subdir under dir.
// Unlike strings.HasPrefix, it rejects sibling paths that share a prefix
// (e.g. /srv/apps/foobar is not within /srv/apps/foo).
func pathWithinDir(path, dir string) bool {
	if path == dir {
		return true
	}
	prefix := dir + string(os.PathSeparator)
	return strings.HasPrefix(path, prefix)
}

func (d *DynamicApp) watchForChanges() {
	var debounceTimer *time.Timer
	const debounceDuration = 500 * time.Millisecond

	pendingDirs := make(map[string]bool)
	var pendingMu sync.Mutex

	for {
		select {
		case event, ok := <-d.watcher.Events:
			if !ok {
				return
			}
			if !isPythonFileEvent(event) {
				handleNewDirEvent(event, d.watcher)
				continue
			}

			d.logger.Debug("python file changed (dynamic)",
				zap.String("file", event.Name),
				zap.String("op", event.Op.String()),
			)

			d.mu.RLock()
			for absDir := range d.dirToKeys {
				if pathWithinDir(event.Name, absDir) {
					pendingMu.Lock()
					pendingDirs[absDir] = true
					pendingMu.Unlock()
				}
			}
			d.mu.RUnlock()

			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(debounceDuration, func() {
				pendingMu.Lock()
				dirs := make([]string, 0, len(pendingDirs))
				for dir := range pendingDirs {
					dirs = append(dirs, dir)
				}
				pendingDirs = make(map[string]bool)
				pendingMu.Unlock()

				for _, dir := range dirs {
					d.reloadDir(dir)
				}
			})
		case err, ok := <-d.watcher.Errors:
			if !ok {
				return
			}
			d.logger.Error("autoreload watcher error", zap.Error(err))
		case <-d.stopCh:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		}
	}
}

// reloadDir evicts all apps associated with the given directory and
// cleans them up after a grace period.
func (d *DynamicApp) reloadDir(absDir string) {
	d.logger.Info("reloading dynamic python apps due to file changes",
		zap.String("working_dir", absDir),
	)

	d.mu.Lock()

	keys, ok := d.dirToKeys[absDir]
	if !ok {
		d.mu.Unlock()
		return
	}

	var oldApps []AppServer
	for _, key := range keys {
		if app := d.removeAppLocked(key); app != nil {
			oldApps = append(oldApps, app)
		}
		delete(d.failed, key)
	}

	delete(d.dirToKeys, absDir)

	d.mu.Unlock()

	d.logger.Info("dynamic python apps evicted, will reimport on next request",
		zap.String("working_dir", absDir),
		zap.Int("apps_evicted", len(oldApps)),
	)

	d.cleanupAppsAsync(oldApps)
}

// HandleRequest resolves placeholders from the request, gets or creates the
// appropriate app, and forwards the request. The app is pinned for the
// duration of the handler so idle/LRU eviction cannot tear down workers
// serving long-lived connections (e.g. WebSockets).
func (d *DynamicApp) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	key, module, dir, venv, envFiles, envVars := d.resolve(r)
	for {
		app, err := d.getOrCreateApp(key, module, dir, venv, envFiles, envVars)
		if err != nil {
			if d.autoreload && d.exitOnReloadFailure != nil {
				d.logger.Error("failed to load python app (autoreload); terminating",
					zap.String("module", module),
					zap.String("working_dir", dir),
					zap.Error(err),
				)
				d.exitOnReloadFailure(1)
			}
			return err
		}
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return errors.New("dynamic app shutting down")
		}
		if cur, ok := d.apps[key]; ok && cur == app {
			d.inUse[key]++
			d.mu.Unlock()
			// defer so a panic in the tenant handler cannot permanently pin the key
			// out of idle/LRU eviction.
			defer d.releaseApp(key)
			return app.HandleRequest(w, r)
		}
		d.mu.Unlock()
		// Evicted between lookup and pin; retry get-or-create.
	}
}

// Cleanup frees all dynamically created apps and stops the autoreload watcher.
func (d *DynamicApp) Cleanup() error {
	d.mu.Lock()
	d.closed = true
	for len(d.inflight) > 0 {
		waits := make([]chan struct{}, 0, len(d.inflight))
		for _, c := range d.inflight {
			waits = append(waits, c.done)
		}
		d.mu.Unlock()
		for _, done := range waits {
			<-done
		}
		d.mu.Lock()
	}

	if d.autoreload && d.stopCh != nil {
		close(d.stopCh)
		_ = d.watcher.Close()
	}

	var errs []error
	for key, app := range d.apps {
		if err := app.Cleanup(); err != nil {
			errs = append(errs, err)
		}
		delete(d.apps, key)
		delete(d.lastUsed, key)
		delete(d.inUse, key)
		delete(d.appDirs, key)
	}
	d.failed = make(map[string]failedAppCreate)
	d.mu.Unlock()
	return errors.Join(errs...)
}
