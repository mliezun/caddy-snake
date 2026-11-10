package caddysnake

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// startSlowRequest runs one request against a dynamic app whose handler blocks
// until release is closed. It returns once the handler is running.
func startSlowRequest(t *testing.T, drainTimeout time.Duration) (d *DynamicApp, key string, cleaned *atomic.Bool, release chan struct{}, finished chan struct{}) {
	t.Helper()
	dir := mkdirDynamicTestDir(t, "app")
	cleaned = &atomic.Bool{}
	release = make(chan struct{})
	finished = make(chan struct{})
	started := make(chan struct{})

	d, err := NewDynamicApp("main:app", dir, "", nil, nil,
		func(module, dir, venv string, envFiles []string, envVars map[string]string) (AppServer, error) {
			return &mockAppServer{
				onCleanup: func() { cleaned.Store(true) },
				onHandleRequest: func(w http.ResponseWriter, r *http.Request) error {
					close(started)
					<-release
					return nil
				},
			}, nil
		},
		zap.NewNop(), false, nil, defaultDynamicAppLimits(),
	)
	if err != nil {
		t.Fatalf("NewDynamicApp: %v", err)
	}
	d.drainTimeout = drainTimeout
	t.Cleanup(func() { _ = d.Cleanup() })

	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	key, _, _, _, _, _ = d.resolve(r)
	go func() {
		defer close(finished)
		_ = d.HandleRequest(httptest.NewRecorder(), r)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	return d, key, cleaned, release, finished
}

// evict removes the app the way a reload does and schedules its cleanup.
func evict(t *testing.T, d *DynamicApp, key string) {
	t.Helper()
	d.mu.Lock()
	app := d.removeAppLocked(key)
	d.mu.Unlock()
	if app == nil {
		t.Fatal("expected an app to evict")
	}
	d.cleanupAppsAsync([]AppServer{app})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDynamicAppReloadWaitsForRunningRequests(t *testing.T) {
	d, key, cleaned, release, finished := startSlowRequest(t, time.Minute)

	evict(t, d, key)

	// The request is still running: its workers must stay up.
	time.Sleep(150 * time.Millisecond)
	if cleaned.Load() {
		t.Fatal("app was cleaned up while a request was still running on it")
	}

	close(release)
	<-finished
	waitFor(t, "cleanup after the request finished", cleaned.Load)

	d.mu.Lock()
	leftover := len(d.active)
	d.mu.Unlock()
	if leftover != 0 {
		t.Fatalf("active map still tracks %d evicted instance(s)", leftover)
	}
}

func TestDynamicAppReloadDrainTimeout(t *testing.T) {
	d, key, cleaned, release, finished := startSlowRequest(t, 50*time.Millisecond)

	evict(t, d, key)

	// The request never finishes on its own; the drain timeout bounds the wait.
	waitFor(t, "cleanup after the drain timeout", cleaned.Load)

	close(release)
	<-finished
}

func TestDynamicAppIdleInstanceCleansUpImmediately(t *testing.T) {
	d, key, cleaned, release, finished := startSlowRequest(t, time.Minute)
	close(release)
	<-finished

	evict(t, d, key)
	// Nothing is running, so there is nothing to wait for.
	waitFor(t, "immediate cleanup of an idle instance", cleaned.Load)
}

func TestParseReloadDrainTimeout(t *testing.T) {
	cases := map[string]time.Duration{
		"":      defaultReloadDrainTimeout,
		"5s":    5 * time.Second,
		"0":     0,
		"-1s":   defaultReloadDrainTimeout,
		"bogus": defaultReloadDrainTimeout,
	}
	for raw, want := range cases {
		t.Setenv(envReloadDrainTimeout, raw)
		if got := parseReloadDrainTimeout(); got != want {
			t.Errorf("parseReloadDrainTimeout(%q) = %v, want %v", raw, got, want)
		}
	}
}
