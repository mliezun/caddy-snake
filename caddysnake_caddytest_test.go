//go:build caddytest

package caddysnake

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/caddytest"
)

// TestProvision_WSGI_ServesRequests and TestProvision_ASGI_ServesRequests use
// caddytest, which starts Caddy in-process. Caddy does not exit when tests
// complete, so these tests are excluded from the default test run to avoid
// "Test I/O incomplete" / "WaitDelay expired" failures. Run them explicitly with:
//
//	go test -race -v -tags=caddytest .
//
// Note: The process may not exit cleanly after these tests; use a timeout if
// running in CI: go test -race -v -tags=caddytest -timeout 90s .

func TestProvision_WSGI_ServesRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	skipIfNoPython(t)

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "app.py"), []byte(minimalWSGIApp), 0644); err != nil {
		t.Fatalf("failed to write app.py: %v", err)
	}
	workDir := filepath.ToSlash(tempDir)

	caddyfile := fmt.Sprintf(`
{
  admin localhost:2999
  http_port 9080
  https_port 9443
  grace_period 1ns
}

localhost:9080 {
  route / {
    python {
      module_wsgi "app:app"
      working_dir %q
      workers 1
    }
  }
}
`, workDir)

	tester := caddytest.NewTester(t)
	tester.WithDefaultOverrides(caddytest.Config{
		LoadRequestTimeout: 15 * time.Second,
	})
	tester.InitServer(caddyfile, "caddyfile")
	tester.AssertGetResponse("http://localhost:9080/", 200, "Hello from Python")
}

func TestProvision_ASGI_ServesRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	skipIfNoPython(t)

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "app.py"), []byte(minimalASGIApp), 0644); err != nil {
		t.Fatalf("failed to write app.py: %v", err)
	}
	workDir := filepath.ToSlash(tempDir)

	caddyfile := fmt.Sprintf(`
{
  admin localhost:2999
  http_port 9080
  https_port 9443
  grace_period 1ns
}

localhost:9080 {
  route / {
    python {
      module_asgi "app:app"
      working_dir %q
      workers 1
    }
  }
}
`, workDir)

	tester := caddytest.NewTester(t)
	tester.WithDefaultOverrides(caddytest.Config{
		LoadRequestTimeout: 15 * time.Second,
	})
	tester.InitServer(caddyfile, "caddyfile")
	tester.AssertGetResponse("http://localhost:9080/", 200, "Hello from ASGI")
}
