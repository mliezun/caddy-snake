//go:build !windows && !linux

package caddysnake

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// processMemoryBytes returns the resident set size of a process using ps,
// which is available on macOS and the BSDs.
func processMemoryBytes(pid int) (int64, error) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected ps output %q", strings.TrimSpace(string(out)))
	}
	return kb * 1024, nil
}
