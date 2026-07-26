package caddysnake

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

type processBackend struct{}

type processWorkerHandle struct {
	cmd        *exec.Cmd
	cmdWaitCh  chan error
	dialNet    string
	dialAddr   string
	socketPath string
	sockDir    string
}

func (processBackend) Start(ctx context.Context, spec WorkerSpec) (WorkerHandle, error) {
	_ = ctx
	logger := spec.Logger

	var socket *os.File
	var sockDir string
	var err error
	if runtime.GOOS == "windows" {
		socket, err = os.CreateTemp("", "caddysnake-worker.port*")
	} else {
		sockDir, err = os.MkdirTemp("", "caddysnake-*")
		if err != nil {
			return nil, err
		}
		if chErr := os.Chmod(sockDir, 0o700); chErr != nil {
			os.RemoveAll(sockDir)
			return nil, chErr
		}
		socket, err = os.Create(filepath.Join(sockDir, "worker.sock"))
	}
	if err != nil {
		if sockDir != "" {
			os.RemoveAll(sockDir)
		}
		return nil, err
	}
	path := socket.Name()
	socket.Close()

	dialNet := "unix"
	dialAddr := path
	useTCP := runtime.GOOS == "windows"
	if useTCP {
		dialNet = "tcp"
		dialAddr = ""
	}

	workingDir := spec.WorkingDir
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}

	args := []string{
		spec.ScriptPath,
		"--interface", spec.Interface,
		"--app", spec.App,
		"--socket", path,
	}
	if workingDir != "" {
		args = append(args, "--working-dir", workingDir)
	}
	if spec.Venv != "" {
		args = append(args, "--venv", spec.Venv)
	}
	if spec.Lifespan != "" {
		args = append(args, "--lifespan", spec.Lifespan)
	}
	if spec.Runtime != "" {
		args = append(args, "--runtime", spec.Runtime)
	}

	fileVars, err := loadEnvFiles(spec.WorkingDir, spec.EnvFiles)
	if err != nil {
		if sockDir != "" {
			os.RemoveAll(sockDir)
		}
		return nil, err
	}

	cmd := exec.Command(spec.PythonBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = buildWorkerEnvForIsolation(spec, fileVars)
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		if sockDir != "" {
			os.RemoveAll(sockDir)
		}
		os.Remove(path)
		return nil, err
	}

	cmdWaitCh := make(chan error, 1)
	go func() {
		cmdWaitCh <- cmd.Wait()
	}()

	timeout := effectiveStartTimeout(spec.StartTimeout)
	if useTCP {
		port, err := waitForPortFile(path, timeout, cmdWaitCh, logger)
		if err != nil {
			_ = stopProcessWorker(cmd, cmdWaitCh, false)
			os.Remove(path)
			if sockDir != "" {
				os.RemoveAll(sockDir)
			}
			return nil, fmt.Errorf("waiting for Python worker port file: %w", err)
		}
		dialAddr = "127.0.0.1:" + strconv.Itoa(port)
	} else if err := waitForUnixSocket(path, timeout, cmdWaitCh, logger); err != nil {
		_ = stopProcessWorker(cmd, cmdWaitCh, false)
		if sockDir != "" {
			os.RemoveAll(sockDir)
		}
		return nil, fmt.Errorf("waiting for Python worker socket: %w", err)
	}

	return &processWorkerHandle{
		cmd:        cmd,
		cmdWaitCh:  cmdWaitCh,
		dialNet:    dialNet,
		dialAddr:   dialAddr,
		socketPath: path,
		sockDir:    sockDir,
	}, nil
}

func (h *processWorkerHandle) DialNetwork() string  { return h.dialNet }
func (h *processWorkerHandle) DialAddress() string  { return h.dialAddr }
func (h *processWorkerHandle) Exited() <-chan error { return h.cmdWaitCh }

func (processBackend) Stop(handle WorkerHandle, grace time.Duration) error {
	h, ok := handle.(*processWorkerHandle)
	if !ok {
		return fmt.Errorf("process backend: invalid handle type")
	}
	if h.cmd == nil || h.cmd.Process == nil {
		return cleanupProcessHandlePaths(h)
	}
	err := stopProcessWorker(h.cmd, h.cmdWaitCh, true)
	if pathErr := cleanupProcessHandlePaths(h); pathErr != nil {
		if err != nil {
			return fmt.Errorf("%w; %v", err, pathErr)
		}
		return pathErr
	}
	_ = grace
	return err
}

func stopProcessWorker(cmd *exec.Cmd, cmdWaitCh chan error, graceful bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" || !graceful {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	if cmdWaitCh != nil {
		select {
		case <-cmdWaitCh:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-cmdWaitCh
		}
	}
	return nil
}

func cleanupProcessHandlePaths(h *processWorkerHandle) error {
	if h == nil {
		return nil
	}
	if h.socketPath != "" {
		_ = os.Remove(h.socketPath)
	}
	if h.sockDir != "" {
		_ = os.RemoveAll(h.sockDir)
	}
	return nil
}
