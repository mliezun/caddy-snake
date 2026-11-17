package caddysnake

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// pinPath follows every link in a path and returns where it really points, so
// a worker that is started with it keeps running the same files even when a
// link such as ".../current" is switched to a newer release a moment later.
//
// Without this a worker that is still importing its app when the link moves
// loads half of the old release and half of the new one: Python looks up each
// module by path at the time it is first imported.
//
// A path that cannot be resolved (it does not exist yet, or cannot be read)
// is returned unchanged.
func pinPath(path string) string {
	if path == "" {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if runtime.GOOS == "windows" {
		abs = followJunctions(abs)
	}
	return abs
}

// followJunctions resolves Windows junctions, which EvalSymlinks leaves alone
// (Go treats them as mount points, not symbolic links). Each leading part of
// the path that is a link with an absolute target is replaced by that target.
func followJunctions(path string) string {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path[len(volume):], string(filepath.Separator))
	current := volume + string(filepath.Separator)
	// A bound on replacements, so a loop of links cannot spin for ever.
	for hops := 0; rest != "" && hops < 64; {
		part := rest
		if i := strings.IndexRune(rest, filepath.Separator); i >= 0 {
			part, rest = rest[:i], rest[i+1:]
		} else {
			rest = ""
		}
		next := filepath.Join(current, part)
		if target, err := os.Readlink(next); err == nil && filepath.IsAbs(target) {
			next = filepath.Clean(target)
			hops++
		}
		current = next
	}
	return current
}
