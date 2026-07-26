package caddysnake

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
)

const (
	isolationBackendNone   = "none"
	isolationBackendDocker = "docker"
)

// IsolationConfig selects how Python workers are run.
type IsolationConfig struct {
	Backend string                 `json:"backend,omitempty"`
	Docker  *DockerIsolationConfig `json:"docker,omitempty"`
}

// DockerIsolationConfig configures the Docker isolation backend.
type DockerIsolationConfig struct {
	Image      string           `json:"image,omitempty"`
	Network    string           `json:"network,omitempty"`
	DockerHost string           `json:"docker_host,omitempty"`
	Memory     string           `json:"memory,omitempty"`
	CPUs       string           `json:"cpus,omitempty"`
	ReadOnly   bool             `json:"read_only,omitempty"`
	Mounts     []IsolationMount `json:"mounts,omitempty"`
}

// IsolationMount is an extra bind mount for Docker workers.
type IsolationMount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	Mode      string `json:"mode,omitempty"` // ro or rw
}

func (ic *IsolationConfig) effectiveBackend() string {
	if ic == nil || ic.Backend == "" {
		return isolationBackendNone
	}
	return ic.Backend
}

func (ic *IsolationConfig) usesDocker() bool {
	return ic != nil && ic.effectiveBackend() == isolationBackendDocker
}

func (ic *IsolationConfig) validate() error {
	if ic == nil {
		return nil
	}
	switch ic.effectiveBackend() {
	case isolationBackendNone:
		return nil
	case isolationBackendDocker:
		if ic.Docker == nil || strings.TrimSpace(ic.Docker.Image) == "" {
			return fmt.Errorf("isolation docker requires image")
		}
		for i, m := range ic.Docker.Mounts {
			if strings.TrimSpace(m.Host) == "" || strings.TrimSpace(m.Container) == "" {
				return fmt.Errorf("isolation docker mount %d requires host and container paths", i)
			}
			mode := strings.ToLower(strings.TrimSpace(m.Mode))
			if mode != "" && mode != "ro" && mode != "rw" {
				return fmt.Errorf("isolation docker mount %d mode must be ro or rw", i)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown isolation backend %q", ic.Backend)
	}
}

func (m *CaddySnake) validateIsolation() error {
	if m.Isolation == nil {
		return nil
	}
	return m.Isolation.validate()
}

func parseIsolationCaddyfile(d *caddyfile.Dispenser, ic **IsolationConfig) error {
	var backend string
	if !d.Args(&backend) {
		return d.Errf("expected isolation backend or 'none'")
	}
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == isolationBackendNone {
		*ic = &IsolationConfig{Backend: isolationBackendNone}
		return nil
	}
	cfg := &IsolationConfig{Backend: backend}
	switch backend {
	case isolationBackendDocker:
		cfg.Docker = &DockerIsolationConfig{}
		for sub := d.Nesting(); d.NextBlock(sub); {
			switch d.Val() {
			case "image":
				if !d.Args(&cfg.Docker.Image) {
					return d.Errf("expected exactly one argument for image")
				}
			case "network":
				if !d.Args(&cfg.Docker.Network) {
					return d.Errf("expected exactly one argument for network")
				}
			case "docker_host":
				if !d.Args(&cfg.Docker.DockerHost) {
					return d.Errf("expected exactly one argument for docker_host")
				}
			case "memory":
				if !d.Args(&cfg.Docker.Memory) {
					return d.Errf("expected exactly one argument for memory")
				}
			case "cpus":
				if !d.Args(&cfg.Docker.CPUs) {
					return d.Errf("expected exactly one argument for cpus")
				}
			case "read_only":
				cfg.Docker.ReadOnly = true
			case "mount":
				var host, container, mode string
				switch d.CountRemainingArgs() {
				case 2:
					if !d.Args(&host, &container) {
						return d.ArgErr()
					}
				case 3:
					if !d.Args(&host, &container, &mode) {
						return d.ArgErr()
					}
				default:
					return d.Errf("expected two or three arguments for mount: host container [ro|rw]")
				}
				cfg.Docker.Mounts = append(cfg.Docker.Mounts, IsolationMount{
					Host:      host,
					Container: container,
					Mode:      mode,
				})
			default:
				return d.Errf("unknown isolation docker subdirective: %s", d.Val())
			}
		}
	default:
		return d.Errf("unknown isolation backend %q", backend)
	}
	*ic = cfg
	return nil
}

func cloneIsolationConfig(src *IsolationConfig) *IsolationConfig {
	if src == nil {
		return nil
	}
	dst := *src
	if src.Docker != nil {
		docker := *src.Docker
		if len(src.Docker.Mounts) > 0 {
			docker.Mounts = append([]IsolationMount(nil), src.Docker.Mounts...)
		}
		dst.Docker = &docker
	}
	return &dst
}

// WorkerSpec is the input to WorkerBackend.Start.
type WorkerSpec struct {
	Interface    string
	App          string
	WorkingDir   string
	Venv         string
	Lifespan     string
	Runtime      string
	PythonBin    string
	ScriptPath   string
	ScriptDir    string
	EnvFiles     []string
	EnvVars      map[string]string
	InternalEnv  []string
	WorkerID     string
	CacheAddr    string
	CacheToken   string
	WorkerToken  string
	StartTimeout time.Duration
	Isolation    *IsolationConfig
	Logger       *zap.Logger
}

// WorkerHandle is a running isolated or local worker.
type WorkerHandle interface {
	DialNetwork() string
	DialAddress() string
	Exited() <-chan error
}

// WorkerBackend starts and stops worker units.
type WorkerBackend interface {
	Start(ctx context.Context, spec WorkerSpec) (WorkerHandle, error)
	Stop(handle WorkerHandle, grace time.Duration) error
}

func newWorkerBackend(isolation *IsolationConfig) (WorkerBackend, error) {
	backend := isolationBackendNone
	if isolation != nil {
		backend = isolation.effectiveBackend()
	}
	switch backend {
	case isolationBackendNone:
		return processBackend{}, nil
	case isolationBackendDocker:
		if isolation == nil || isolation.Docker == nil {
			return nil, fmt.Errorf("docker isolation config is required")
		}
		return newDockerBackend(isolation.Docker)
	default:
		return nil, fmt.Errorf("unsupported isolation backend %q", backend)
	}
}

func cacheAddrForContainer(cacheAddr string) string {
	if cacheAddr == "" {
		return ""
	}
	if strings.HasPrefix(cacheAddr, cacheAddrUnixScheme) {
		return cacheAddr
	}
	host, port, err := net.SplitHostPort(cacheAddr)
	if err != nil {
		return cacheAddr
	}
	if host == "127.0.0.1" || host == "localhost" {
		return net.JoinHostPort("host.docker.internal", port)
	}
	return cacheAddr
}

func workerInternalEnvForIsolation(iface, cacheAddr, cacheToken, workerID, workerToken string, isolated bool) []string {
	addr := cacheAddr
	if isolated {
		addr = cacheAddrForContainer(cacheAddr)
	}
	return workerInternalEnv(iface, addr, cacheToken, workerID, workerToken)
}

func buildWorkerEnvForIsolation(spec WorkerSpec, fileVars map[string]string) []string {
	isolated := spec.Isolation != nil && spec.Isolation.usesDocker()
	internal := workerInternalEnvForIsolation(spec.Interface, spec.CacheAddr, spec.CacheToken, spec.WorkerID, spec.WorkerToken, isolated)
	if isolated {
		return buildWorkerEnv(nil, fileVars, spec.EnvVars, internal...)
	}
	return buildWorkerEnv(os.Environ(), fileVars, spec.EnvVars, internal...)
}

func buildIsolationFromCLI(backend, image, network, dockerHost, memory, cpus string, readOnly bool) (*IsolationConfig, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		return nil, nil
	}
	cfg := &IsolationConfig{Backend: backend}
	switch backend {
	case isolationBackendNone:
		return cfg, nil
	case isolationBackendDocker:
		cfg.Docker = &DockerIsolationConfig{
			Image:      strings.TrimSpace(image),
			Network:    strings.TrimSpace(network),
			DockerHost: strings.TrimSpace(dockerHost),
			Memory:     strings.TrimSpace(memory),
			CPUs:       strings.TrimSpace(cpus),
			ReadOnly:   readOnly,
		}
		if err := cfg.validate(); err != nil {
			return nil, err
		}
		return cfg, nil
	default:
		return nil, fmt.Errorf("invalid --isolation %q (want none or docker)", backend)
	}
}
