package caddysnake

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// containsPlaceholder checks if a string contains Caddy placeholders (e.g. {host.labels.0}).
func containsPlaceholder(s string) bool {
	return strings.Contains(s, "{") && strings.Contains(s, "}")
}

// appFactory is a function that creates a new AppServer for a resolved
// module, working directory and venv path combination.
type appFactory func(resolvedModule, resolvedDir, resolvedVenv string) (AppServer, error)

// DynamicApp implements AppServer by lazily importing Python apps based on
// Caddy placeholders resolved at request time. For example, when working_dir
// contains {host.labels.2}, each subdomain gets its own Python app instance
// imported from the corresponding directory.
type DynamicApp struct {
	mu            sync.RWMutex
	apps          map[string]AppServer
	modulePattern string
	workingDir    string
	venvPath      string
	factory       appFactory
	logger        *zap.Logger

	// Autoreload fields
	autoreload bool
	watcher    *fsnotify.Watcher
	dirToKeys  map[string][]string // abs working dir -> cache keys that use it
	stopCh     chan struct{}
}

// NewDynamicApp creates a DynamicApp that resolves placeholders from
// modulePattern, workingDir, and venvPath at request time and lazily creates
// Python app instances via the supplied factory function.
func NewDynamicApp(modulePattern, workingDir, venvPath string, factory appFactory, logger *zap.Logger, autoreload bool) (*DynamicApp, error) {
	d := &DynamicApp{
		apps:          make(map[string]AppServer),
		modulePattern: modulePattern,
		workingDir:    workingDir,
		venvPath:      venvPath,
		factory:       factory,
		logger:        logger,
		autoreload:    autoreload,
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

// resolve uses the Caddy replacer from the request context to substitute
// placeholders in the module pattern, working directory, and venv path.
func (d *DynamicApp) resolve(r *http.Request) (key, module, dir, venv string) {
	module = d.modulePattern
	dir = d.workingDir
	venv = d.venvPath

	if repl, ok := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer); ok && repl != nil {
		module = repl.ReplaceAll(module, "")
		dir = repl.ReplaceAll(dir, "")
		venv = repl.ReplaceAll(venv, "")
	}

	key = module + "|" + dir + "|" + venv
	return
}

// getOrCreateApp returns an existing app for the given key, or creates one
// using the factory if it doesn't exist yet.
func (d *DynamicApp) getOrCreateApp(key, module, dir, venv string) (AppServer, error) {
	d.mu.RLock()
	app, ok := d.apps[key]
	d.mu.RUnlock()
	if ok {
		return app, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	app, ok = d.apps[key]
	if ok {
		return app, nil
	}

	d.logger.Info("dynamically importing python app",
		zap.String("module", module),
		zap.String("working_dir", dir),
		zap.String("venv", venv),
	)

	app, err := d.factory(module, dir, venv)
	if err != nil {
		return nil, err
	}

	d.apps[key] = app

	if d.autoreload && dir != "" {
		d.startWatchingDir(dir, key)
	}

	return app, nil
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
	}

	delete(d.dirToKeys, absDir)

	d.mu.Unlock()

	d.logger.Info("dynamic python apps evicted, will reimport on next request",
		zap.String("working_dir", absDir),
		zap.Int("apps_evicted", len(oldApps)),
	)

	if len(oldApps) > 0 {
		go func() {
			time.Sleep(10 * time.Second)
			for _, app := range oldApps {
				if err := app.Cleanup(); err != nil {
					d.logger.Error("failed to cleanup old dynamic app",
						zap.Error(err),
					)
				}
			}
		}()
	}
}

// HandleRequest resolves placeholders from the request, gets or creates the
// appropriate app, and forwards the request.
func (d *DynamicApp) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	key, module, dir, venv := d.resolve(r)
	app, err := d.getOrCreateApp(key, module, dir, venv)
	if err != nil {
		return err
	}
	return app.HandleRequest(w, r)
}

// Cleanup frees all dynamically created apps and stops the autoreload watcher.
func (d *DynamicApp) Cleanup() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.autoreload && d.stopCh != nil {
		close(d.stopCh)
		d.watcher.Close()
	}

	var errs []error
	for key, app := range d.apps {
		if err := app.Cleanup(); err != nil {
			errs = append(errs, err)
		}
		delete(d.apps, key)
	}
	return errors.Join(errs...)
}
