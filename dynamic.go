package caddysnake

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
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
}

// NewDynamicApp creates a DynamicApp that resolves placeholders from
// modulePattern, workingDir, and venvPath at request time and lazily creates
// Python app instances via the supplied factory function.
func NewDynamicApp(modulePattern, workingDir, venvPath string, factory appFactory, logger *zap.Logger) *DynamicApp {
	return &DynamicApp{
		apps:          make(map[string]AppServer),
		modulePattern: modulePattern,
		workingDir:    workingDir,
		venvPath:      venvPath,
		factory:       factory,
		logger:        logger,
	}
}

// resolve uses the Caddy replacer from the request context to substitute
// placeholders in the module pattern, working directory, and venv path.
// It returns a composite cache key along with the three resolved values.
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
// using the factory if it doesn't exist yet. Uses double-check locking to
// allow concurrent reads while serializing creation.
func (d *DynamicApp) getOrCreateApp(key, module, dir, venv string) (AppServer, error) {
	// Fast path: read lock
	d.mu.RLock()
	app, ok := d.apps[key]
	d.mu.RUnlock()
	if ok {
		return app, nil
	}

	// Slow path: write lock with double check
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
	return app, nil
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

// Cleanup frees all dynamically created apps.
func (d *DynamicApp) Cleanup() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var errs []error
	for key, app := range d.apps {
		if err := app.Cleanup(); err != nil {
			errs = append(errs, err)
		}
		delete(d.apps, key)
	}
	return errors.Join(errs...)
}
