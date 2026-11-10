package caddysnake

import "errors"

// processMemoryBytes is not implemented on Windows; memory_limit is ignored there.
func processMemoryBytes(pid int) (int64, error) {
	return 0, errors.New("process memory measurement is not supported on windows")
}
