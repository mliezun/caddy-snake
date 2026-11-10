package apps

import "golang.org/x/sys/windows"

// isAdmin reports whether this process has administrator rights.
func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
