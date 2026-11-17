//go:build linux

package caddysnake

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processMemoryBytes returns the memory a process is responsible for.
//
// It prefers PSS (proportional set size) from smaps_rollup, which splits pages
// shared between workers fairly, and falls back to RSS from statm.
func processMemoryBytes(pid int) (int64, error) {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid)); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "Pss:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return kb * 1024, nil
				}
			}
		}
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected /proc/%d/statm format", pid)
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * int64(os.Getpagesize()), nil
}
