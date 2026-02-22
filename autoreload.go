package caddysnake

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// watchDirRecursive adds all directories under root to the fsnotify watcher.
// It is used by both AutoreloadableApp and DynamicApp.
func watchDirRecursive(watcher *fsnotify.Watcher, root string, logger *zap.Logger) {
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if addErr := watcher.Add(path); addErr != nil {
			logger.Warn("autoreload: failed to watch directory",
				zap.String("path", path),
				zap.Error(addErr),
			)
		}
		return nil
	})
}

// isPythonFileEvent returns true if the event is a write/create/remove/rename
// of a .py file.
func isPythonFileEvent(event fsnotify.Event) bool {
	if filepath.Ext(event.Name) != ".py" {
		return false
	}
	return event.Has(fsnotify.Write) || event.Has(fsnotify.Create) ||
		event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)
}

// handleNewDirEvent checks if the event is a newly created directory and adds
// it to the watcher if appropriate.
func handleNewDirEvent(event fsnotify.Event, watcher *fsnotify.Watcher) {
	if !event.Has(fsnotify.Create) {
		return
	}
	info, err := os.Stat(event.Name)
	if err != nil || !info.IsDir() {
		return
	}
	watcher.Add(event.Name)
}

// AutoreloadableApp wraps an AppServer to support hot-reloading when Python
// files in the working directory change. It watches for .py file modifications
// and reloads the app after a debounce period to group rapid changes.
type AutoreloadableApp struct {
	mu         sync.RWMutex
	app        AppServer
	factory    func() (AppServer, error)
	watcher    *fsnotify.Watcher
	stopCh     chan struct{}
	logger     *zap.Logger
	workingDir string
}

// NewAutoreloadableApp creates an AutoreloadableApp that wraps the given app and
// starts a filesystem watcher on the working directory. When any .py file changes,
// the app is reloaded after a 500ms debounce window.
func NewAutoreloadableApp(
	app AppServer,
	workingDir string,
	factory func() (AppServer, error),
	logger *zap.Logger,
) (*AutoreloadableApp, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	a := &AutoreloadableApp{
		app:        app,
		factory:    factory,
		watcher:    watcher,
		stopCh:     make(chan struct{}),
		logger:     logger,
		workingDir: workingDir,
	}

	watchDirRecursive(watcher, workingDir, logger)

	go a.watch()

	logger.Info("autoreload enabled", zap.String("working_dir", workingDir))

	return a, nil
}

// watch runs in a goroutine and listens for filesystem events.
// It debounces rapid changes (e.g. editor save + format) into a single reload.
func (a *AutoreloadableApp) watch() {
	var debounceTimer *time.Timer
	const debounceDuration = 500 * time.Millisecond

	for {
		select {
		case event, ok := <-a.watcher.Events:
			if !ok {
				return
			}
			if !isPythonFileEvent(event) {
				handleNewDirEvent(event, a.watcher)
				continue
			}
			a.logger.Debug("python file changed",
				zap.String("file", event.Name),
				zap.String("op", event.Op.String()),
			)
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(debounceDuration, func() {
				a.reload()
			})
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}
			a.logger.Error("autoreload watcher error", zap.Error(err))
		case <-a.stopCh:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		}
	}
}

// reload performs the actual app reload by stopping the old worker processes
// and starting new ones via the factory function.
func (a *AutoreloadableApp) reload() {
	a.logger.Info("reloading python app due to file changes")
	a.mu.Lock()
	defer a.mu.Unlock()

	oldApp := a.app
	if err := oldApp.Cleanup(); err != nil {
		a.logger.Error("failed to cleanup old python app during reload", zap.Error(err))
	}

	newApp, err := a.factory()
	if err != nil {
		a.logger.Error("failed to reload python app, requests will return 500",
			zap.Error(err),
		)
		a.app = &errorApp{err: err}
		return
	}

	a.app = newApp
	a.logger.Info("python app reloaded successfully")
}

// HandleRequest forwards the request to the underlying app while holding a read
// lock. This ensures the app isn't swapped mid-request.
func (a *AutoreloadableApp) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.app.HandleRequest(w, r)
}

// Cleanup stops the filesystem watcher and cleans up the underlying app.
func (a *AutoreloadableApp) Cleanup() error {
	close(a.stopCh)
	a.watcher.Close()
	return a.app.Cleanup()
}

// errorApp is a stub AppServer returned when a reload fails.
// It returns HTTP 500 for all requests until the next successful reload.
type errorApp struct {
	err error
}

func (e *errorApp) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte("Python app reload failed: " + e.err.Error()))
	return nil
}

func (e *errorApp) Cleanup() error {
	return nil
}
