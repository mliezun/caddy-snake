//go:build caddytest

package caddysnake

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
//
// For end-to-end coverage of tls.permission.python_dir with on-demand TLS (no ACME):
//
//	go test -race -v -tags=caddytest . -run TestPythonDir_OnDemandDynamicASGI_OverHTTPS -timeout 90s

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

func TestPythonDir_OnDemandDynamicASGI_OverHTTPS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	skipIfNoPython(t)

	branches := t.TempDir()
	slug := "alpha1"
	appDir := filepath.Join(branches, slug)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "app.py"), []byte(minimalASGIApp), 0644); err != nil {
		t.Fatalf("failed to write app.py: %v", err)
	}

	brSlash := filepath.ToSlash(branches)

	// Internal CA + on_demand TLS exercises CertificateAllowed in-process (no public ACME).
	// skip_install_trust avoids failures on headless CI when Caddy cannot install the local CA.
	// For host alpha1.srv.test.local (four labels), Caddy indexes from the right:
	// labels.0=local, labels.1=test, labels.2=srv, labels.3=alpha1.
	caddyfile := fmt.Sprintf(`
{
  admin localhost:2999
  http_port 9080
  https_port 9443
  grace_period 1ns
  skip_install_trust

  on_demand_tls {
    permission python_dir {
      root %q
      domain_suffix srv.test.local
    }
  }
}

https:// {
  tls internal {
    on_demand
  }

  route / {
    python {
      module_asgi "app:app"
      working_dir "%s/{http.request.host.labels.3}"
      workers 1
    }
  }
}
`, branches, brSlash)

	tester := caddytest.NewTester(t)
	tester.WithDefaultOverrides(caddytest.Config{
		LoadRequestTimeout: 20 * time.Second,
		TestRequestTimeout: 20 * time.Second,
	})

	tester.InitServer(caddyfile, "caddyfile")

	host := slug + ".srv.test.local"
	url := fmt.Sprintf("https://%s:9443/", host)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host

	resp, err := tester.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if string(body) != "Hello from ASGI" {
		t.Fatalf("unexpected body %q", string(body))
	}
}
