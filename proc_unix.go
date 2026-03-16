//go:build !windows && !linux

package caddysnake

import "os/exec"

func setSysProcAttr(cmd *exec.Cmd) {
	// Pdeathsig is Linux-only; not available on darwin/BSD.
}
