package caddysnake

import "os/exec"

func setSysProcAttr(cmd *exec.Cmd) {
	// Pdeathsig is not available on Windows.
}
