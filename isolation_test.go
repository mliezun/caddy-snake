package caddysnake

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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

func TestEffectiveBackend(t *testing.T) {
	var nilCfg *IsolationConfig
	if got := nilCfg.effectiveBackend(); got != isolationBackendNone {
		t.Fatalf("nil = %q", got)
	}
	empty := &IsolationConfig{}
	if got := empty.effectiveBackend(); got != isolationBackendNone {
		t.Fatalf("empty = %q", got)
	}
	docker := &IsolationConfig{Backend: isolationBackendDocker}
	if got := docker.effectiveBackend(); got != isolationBackendDocker {
		t.Fatalf("docker = %q", got)
	}
}

func TestCloneIsolationConfig(t *testing.T) {
	if cloneIsolationConfig(nil) != nil {
		t.Fatal("nil clone should be nil")
	}
	src := &IsolationConfig{
		Backend: isolationBackendDocker,
		Docker: &DockerIsolationConfig{
			Image:  "python:3.13-slim",
			Mounts: []IsolationMount{{Host: "/a", Container: "/b", Mode: "ro"}},
		},
	}
	dst := cloneIsolationConfig(src)
	if dst == src || dst.Docker == src.Docker {
		t.Fatal("expected deep copy")
	}
	dst.Docker.Image = "mutated"
	if src.Docker.Image == "mutated" {
		t.Fatal("clone should not alias docker config")
	}
	dst.Docker.Mounts[0].Host = "mutated"
	if src.Docker.Mounts[0].Host == "mutated" {
		t.Fatal("clone should not alias mounts slice")
	}
}

func TestNewWorkerBackend(t *testing.T) {
	b, err := newWorkerBackend(nil)
	if err != nil || b == nil {
		t.Fatalf("nil isolation: backend=%T err=%v", b, err)
	}
	b, err = newWorkerBackend(&IsolationConfig{Backend: isolationBackendNone})
	if err != nil || b == nil {
		t.Fatalf("none: backend=%T err=%v", b, err)
	}
	b, err = newWorkerBackend(&IsolationConfig{
		Backend: isolationBackendDocker,
		Docker:  &DockerIsolationConfig{Image: "python:3.13-slim"},
	})
	if err != nil || b == nil {
		t.Fatalf("docker: backend=%T err=%v", b, err)
	}
	if _, err := newWorkerBackend(&IsolationConfig{Backend: isolationBackendDocker}); err == nil {
		t.Fatal("docker without config should fail")
	}
	if _, err := newWorkerBackend(&IsolationConfig{Backend: "lxc"}); err == nil {
		t.Fatal("unsupported backend should fail")
	}
}

func TestNewDockerBackend(t *testing.T) {
	if _, err := newDockerBackend(nil); err == nil {
		t.Fatal("expected error for nil config")
	}
	b, err := newDockerBackend(&DockerIsolationConfig{Image: "python:3.13-slim"})
	if err != nil || b == nil {
		t.Fatalf("newDockerBackend: %v", err)
	}
}

func TestDockerWorkerContainerName(t *testing.T) {
	name := dockerWorkerContainerName("3")
	if !strings.HasPrefix(name, dockerWorkerNamePrefix+"3-") {
		t.Fatalf("unexpected name %q", name)
	}
}

func TestDockerBackend_dockerEnv(t *testing.T) {
	b := dockerBackend{cfg: &DockerIsolationConfig{}}
	if env := b.dockerEnv(); env != nil {
		t.Fatalf("empty host: %v", env)
	}
	b.cfg.DockerHost = "unix:///var/run/docker.sock"
	env := b.dockerEnv()
	if len(env) != 1 || env[0] != "DOCKER_HOST=unix:///var/run/docker.sock" {
		t.Fatalf("dockerEnv = %v", env)
	}
}

func TestDockerBackend_Stop(t *testing.T) {
	b := dockerBackend{cfg: &DockerIsolationConfig{Image: "python:3.13-slim"}}
	portDir := t.TempDir()
	h := &dockerWorkerHandle{
		containerID: "nonexistent-container-id",
		portDir:     portDir,
		dialNet:     "tcp",
		dialAddr:    "127.0.0.1:1",
		exited:      make(chan error),
	}
	if err := b.Stop(h, time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(portDir); err == nil {
		t.Fatal("portDir should be removed")
	}
	if err := b.Stop(fakeWorkerHandle{}, time.Second); err == nil {
		t.Fatal("invalid handle should error")
	}
}

func TestDockerWorkerHandleAccessors(t *testing.T) {
	exited := make(chan error, 1)
	h := &dockerWorkerHandle{dialNet: "tcp", dialAddr: "10.0.0.2:8080", exited: exited}
	if h.DialNetwork() != "tcp" || h.DialAddress() != "10.0.0.2:8080" || h.Exited() != exited {
		t.Fatal("accessor mismatch")
	}
}

func TestParseIsolationCaddyfile_FullDocker(t *testing.T) {
	input := `python {
		module_wsgi "main:app"
		isolation docker {
			image "python:3.13-slim"
			network "host"
			docker_host "unix:///var/run/docker.sock"
			memory "1g"
			cpus "2"
			read_only
			mount /data /data ro
			mount /tmp/scratch /scratch rw
		}
	}`
	f := loadPythonHandlerFromCaddyfile(t, input)
	d := f.Isolation.Docker
	if d.Image != "python:3.13-slim" || d.Network != "host" || d.DockerHost != "unix:///var/run/docker.sock" {
		t.Fatalf("docker config = %#v", d)
	}
	if d.Memory != "1g" || d.CPUs != "2" || !d.ReadOnly || len(d.Mounts) != 2 {
		t.Fatalf("resources/mounts = %#v", d)
	}
}

func TestValidateIsolation_MountErrors(t *testing.T) {
	m := &CaddySnake{
		ModuleWsgi: "main:app",
		Isolation: &IsolationConfig{
			Backend: isolationBackendDocker,
			Docker: &DockerIsolationConfig{
				Image:  "python:3.13-slim",
				Mounts: []IsolationMount{{Host: "", Container: "/x"}},
			},
		},
	}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "mount 0") {
		t.Fatalf("Validate() = %v", err)
	}
	m.Isolation.Docker.Mounts = []IsolationMount{{Host: "/h", Container: "/c", Mode: "invalid"}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestCacheAddrForContainer_EdgeCases(t *testing.T) {
	if got := cacheAddrForContainer(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	unix := cacheAddrUnixScheme + "/tmp/cs.sock"
	if got := cacheAddrForContainer(unix); got != unix {
		t.Fatalf("unix: %q", got)
	}
	if got := cacheAddrForContainer("127.0.0.1:9999"); got != "host.docker.internal:9999" {
		t.Fatalf("loopback: %q", got)
	}
	if got := cacheAddrForContainer("localhost:1234"); got != "host.docker.internal:1234" {
		t.Fatalf("localhost: %q", got)
	}
	if got := cacheAddrForContainer("10.0.0.5:4000"); got != "10.0.0.5:4000" {
		t.Fatalf("remote host: %q", got)
	}
	if got := cacheAddrForContainer("not-an-addr"); got != "not-an-addr" {
		t.Fatalf("invalid addr: %q", got)
	}
}

func TestBuildIsolationFromCLI(t *testing.T) {
	if iso, err := buildIsolationFromCLI("", "", "", "", "", "", false); err != nil || iso != nil {
		t.Fatalf("empty backend: iso=%#v err=%v", iso, err)
	}
	iso, err := buildIsolationFromCLI("none", "", "", "", "", "", false)
	if err != nil || iso.Backend != isolationBackendNone {
		t.Fatalf("none: iso=%#v err=%v", iso, err)
	}
	if _, err := buildIsolationFromCLI("lxc", "", "", "", "", "", false); err == nil {
		t.Fatal("invalid backend should fail")
	}
	if _, err := buildIsolationFromCLI("docker", "", "", "", "", "", false); err == nil {
		t.Fatal("docker without image should fail")
	}
}

func TestBuildWorkerEnvForIsolation_ProcessIncludesHostEnv(t *testing.T) {
	t.Setenv("CADDYSNAKE_ISOLATION_PROBE_SECRET", "host-secret")
	spec := WorkerSpec{
		Interface: "wsgi",
		CacheAddr: "127.0.0.1:9999",
		WorkerID:  "0",
		Isolation: &IsolationConfig{Backend: isolationBackendNone},
	}
	env := buildWorkerEnvForIsolation(spec, nil)
	if !strings.Contains(strings.Join(env, "\n"), "CADDYSNAKE_ISOLATION_PROBE_SECRET=host-secret") {
		t.Fatal("process mode should inherit host env")
	}
}

func TestStartCacheServerForIsolation(t *testing.T) {
	s, err := startCacheServerForIsolation(&IsolationConfig{
		Backend: isolationBackendDocker,
		Docker:  &DockerIsolationConfig{Image: "python:3.13-slim"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !strings.HasPrefix(s.Addr(), "127.0.0.1:") {
		t.Fatalf("docker isolation should force TCP cache addr, got %q", s.Addr())
	}
}

func TestDockerCLIHelpers(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	b := dockerBackend{cfg: &DockerIsolationConfig{DockerHost: ""}}
	ctx := context.Background()
	if _, err := b.runDocker(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		t.Fatalf("runDocker: %v", err)
	}
	if err := b.waitDocker(ctx, "definitely-not-a-container"); err == nil {
		t.Fatal("waitDocker should fail for missing container")
	}
	if _, err := b.containerIP(ctx, "definitely-not-a-container"); err == nil {
		t.Fatal("containerIP should fail for missing container")
	}
	if err := b.stopContainer(ctx, "definitely-not-a-container", time.Second); err == nil {
		t.Fatal("stopContainer should fail for missing container")
	}
	if err := b.removeContainer(ctx, "definitely-not-a-container"); err != nil {
		t.Fatalf("removeContainer missing container: %v", err)
	}
	ids, err := ListDockerWorkerContainers(ctx, "")
	if err != nil {
		t.Fatalf("ListDockerWorkerContainers: %v", err)
	}
	_ = ids
}

func TestIsolationConfig_validateNil(t *testing.T) {
	var ic *IsolationConfig
	if err := ic.validate(); err != nil {
		t.Fatalf("nil validate: %v", err)
	}
}

type fakeWorkerHandle struct{}

func (fakeWorkerHandle) DialNetwork() string  { return "" }
func (fakeWorkerHandle) DialAddress() string  { return "" }
func (fakeWorkerHandle) Exited() <-chan error { return nil }

func loadPythonHandlerFromCaddyfile(t *testing.T, input string) CaddySnake {
	t.Helper()
	d := caddyfile.NewTestDispenser(input)
	var f CaddySnake
	if err := f.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile: %v", err)
	}
	return f
}
