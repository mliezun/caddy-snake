package caddysnake

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
	"go.uber.org/zap"
)

// This file is the small surface other Go packages use to look at, and nudge,
// dynamically loaded apps: which ones are loaded, how much memory they use, and
// "reload the app in this directory".

const envMemoryCheckInterval = "CADDYSNAKE_MEMORY_CHECK_INTERVAL"

// defaultMemoryCheckInterval is how often apps are measured against memory_limit.
const defaultMemoryCheckInterval = 5 * time.Second

func parseMemoryCheckInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envMemoryCheckInterval))
	if raw == "" {
		return defaultMemoryCheckInterval
	}
	dur, err := time.ParseDuration(raw)
	if err != nil || dur <= 0 {
		return defaultMemoryCheckInterval
	}
	return dur
}

// parseMemoryLimit parses a memory_limit size ("256MiB", "1GB"). Empty means no limit.
func parseMemoryLimit(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := humanize.ParseBytes(s)
	if err != nil {
		return 0, fmt.Errorf("invalid memory_limit %q: %w", s, err)
	}
	if n == 0 {
		return 0, fmt.Errorf("memory_limit must be greater than 0, got %q", s)
	}
	if n > uint64(^uint64(0)>>1) {
		return 0, fmt.Errorf("memory_limit %q overflows int64", s)
	}
	return int64(n), nil
}

// memoryReporter is implemented by app servers that can measure their workers.
type memoryReporter interface {
	MemoryBytes() (int64, error)
}

// MemoryBytes returns the memory used by all workers in the group.
func (wg *PythonWorkerGroup) MemoryBytes() (int64, error) {
	var total int64
	for _, w := range wg.Workers {
		if w == nil {
			continue
		}
		ph, ok := w.handle.(interface{ Pid() int })
		if !ok {
			return 0, fmt.Errorf("worker memory is not measurable for this isolation backend")
		}
		n, err := processMemoryBytes(ph.Pid())
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// Pid returns the worker process id.
func (h *processWorkerHandle) Pid() int {
	if h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

var dynamicRegistry struct {
	mu   sync.Mutex
	apps map[*DynamicApp]struct{}
}

func registerDynamicApp(d *DynamicApp) {
	dynamicRegistry.mu.Lock()
	if dynamicRegistry.apps == nil {
		dynamicRegistry.apps = make(map[*DynamicApp]struct{})
	}
	dynamicRegistry.apps[d] = struct{}{}
	dynamicRegistry.mu.Unlock()
}

func unregisterDynamicApp(d *DynamicApp) {
	dynamicRegistry.mu.Lock()
	delete(dynamicRegistry.apps, d)
	dynamicRegistry.mu.Unlock()
}

func registeredDynamicApps() []*DynamicApp {
	dynamicRegistry.mu.Lock()
	defer dynamicRegistry.mu.Unlock()
	out := make([]*DynamicApp, 0, len(dynamicRegistry.apps))
	for d := range dynamicRegistry.apps {
		out = append(out, d)
	}
	return out
}

// DynamicAppInfo describes one loaded (awake) dynamic app instance.
type DynamicAppInfo struct {
	// WorkingDir is the resolved working directory (symlinks followed).
	WorkingDir string
	// MemoryBytes is the memory used by the app's workers, or -1 if unknown.
	MemoryBytes int64
	// ActiveRequests is the number of requests currently running on the app.
	ActiveRequests int
	// LastUsed is when the app last finished serving a request.
	LastUsed time.Time
}

type dynamicSnapshot struct {
	key      string
	app      AppServer
	dir      string
	inUse    int
	lastUsed time.Time
}

func (d *DynamicApp) snapshot() []dynamicSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]dynamicSnapshot, 0, len(d.apps))
	for key, app := range d.apps {
		out = append(out, dynamicSnapshot{
			key:      key,
			app:      app,
			dir:      d.appDirs[key],
			inUse:    d.inUse[key],
			lastUsed: d.lastUsed[key],
		})
	}
	return out
}

func appMemoryBytes(app AppServer) int64 {
	mr, ok := app.(memoryReporter)
	if !ok {
		return -1
	}
	n, err := mr.MemoryBytes()
	if err != nil {
		return -1
	}
	return n
}

// LoadedDynamicApps lists every dynamic app instance that is currently loaded,
// across all python handlers in this process.
func LoadedDynamicApps() []DynamicAppInfo {
	var out []DynamicAppInfo
	for _, d := range registeredDynamicApps() {
		for _, s := range d.snapshot() {
			out = append(out, DynamicAppInfo{
				WorkingDir:     s.dir,
				MemoryBytes:    appMemoryBytes(s.app),
				ActiveRequests: s.inUse,
				LastUsed:       s.lastUsed,
			})
		}
	}
	return out
}

func resolveDirBestEffort(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func dirIsOrUnder(dir, root string) bool {
	if dir == root {
		return true
	}
	return strings.HasPrefix(dir, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// ReloadWorkingDir unloads every dynamic app whose working directory is dir or
// lies under it, in all python handlers. Each app is imported again on its next
// request; requests already running finish first. It returns how many
// instances were unloaded.
//
// Use it after changing something autoreload does not watch, such as an
// env_file, or to restart an app on demand.
func ReloadWorkingDir(dir string) int {
	roots := []string{resolveDirBestEffort(dir)}
	if abs, err := filepath.Abs(dir); err == nil && abs != roots[0] {
		roots = append(roots, abs)
	}
	total := 0
	for _, d := range registeredDynamicApps() {
		total += d.evictWhere(func(s dynamicSnapshot) bool {
			for _, root := range roots {
				if s.dir != "" && dirIsOrUnder(s.dir, root) {
					return true
				}
			}
			return false
		})
	}
	return total
}

// evictWhere removes the instances matching pred and stops them once drained.
func (d *DynamicApp) evictWhere(pred func(dynamicSnapshot) bool) int {
	var old []AppServer
	for _, s := range d.snapshot() {
		if !pred(s) {
			continue
		}
		d.mu.Lock()
		// Only evict the instance we looked at, not a replacement created since.
		if cur, ok := d.apps[s.key]; ok && cur == s.app {
			if app := d.removeAppLocked(s.key); app != nil {
				old = append(old, app)
			}
			delete(d.failed, s.key)
		}
		d.mu.Unlock()
	}
	d.cleanupAppsAsync(old)
	return len(old)
}

// monitorMemory restarts apps that grow past the configured memory limit.
func (d *DynamicApp) monitorMemory(limit int64, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-d.monitorStop:
			return
		case <-ticker.C:
			d.enforceMemoryLimit(limit)
		}
	}
}

func (d *DynamicApp) enforceMemoryLimit(limit int64) {
	d.evictWhere(func(s dynamicSnapshot) bool {
		used := appMemoryBytes(s.app)
		if used < 0 || used <= limit {
			return false
		}
		msg := fmt.Sprintf("app used %s, over its memory_limit of %s; restarting it",
			humanize.IBytes(uint64(used)), humanize.IBytes(uint64(limit)))
		d.logger.Warn("python app exceeded memory_limit; restarting",
			zap.String("working_dir", s.dir),
			zap.Int64("memory_bytes", used),
			zap.Int64("memory_limit_bytes", limit),
		)
		emitWorkerOutput(WorkerOutput{
			WorkingDir: s.dir,
			Stream:     "caddy-snake",
			Line:       msg,
			Time:       time.Now(),
		})
		return true
	})
}
