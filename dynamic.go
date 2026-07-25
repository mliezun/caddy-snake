package caddysnake

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

var validModulePattern = regexp.MustCompile(`^[a-zA-Z_][\w.]*:[a-zA-Z_]\w*$`)

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
	done  chan struct{}
	entry *dynamicAppEntry
	err   error
}

type dynamicAppEntry struct {
	app         AppServer
	active      int
	retired     bool
	cleanupOnce sync.Once
	cleanupErr  error
}

func (e *dynamicAppEntry) cleanup() error {
	e.cleanupOnce.Do(func() {
		e.cleanupErr = e.app.Cleanup()
	})
	return e.cleanupErr
}

// DynamicApp implements AppServer by lazily importing Python apps based on
// Caddy placeholders resolved at request time. For example, when working_dir
// contains {host.labels.2}, each subdomain gets its own Python app instance
// imported from the corresponding directory.
type DynamicApp struct {
	mu              sync.RWMutex
	drain           *sync.Cond
	apps            map[string]*dynamicAppEntry
	inflight        map[string]*appCreate
	retired         map[*dynamicAppEntry]struct{}
	closed          bool
	maxApps         int
	modulePattern   string
	workingDir      string
	venvPath        string
	envFilePatterns []string
	envVarPatterns  map[string]string
	factory         appFactory
	logger          *zap.Logger

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
func NewDynamicApp(modulePattern, workingDir, venvPath string, envFilePatterns []string, envVarPatterns map[string]string, factory appFactory, logger *zap.Logger, autoreload bool, exitOnReloadFailure func(code int), maxApps ...int) (*DynamicApp, error) {
	maxDynamicApps := 0
	if len(maxApps) > 0 {
		maxDynamicApps = maxApps[0]
	}
	if maxDynamicApps < 0 {
		return nil, fmt.Errorf("max dynamic apps must be non-negative")
	}
	d := &DynamicApp{
		apps:                make(map[string]*dynamicAppEntry),
		inflight:            make(map[string]*appCreate),
		retired:             make(map[*dynamicAppEntry]struct{}),
		maxApps:             maxDynamicApps,
		modulePattern:       modulePattern,
		workingDir:          workingDir,
		venvPath:            venvPath,
		envFilePatterns:     cloneEnvFiles(envFilePatterns),
		envVarPatterns:      cloneEnvVars(envVarPatterns),
		factory:             factory,
		logger:              logger,
		autoreload:          autoreload,
		exitOnReloadFailure: exitOnReloadFailure,
	}
	d.drain = sync.NewCond(&d.mu)

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

// getOrCreateEntry returns an existing entry for the given key, or creates one
// using the factory if it doesn't exist yet. The factory runs outside the
// exclusive lock so a slow or indefinite start_timeout for one tenant cannot
// stall other dynamic keys (same pattern as AutoreloadableApp.reload).
func (d *DynamicApp) getOrCreateEntry(key, module, dir, venv string, envFiles []string, envVars map[string]string) (*dynamicAppEntry, error) {
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

	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return nil, errors.New("dynamic app shutting down")
	}
	entry, ok := d.apps[key]
	d.mu.RUnlock()
	if ok {
		return entry, nil
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("dynamic app shutting down")
	}
	entry, ok = d.apps[key]
	if ok {
		d.mu.Unlock()
		return entry, nil
	}
	if c, creating := d.inflight[key]; creating {
		d.mu.Unlock()
		<-c.done
		return c.entry, c.err
	}
	if d.maxApps > 0 && len(d.apps)+len(d.inflight)+len(d.retired) >= d.maxApps {
		d.mu.Unlock()
		return nil, fmt.Errorf("dynamic app limit reached (%d)", d.maxApps)
	}
	c := &appCreate{done: make(chan struct{})}
	d.inflight[key] = c
	d.mu.Unlock()

	d.logger.Info("dynamically importing python app",
		zap.String("module", module),
		zap.String("working_dir", dir),
		zap.String("venv", venv),
	)

	// Factory runs outside the lock; if it panics, still remove the inflight
	// entry and close done so waiters and Cleanup cannot hang forever.
	var app AppServer
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				app = nil
				err = fmt.Errorf("panic creating dynamic app: %v", r)
			}
		}()
		app, err = d.factory(module, dir, venv, cloneEnvFiles(envFiles), cloneEnvVars(envVars))
	}()
	if err == nil && app == nil {
		err = errors.New("dynamic app factory returned nil app")
	}

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
		c.entry = nil
		c.err = err
		close(c.done)
		return nil, err
	}
	if err == nil {
		entry = &dynamicAppEntry{app: app}
		d.apps[key] = entry
		if d.autoreload && dir != "" {
			d.startWatchingDir(dir, key)
		}
	}
	c.entry = entry
	c.err = err
	close(c.done)
	d.mu.Unlock()

	return entry, err
}

func (d *DynamicApp) getOrCreateApp(key, module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
	entry, err := d.getOrCreateEntry(key, module, dir, venv, envFiles, envVars)
	if err != nil {
		return nil, err
	}
	return entry.app, nil
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

func pathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func (d *DynamicApp) acquireApp(key, module, dir, venv string, envFiles []string, envVars map[string]string) (*dynamicAppEntry, error) {
	for {
		entry, err := d.getOrCreateEntry(key, module, dir, venv, envFiles, envVars)
		if err != nil {
			return nil, err
		}

		d.mu.Lock()
		current, ok := d.apps[key]
		if ok && current == entry && !entry.retired {
			entry.active++
			d.mu.Unlock()
			return entry, nil
		}
		d.mu.Unlock()
	}
}

func (d *DynamicApp) releaseApp(entry *dynamicAppEntry) {
	var cleanup *dynamicAppEntry
	d.mu.Lock()
	entry.active--
	if entry.active == 0 && entry.retired && !d.closed {
		delete(d.retired, entry)
		cleanup = entry
	}
	d.drain.Broadcast()
	d.mu.Unlock()

	if cleanup != nil {
		if err := cleanup.cleanup(); err != nil {
			d.logger.Error("failed to cleanup retired dynamic app", zap.Error(err))
		}
	}
}

func (d *DynamicApp) retireEntryLocked(key string, entry *dynamicAppEntry) *dynamicAppEntry {
	delete(d.apps, key)
	entry.retired = true
	if entry.active == 0 {
		return entry
	}
	d.retired[entry] = struct{}{}
	return nil
}

// reloadDir evicts all apps associated with the given directory and
// cleans each one up after its in-flight requests complete.
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

	evicted := 0
	var oldApps []*dynamicAppEntry
	for _, key := range keys {
		if entry, exists := d.apps[key]; exists {
			evicted++
			if cleanup := d.retireEntryLocked(key, entry); cleanup != nil {
				oldApps = append(oldApps, cleanup)
			}
		}
	}

	delete(d.dirToKeys, absDir)

	d.mu.Unlock()

	d.logger.Info("dynamic python apps evicted, will reimport on next request",
		zap.String("working_dir", absDir),
		zap.Int("apps_evicted", evicted),
	)

	for _, entry := range oldApps {
		if err := entry.cleanup(); err != nil {
			d.logger.Error("failed to cleanup old dynamic app",
				zap.Error(err),
			)
		}
	}
}

// HandleRequest resolves placeholders from the request, gets or creates the
// appropriate app, and forwards the request.
func (d *DynamicApp) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	key, module, dir, venv, envFiles, envVars := d.resolve(r)
	entry, err := d.acquireApp(key, module, dir, venv, envFiles, envVars)
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
	defer d.releaseApp(entry)
	return entry.app.HandleRequest(w, r)
}

// Cleanup frees all dynamically created apps and stops the autoreload watcher.
func (d *DynamicApp) Cleanup() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
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

	entries := make(map[*dynamicAppEntry]struct{}, len(d.apps)+len(d.retired))
	for key, entry := range d.apps {
		entry.retired = true
		entries[entry] = struct{}{}
		delete(d.apps, key)
	}
	for entry := range d.retired {
		entries[entry] = struct{}{}
		delete(d.retired, entry)
	}
	for {
		busy := false
		for entry := range entries {
			if entry.active > 0 {
				busy = true
				break
			}
		}
		if !busy {
			break
		}
		d.drain.Wait()
	}
	d.mu.Unlock()

	var errs []error
	for entry := range entries {
		if err := entry.cleanup(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
