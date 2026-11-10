//go:build !windows

package apps

import "os"

// isAdmin reports whether this process has administrator rights.
func isAdmin() bool {
	return os.Geteuid() == 0
}
