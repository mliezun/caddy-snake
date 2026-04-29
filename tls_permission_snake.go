package caddysnake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddytls"
)

func init() {
	caddy.RegisterModule(PermissionBySnakeDir{})
}

// PermissionBySnakeDir implements on-demand TLS permission by checking that the
// requested hostname is of the form {slug}.{domain_suffix} (exactly one label
// before the suffix), and that filepath.Join(root, slug) exists as a directory,
// optionally with a marker path inside it. Users can pair this with a dynamic
// python block using working_dir "{http.request.host.labels.2}/" when
// slug.appdomain.com uses labels.2 == slug for a three-part host.
//
// Implements [caddytls.OnDemandPermission] as tls.permission.snake_dir.
type PermissionBySnakeDir struct {
	// Absolute base path containing one subdirectory per slug (branch name, tenant, etc.).
	Root string `json:"root,omitempty"`
	// Registered domain suffix, e.g. appdomain.com (no leading dot). Hostname must be {slug}.{domain_suffix}.
	DomainSuffix string `json:"domain_suffix,omitempty"`
	// If non-empty, this path relative to the app directory must exist (file or subdirectory).
	RequireRegularFile string `json:"require_regular_file,omitempty"`

	rootAbs    string
	suffixNorm string
}

// CaddyModule returns the Caddy module information.
func (PermissionBySnakeDir) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "tls.permission.snake_dir",
		New: func() caddy.Module { return new(PermissionBySnakeDir) },
	}
}

var validSlugSegment = regexp.MustCompile(`^(?:[a-z0-9]|[a-z0-9][a-z0-9_-]*[a-z0-9])$`)

// Provision validates configuration and resolves Root to an absolute path.
func (p *PermissionBySnakeDir) Provision(ctx caddy.Context) error {
	_ = ctx
	if strings.TrimSpace(p.Root) == "" {
		return fmt.Errorf("tls.permission.snake_dir: root is required")
	}
	if strings.TrimSpace(p.DomainSuffix) == "" {
		return fmt.Errorf("tls.permission.snake_dir: domain_suffix is required")
	}
	abs, err := filepath.Abs(p.Root)
	if err != nil {
		return fmt.Errorf("tls.permission.snake_dir: resolving root: %w", err)
	}
	rootEval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("tls.permission.snake_dir: resolving root symlink: %w", err)
	}
	st, err := os.Stat(rootEval)
	if err != nil {
		return fmt.Errorf("tls.permission.snake_dir: stat root %q: %w", rootEval, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("tls.permission.snake_dir: root %q is not a directory", rootEval)
	}
	p.rootAbs = filepath.Clean(rootEval)

	s := strings.ToLower(strings.TrimSpace(p.DomainSuffix))
	s = strings.Trim(s, ".")
	p.suffixNorm = s

	if p.RequireRegularFile != "" {
		cp := filepath.ToSlash(filepath.Clean(p.RequireRegularFile))
		if cp == "" || cp == "." || cp == ".." || strings.HasPrefix(cp, "../") || strings.Contains(cp, "/../") || filepath.IsAbs(p.RequireRegularFile) {
			return fmt.Errorf("tls.permission.snake_dir: require_regular_file must be a non-empty relative path without ..")
		}
	}
	return nil
}

func pathWithinRoot(rootCleanAbs, resolved string) bool {
	rel, err := filepath.Rel(rootCleanAbs, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// CertificateAllowed implements [caddytls.OnDemandPermission].
func (p *PermissionBySnakeDir) CertificateAllowed(_ context.Context, name string) error {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if host == "" {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}

	suffixNeedle := "." + p.suffixNorm
	if !strings.HasSuffix(host, suffixNeedle) {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}
	slug := strings.TrimSuffix(host, suffixNeedle)
	if slug == "" || strings.Contains(slug, ".") {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}
	if !validSlugSegment.MatchString(slug) {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}

	dirPath := filepath.Join(p.rootAbs, slug)
	dirPath = filepath.Clean(dirPath)
	if !pathWithinRoot(p.rootAbs, dirPath) {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}

	dirResolved, err := filepath.EvalSymlinks(dirPath)
	if err != nil {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}
	if !pathWithinRoot(p.rootAbs, filepath.Clean(dirResolved)) {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}

	st, err := os.Stat(dirResolved)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
	}

	if p.RequireRegularFile != "" {
		marker := filepath.Join(dirResolved, filepath.FromSlash(filepath.ToSlash(filepath.Clean(p.RequireRegularFile))))
		markerResolved, err := filepath.EvalSymlinks(marker)
		if err != nil {
			return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
		}
		if !pathWithinRoot(dirResolved, filepath.Clean(markerResolved)) {
			return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
		}
		stM, err := os.Stat(markerResolved)
		if err != nil {
			return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
		}
		if !stM.Mode().IsRegular() && !stM.IsDir() {
			return fmt.Errorf("%w", caddytls.ErrPermissionDenied)
		}
	}

	return nil
}

// UnmarshalCaddyfile implements [caddyfile.Unmarshaler].
//
//	snake_dir {
//	    root /home/server
//	    domain_suffix appdomain.com
//	    require_regular_file main.py
//	}
func (p *PermissionBySnakeDir) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		switch d.Val() {
		case "root":
			if !d.Args(&p.Root) {
				return d.ArgErr()
			}
		case "domain_suffix":
			if !d.Args(&p.DomainSuffix) {
				return d.ArgErr()
			}
		case "require_regular_file":
			if !d.Args(&p.RequireRegularFile) {
				return d.ArgErr()
			}
		default:
			return d.Errf("unknown subdirective %q", d.Val())
		}
	}
	return nil
}

// Interface guards
var (
	_ caddytls.OnDemandPermission = (*PermissionBySnakeDir)(nil)
	_ caddy.Provisioner           = (*PermissionBySnakeDir)(nil)
	_ caddyfile.Unmarshaler       = (*PermissionBySnakeDir)(nil)
)
