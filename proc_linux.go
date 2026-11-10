//go:build linux

package caddysnake

import (
	"os/exec"
	"syscall"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
}

// tieToParent is only needed on Windows.
func tieToParent(cmd *exec.Cmd) {}
