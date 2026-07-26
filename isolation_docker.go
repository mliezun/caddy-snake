package caddysnake

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	dockerWorkerLabel      = "caddy-snake.worker"
	dockerWorkerNamePrefix = "caddysnake-w-"
)

type dockerBackend struct {
	cfg *DockerIsolationConfig
}

func newDockerBackend(cfg *DockerIsolationConfig) (WorkerBackend, error) {
	if cfg == nil {
		return nil, fmt.Errorf("docker isolation config is required")
	}
	return dockerBackend{cfg: cfg}, nil
}

type dockerWorkerHandle struct {
	containerID string
	portFile    string
	portDir     string
	dialNet     string
	dialAddr    string
	exited      chan error
}

func (b dockerBackend) Start(ctx context.Context, spec WorkerSpec) (WorkerHandle, error) {
	logger := spec.Logger

	portDir, err := os.MkdirTemp("", "caddysnake-docker-*")
	if err != nil {
		return nil, err
	}
	if chErr := os.Chmod(portDir, 0o700); chErr != nil {
		os.RemoveAll(portDir)
		return nil, chErr
	}
	portFileHost := filepath.Join(portDir, "worker.port")
	containerPortPath := "/run/caddysnake/worker.port"

	workingDir := spec.WorkingDir
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		os.RemoveAll(portDir)
		return nil, err
	}

	scriptDir := spec.ScriptDir
	if scriptDir == "" {
		scriptDir = filepath.Dir(spec.ScriptPath)
	}
	absScriptDir, err := filepath.Abs(scriptDir)
	if err != nil {
		os.RemoveAll(portDir)
		return nil, err
	}
	scriptName := filepath.Base(spec.ScriptPath)
	containerScriptPath := filepath.Join("/opt/caddysnake", scriptName)

	// Prefer the image interpreter; host-resolved absolute paths are not visible in the container.
	pythonBin := "python3"
	if spec.PythonBin != "" && !filepath.IsAbs(spec.PythonBin) {
		pythonBin = spec.PythonBin
	}

	args := []string{
		"run", "-d", "--rm",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--label", dockerWorkerLabel + "=true",
		"--label", "caddy-snake.worker.id=" + spec.WorkerID,
		"--name", dockerWorkerContainerName(spec.WorkerID),
		"--add-host", "host.docker.internal:host-gateway",
	}
	network := strings.TrimSpace(b.cfg.Network)
	if network != "" {
		args = append(args, "--network", network)
	}
	if b.cfg.ReadOnly {
		args = append(args, "--read-only")
	}
	if mem := strings.TrimSpace(b.cfg.Memory); mem != "" {
		args = append(args, "--memory", mem)
	}
	if cpus := strings.TrimSpace(b.cfg.CPUs); cpus != "" {
		args = append(args, "--cpus", cpus)
	}

	args = append(args,
		"-v", absWorkingDir+":"+absWorkingDir+":rw",
		"-v", absScriptDir+":/opt/caddysnake:ro",
		"-v", portDir+":/run/caddysnake:rw",
	)
	if spec.Venv != "" {
		absVenv, vErr := filepath.Abs(spec.Venv)
		if vErr != nil {
			os.RemoveAll(portDir)
			return nil, vErr
		}
		args = append(args, "-v", absVenv+":"+absVenv+":ro")
		pythonBin = filepath.Join(absVenv, "bin", "python3")
	}
	for _, m := range b.cfg.Mounts {
		mode := strings.ToLower(strings.TrimSpace(m.Mode))
		if mode == "" {
			mode = "ro"
		}
		args = append(args, "-v", m.Host+":"+m.Container+":"+mode)
	}

	fileVars, err := loadEnvFiles(spec.WorkingDir, spec.EnvFiles)
	if err != nil {
		os.RemoveAll(portDir)
		return nil, err
	}
	for _, entry := range buildWorkerEnvForIsolation(spec, fileVars) {
		args = append(args, "-e", entry)
	}

	runArgs := []string{
		pythonBin,
		containerScriptPath,
		"--interface", spec.Interface,
		"--app", spec.App,
		"--socket", containerPortPath,
	}
	if absWorkingDir != "" {
		runArgs = append(runArgs, "--working-dir", absWorkingDir)
	}
	if spec.Venv != "" {
		absVenv, _ := filepath.Abs(spec.Venv)
		runArgs = append(runArgs, "--venv", absVenv)
	}
	if spec.Lifespan != "" {
		runArgs = append(runArgs, "--lifespan", spec.Lifespan)
	}
	if spec.Runtime != "" {
		runArgs = append(runArgs, "--runtime", spec.Runtime)
	}
	args = append(args, strings.TrimSpace(b.cfg.Image))
	args = append(args, runArgs...)

	containerID, err := b.runDocker(ctx, args...)
	if err != nil {
		os.RemoveAll(portDir)
		return nil, err
	}

	exited := make(chan error, 1)
	go func() {
		exited <- b.waitDocker(ctx, containerID)
	}()

	timeout := effectiveStartTimeout(spec.StartTimeout)
	port, err := waitForPortFile(portFileHost, timeout, exited, logger)
	if err != nil {
		logs := b.containerLogs(ctx, containerID)
		_ = b.removeContainer(ctx, containerID)
		os.RemoveAll(portDir)
		if logs != "" {
			return nil, fmt.Errorf("waiting for docker worker port file: %w\ncontainer logs:\n%s", err, logs)
		}
		return nil, fmt.Errorf("waiting for docker worker port file: %w", err)
	}

	containerIP, err := b.containerIP(ctx, containerID)
	if err != nil {
		_ = b.removeContainer(ctx, containerID)
		os.RemoveAll(portDir)
		return nil, err
	}

	return &dockerWorkerHandle{
		containerID: containerID,
		portFile:    portFileHost,
		portDir:     portDir,
		dialNet:     "tcp",
		dialAddr:    net.JoinHostPort(containerIP, strconv.Itoa(port)),
		exited:      exited,
	}, nil
}

func (h *dockerWorkerHandle) DialNetwork() string  { return h.dialNet }
func (h *dockerWorkerHandle) DialAddress() string  { return h.dialAddr }
func (h *dockerWorkerHandle) Exited() <-chan error { return h.exited }

func (b dockerBackend) Stop(handle WorkerHandle, grace time.Duration) error {
	h, ok := handle.(*dockerWorkerHandle)
	if !ok {
		return fmt.Errorf("docker backend: invalid handle type")
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace+10*time.Second)
	defer cancel()
	_ = b.stopContainer(ctx, h.containerID, grace)
	_ = b.removeContainer(ctx, h.containerID)
	if h.portDir != "" {
		_ = os.RemoveAll(h.portDir)
	}
	return nil
}

func dockerWorkerContainerName(workerID string) string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s%s-%s", dockerWorkerNamePrefix, workerID, hex.EncodeToString(buf))
}

func (b dockerBackend) dockerEnv() []string {
	if host := strings.TrimSpace(b.cfg.DockerHost); host != "" {
		return []string{"DOCKER_HOST=" + host}
	}
	return nil
}

func (b dockerBackend) runDocker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = append(os.Environ(), b.dockerEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		prefix := "docker run"
		if len(args) > 0 {
			prefix = "docker " + args[0]
		}
		return "", fmt.Errorf("%s: %w: %s", prefix, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (b dockerBackend) waitDocker(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, "docker", "wait", containerID)
	cmd.Env = append(os.Environ(), b.dockerEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker wait: %w: %s", err, strings.TrimSpace(string(out)))
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil || code != 0 {
		return fmt.Errorf("docker container exited with code %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (b dockerBackend) stopContainer(ctx context.Context, containerID string, grace time.Duration) error {
	secs := int(grace.Seconds())
	if secs < 1 {
		secs = 1
	}
	cmd := exec.CommandContext(ctx, "docker", "stop", "-t", strconv.Itoa(secs), containerID)
	cmd.Env = append(os.Environ(), b.dockerEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker stop: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b dockerBackend) removeContainer(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	cmd.Env = append(os.Environ(), b.dockerEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such container") {
			return nil
		}
		return fmt.Errorf("docker rm: %w: %s", err, msg)
	}
	return nil
}

func (b dockerBackend) containerIP(ctx context.Context, containerID string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerID)
	cmd.Env = append(os.Environ(), b.dockerEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker inspect ip: %w: %s", err, strings.TrimSpace(string(out)))
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("docker container %s has no IP address", containerID)
	}
	return ip, nil
}

func (b dockerBackend) containerLogs(ctx context.Context, containerID string) string {
	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", "200", containerID)
	cmd.Env = append(os.Environ(), b.dockerEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return strings.TrimSpace(string(out))
}

// ListDockerWorkerContainers returns container IDs for caddy-snake worker containers.
func ListDockerWorkerContainers(ctx context.Context, dockerHost string) ([]string, error) {
	args := []string{"ps", "-aq", "--filter", "label=" + dockerWorkerLabel + "=true"}
	cmd := exec.CommandContext(ctx, "docker", args...)
	if strings.TrimSpace(dockerHost) != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+dockerHost)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var ids []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}
