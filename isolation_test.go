package caddysnake

import (
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestParseIsolationCaddyfile_Docker(t *testing.T) {
	input := `python {
		module_wsgi "main:app"
		isolation docker {
			image "python:3.13-slim"
			network "bridge"
			memory "512m"
		}
	}`
	f := loadPythonHandlerFromCaddyfile(t, input)
	if f.Isolation == nil || f.Isolation.Backend != isolationBackendDocker {
		t.Fatalf("Isolation = %#v", f.Isolation)
	}
	if f.Isolation.Docker.Image != "python:3.13-slim" {
		t.Fatalf("image = %q", f.Isolation.Docker.Image)
	}
	if f.Isolation.Docker.Network != "bridge" {
		t.Fatalf("network = %q", f.Isolation.Docker.Network)
	}
	if f.Isolation.Docker.Memory != "512m" {
		t.Fatalf("memory = %q", f.Isolation.Docker.Memory)
	}
}

func TestParseIsolationCaddyfile_None(t *testing.T) {
	input := `python {
		module_wsgi "main:app"
		isolation none
	}`
	f := loadPythonHandlerFromCaddyfile(t, input)
	if f.Isolation == nil || f.Isolation.Backend != isolationBackendNone {
		t.Fatalf("Isolation = %#v", f.Isolation)
	}
}

func TestValidateIsolation_DockerRequiresImage(t *testing.T) {
	m := &CaddySnake{
		ModuleWsgi: "main:app",
		Isolation: &IsolationConfig{
			Backend: isolationBackendDocker,
			Docker:  &DockerIsolationConfig{},
		},
	}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("Validate() = %v, want image error", err)
	}
}

func TestValidateIsolation_UnknownBackend(t *testing.T) {
	m := &CaddySnake{
		ModuleWsgi: "main:app",
		Isolation:  &IsolationConfig{Backend: "firecracker"},
	}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "unknown isolation backend") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestBuildIsolationFromCLI_Docker(t *testing.T) {
	iso, err := buildIsolationFromCLI("docker", "python:3.13-slim", "", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if iso == nil || !iso.usesDocker() {
		t.Fatalf("iso = %#v", iso)
	}
}

func TestCacheAddrForContainer(t *testing.T) {
	got := cacheAddrForContainer("127.0.0.1:12345")
	if got != "host.docker.internal:12345" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildWorkerEnvForIsolation_DockerOmitsHostEnv(t *testing.T) {
	t.Setenv("CADDYSNAKE_ISOLATION_PROBE_SECRET", "host-secret")
	spec := WorkerSpec{
		Interface: "wsgi",
		CacheAddr: "127.0.0.1:9999",
		WorkerID:  "0",
		Isolation: &IsolationConfig{Backend: isolationBackendDocker, Docker: &DockerIsolationConfig{Image: "python:3.13-slim"}},
		EnvVars:   map[string]string{"APP_ENV": "test"},
	}
	env := buildWorkerEnvForIsolation(spec, nil)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "CADDYSNAKE_ISOLATION_PROBE_SECRET=host-secret") {
		t.Fatalf("host env leaked into docker worker env: %s", joined)
	}
	if !strings.Contains(joined, "APP_ENV=test") {
		t.Fatalf("missing configured env var: %s", joined)
	}
	if !strings.Contains(joined, envCaddysnakeWorkerTCP+"=1") {
		t.Fatalf("missing tcp env: %s", joined)
	}
	if !strings.Contains(joined, "host.docker.internal:9999") {
		t.Fatalf("cache addr not rewritten: %s", joined)
	}
}

func loadPythonHandlerFromCaddyfile(t *testing.T, input string) CaddySnake {
	t.Helper()
	d := caddyfile.NewTestDispenser(input)
	var f CaddySnake
	if err := f.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile: %v", err)
	}
	return f
}
