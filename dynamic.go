package caddysnake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

var (
	ErrDynamicAppCapacity = errors.New("dynamic app limit reached")
	ErrDynamicCreateLimit = errors.New("too many concurrent dynamic app creations")
)

const (
	defaultMaxDynamicApps        = 128
	defaultDynamicMaxConcurrency = 4
	defaultDynamicFailureTTL     = 5 * time.Second
)

// DynamicAppLimits configures capacity and backoff for dynamic app loading.
type DynamicAppLimits struct {
	MaxApps        int
	MaxConcurrency int
	FailureTTL     time.Duration
}

func defaultDynamicAppLimits() DynamicAppLimits {
	return DynamicAppLimits{
		MaxApps:        defaultMaxDynamicApps,
		MaxConcurrency: defaultDynamicMaxConcurrency,
		FailureTTL:     defaultDynamicFailureTTL,
	}
}

func normalizeDynamicAppLimits(l DynamicAppLimits) DynamicAppLimits {
	d := defaultDynamicAppLimits()
	if l.MaxApps > 0 {
		d.MaxApps = l.MaxApps
	}
	if l.MaxConcurrency > 0 {
		d.MaxConcurrency = l.MaxConcurrency
	}
	if l.FailureTTL > 0 {
		d.FailureTTL = l.FailureTTL
	}
	return d
}

func parseDynamicAppLimits(maxApps, maxConcurrency, failureTTL string) (DynamicAppLimits, error) {
	lim := defaultDynamicAppLimits()
	if maxApps != "" {
		n, err := strconv.Atoi(maxApps)
		if err != nil || n <= 0 {
			return lim, fmt.Errorf("invalid max_dynamic_apps: %q", maxApps)
		}
		lim.MaxApps = n
	}
	if maxConcurrency != "" {
		n, err := strconv.Atoi(maxConcurrency)
		if err != nil || n <= 0 {
			return lim, fmt.Errorf("invalid dynamic_max_concurrency: %q", maxConcurrency)
		}
		lim.MaxConcurrency = n
	}
	if failureTTL != "" {
		d, err := caddy.ParseDuration(failureTTL)
		if err != nil || d <= 0 {
			return lim, fmt.Errorf("invalid dynamic_failure_ttl: %q", failureTTL)
		}
		lim.FailureTTL = d
	}
	return lim, nil
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
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("invalid working directory: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("working directory does not exist: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("working directory is not a directory: %q", abs)
		}
	}
	if venv != "" {
		if hasDotDotSegment(venv) {
			return fmt.Errorf("venv path contains path traversal: %q", venv)
		}
		abs, err := filepath.Abs(venv)
		if err != nil {
			return fmt.Errorf("invalid venv path: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("venv path does not exist: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("venv path is not a directory: %q", abs)
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

type dynamicKeyEnvPair struct {
	Key   string `json:"k"`
	Value string `json:"v"`
}

type dynamicKeyPayload struct {
	Module   string              `json:"module"`
	Dir      string              `json:"dir"`
	Venv     string              `json:"venv"`
	EnvFiles []string            `json:"env_files,omitempty"`
	EnvVars  []dynamicKeyEnvPair `json:"env_vars,omitempty"`
}

func dynamicAppCacheKey(module, dir, venv string, envFiles []string, envVars map[string]string) string {
	payload := dynamicKeyPayload{
		Module:   module,
		Dir:      dir,
		Venv:     venv,
		EnvFiles: append([]string(nil), envFiles...),
	}
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			payload.EnvVars = append(payload.EnvVars, dynamicKeyEnvPair{Key: k, Value: envVars[k]})
		}
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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

// DynamicApp implements AppServer by lazily importing Python apps based on
// Caddy placeholders resolved at request time. For example, when working_dir
// contains {host.labels.2}, each subdomain gets its own Python app instance
// imported from the corresponding directory.
type DynamicApp struct {
	mu              sync.RWMutex
	apps            map[string]AppServer
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
	limits          DynamicAppLimits
	createSem       chan struct{}

	// Autoreload fields
	autoreload          bool
	watcher             *fsnotify.Watcher
	dirToKeys           map[string][]string // abs working dir -> cache keys that use it
	stopCh              chan struct{}
	stopOnce            sync.Once
	cleanupMu           sync.Mutex
	pendingCleanups     []context.CancelFunc
	exitOnReloadFailure func(code int) // if set and autoreload, process exits when app creation fails
}

// NewDynamicApp creates a DynamicApp that resolves placeholders from
// modulePattern, workingDir, venvPath, env_file paths, and env_var values at
// request time and lazily creates Python app instances via the supplied factory.
// When autoreload is true, if exitOnReloadFailure is non-nil it is called with
// code 1 when app creation fails (e.g. app deleted), so the process can terminate.
func NewDynamicApp(modulePattern, workingDir, venvPath string, envFilePatterns []string, envVarPatterns map[string]string, factory appFactory, logger *zap.Logger, autoreload bool, exitOnReloadFailure func(code int), limits DynamicAppLimits) (*DynamicApp, error) {
	limits = normalizeDynamicAppLimits(limits)
	d := &DynamicApp{
		apps:                make(map[string]AppServer),
		inflight:            make(map[string]*appCreate),
		failed:              make(map[string]failedAppCreate),
		modulePattern:       modulePattern,
		workingDir:          workingDir,
		venvPath:            venvPath,
		envFilePatterns:     cloneEnvFiles(envFilePatterns),
		envVarPatterns:      cloneEnvVars(envVarPatterns),
		factory:             factory,
		logger:              logger,
		limits:              limits,
		createSem:           make(chan struct{}, limits.MaxConcurrency),
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

func (d *DynamicApp) pendingCreateCountLocked() int {
	pending := 0
	for k := range d.inflight {
		if _, exists := d.apps[k]; !exists {
			pending++
		}
	}
	return pending
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
	d.failed[key] = failedAppCreate{err: err, expiresAt: now.Add(d.limits.FailureTTL)}
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

	key = dynamicAppCacheKey(module, dir, venv, envFiles, envVars)
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

	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return nil, errors.New("dynamic app shutting down")
	}
	app, ok := d.apps[key]
	if ok {
		d.mu.RUnlock()
		return app, nil
	}
	if f, failed := d.failed[key]; failed && time.Now().Before(f.expiresAt) {
		err := f.err
		d.mu.RUnlock()
		return nil, err
	}
	d.mu.RUnlock()

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("dynamic app shutting down")
	}
	d.pruneExpiredFailuresLocked(time.Now())
	app, ok = d.apps[key]
	if ok {
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
	if len(d.apps)+d.pendingCreateCountLocked() >= d.limits.MaxApps {
		d.mu.Unlock()
		return nil, ErrDynamicAppCapacity
	}
	select {
	case d.createSem <- struct{}{}:
	default:
		d.mu.Unlock()
		return nil, ErrDynamicCreateLimit
	}
	c := &appCreate{done: make(chan struct{})}
	d.inflight[key] = c
	d.mu.Unlock()

	releaseCreate := func() { <-d.createSem }

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
				releaseCreate()
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

	releaseCreate()

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
		d.apps[key] = app
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
				if strings.HasPrefix(event.Name, absDir+string(os.PathSeparator)) ||
					strings.HasPrefix(event.Name, absDir) {
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

func (d *DynamicApp) cancelPendingCleanups() {
	d.cleanupMu.Lock()
	for _, cancel := range d.pendingCleanups {
		cancel()
	}
	d.pendingCleanups = nil
	d.cleanupMu.Unlock()
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
		if app, exists := d.apps[key]; exists {
			oldApps = append(oldApps, app)
			delete(d.apps, key)
		}
		delete(d.failed, key)
	}

	delete(d.dirToKeys, absDir)

	d.mu.Unlock()

	d.cancelPendingCleanups()

	d.logger.Info("dynamic python apps evicted, will reimport on next request",
		zap.String("working_dir", absDir),
		zap.Int("apps_evicted", len(oldApps)),
	)

	if len(oldApps) > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		d.cleanupMu.Lock()
		d.pendingCleanups = append(d.pendingCleanups, cancel)
		d.cleanupMu.Unlock()
		go func() {
			defer cancel()
			select {
			case <-time.After(10 * time.Second):
				for _, app := range oldApps {
					if err := app.Cleanup(); err != nil {
						d.logger.Error("failed to cleanup old dynamic app",
							zap.Error(err),
						)
					}
				}
			case <-ctx.Done():
				for _, app := range oldApps {
					if err := app.Cleanup(); err != nil {
						d.logger.Error("failed to cleanup cancelled dynamic app",
							zap.Error(err),
						)
					}
				}
			}
		}()
	}
}

// HandleRequest resolves placeholders from the request, gets or creates the
// appropriate app, and forwards the request.
func (d *DynamicApp) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	key, module, dir, venv, envFiles, envVars := d.resolve(r)
	app, err := d.getOrCreateApp(key, module, dir, venv, envFiles, envVars)
	if err != nil {
		if errors.Is(err, ErrDynamicAppCapacity) || errors.Is(err, ErrDynamicCreateLimit) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return nil
		}
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
	return app.HandleRequest(w, r)
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

	d.stopOnce.Do(func() {
		if d.autoreload && d.stopCh != nil {
			close(d.stopCh)
		}
	})
	if d.autoreload && d.watcher != nil {
		d.watcher.Close()
	}

	d.cancelPendingCleanups()

	var errs []error
	for key, app := range d.apps {
		if err := app.Cleanup(); err != nil {
			errs = append(errs, err)
		}
		delete(d.apps, key)
	}
	d.failed = make(map[string]failedAppCreate)
	d.mu.Unlock()
	return errors.Join(errs...)
}
