package caddysnake

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// caddySnakeMountPrefixHeader carries the resolved mount prefix to the Python
// worker. Like the other Caddy-Snake-* hop headers it is always stripped from
// the inbound request and set only by this module.
const caddySnakeMountPrefixHeader = "Caddy-Snake-Mount-Prefix"

type mountPrefixCtxKey struct{}

// normalizeMountPrefix validates a (resolved) mount prefix and returns its
// canonical form: a leading slash, no trailing slash, and "" for "no prefix"
// (empty input or "/").
//
// Segments are limited to RFC 3986 unreserved characters so the prefix is the
// same whether or not a client percent-encodes it, and so it is always safe as
// a URL path, a WSGI SCRIPT_NAME and an HTTP header value.
func normalizeMountPrefix(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "/" {
		return "", nil
	}
	if !strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("mount_prefix %q must start with /", s)
	}
	s = strings.TrimSuffix(s, "/")
	for _, seg := range strings.Split(s[1:], "/") {
		if seg == "" {
			return "", fmt.Errorf("mount_prefix %q has an empty path segment", s)
		}
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("mount_prefix %q must not contain %q segments", s, seg)
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			case c == '-', c == '.', c == '_', c == '~':
			default:
				return "", fmt.Errorf("mount_prefix %q contains unsupported character %q (allowed: letters, digits, - . _ ~)", s, c)
			}
		}
	}
	return s, nil
}

// resolveMountPrefix expands placeholders in the configured prefix for this
// request and normalizes the result.
func resolveMountPrefix(pattern string, r *http.Request) (string, error) {
	if containsPlaceholder(pattern) {
		if repl, ok := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer); ok && repl != nil {
			pattern = repl.ReplaceAll(pattern, "")
		}
	}
	return normalizeMountPrefix(pattern)
}

// applyMountPrefix enforces the mount prefix for one request.
//
// It returns the request to forward (carrying the prefix for the worker) and
// handled=true when a response was already written (the trailing-slash
// redirect). Requests outside the prefix get a 404 error.
func applyMountPrefix(pattern string, w http.ResponseWriter, r *http.Request) (out *http.Request, handled bool, err error) {
	if pattern == "" {
		return r, false, nil
	}
	prefix, err := resolveMountPrefix(pattern, r)
	if err != nil {
		// A placeholder resolved to something that cannot be a mount point
		// (for example a path segment with spaces): nothing is mounted there.
		return r, false, caddyhttp.Error(http.StatusNotFound, err)
	}
	if prefix == "" {
		return r, false, nil
	}
	path := r.URL.Path
	if path == prefix {
		// Relative links in the app's HTML only resolve correctly under
		// "<prefix>/", so send the bare prefix there.
		target := prefix + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusPermanentRedirect)
		return r, true, nil
	}
	if !strings.HasPrefix(path, prefix+"/") {
		return r, false, caddyhttp.Error(http.StatusNotFound,
			fmt.Errorf("request path %q is outside mount_prefix %q", path, prefix))
	}
	return r.WithContext(context.WithValue(r.Context(), mountPrefixCtxKey{}, prefix)), false, nil
}

// mountPrefixFromContext returns the prefix stored by applyMountPrefix, if any.
func mountPrefixFromContext(ctx context.Context) string {
	prefix, _ := ctx.Value(mountPrefixCtxKey{}).(string)
	return prefix
}
