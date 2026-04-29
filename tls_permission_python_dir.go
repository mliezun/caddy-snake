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
	caddy.RegisterModule(PermissionByPythonDir{})
}

// PermissionByPythonDir implements on-demand TLS permission by checking that the
// requested hostname is of the form {slug}.{domain_suffix} (exactly one label
// before the suffix), and that filepath.Join(root, slug) exists as a directory.
// Users can pair this with a dynamic python block using working_dir "{http.request.host.labels.2}/" when
// slug.appdomain.com uses labels.2 == slug for a three-part host.
//
// Implements [caddytls.OnDemandPermission] as tls.permission.python_dir.
type PermissionByPythonDir struct {
	// Absolute base path containing one subdirectory per slug (branch name, tenant, etc.).
	Root string `json:"root,omitempty"`
	// Registered domain suffix, e.g. appdomain.com (no leading dot). Hostname must be {slug}.{domain_suffix}.
	DomainSuffix string `json:"domain_suffix,omitempty"`

	rootAbs    string
	suffixNorm string
}

// CaddyModule returns the Caddy module information.
func (PermissionByPythonDir) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "tls.permission.python_dir",
		New: func() caddy.Module { return new(PermissionByPythonDir) },
	}
}

var validSlugSegment = regexp.MustCompile(`^(?:[a-z0-9]|[a-z0-9][a-z0-9_-]*[a-z0-9])$`)

// Provision validates configuration and resolves Root to an absolute path.
func (p *PermissionByPythonDir) Provision(ctx caddy.Context) error {
	_ = ctx
	if strings.TrimSpace(p.Root) == "" {
		return fmt.Errorf("tls.permission.python_dir: root is required")
	}
	if strings.TrimSpace(p.DomainSuffix) == "" {
		return fmt.Errorf("tls.permission.python_dir: domain_suffix is required")
	}
	abs, err := filepath.Abs(p.Root)
	if err != nil {
		return fmt.Errorf("tls.permission.python_dir: resolving root: %w", err)
	}
	rootEval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("tls.permission.python_dir: resolving root symlink: %w", err)
	}
	st, err := os.Stat(rootEval)
	if err != nil {
		return fmt.Errorf("tls.permission.python_dir: stat root %q: %w", rootEval, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("tls.permission.python_dir: root %q is not a directory", rootEval)
	}
	p.rootAbs = filepath.Clean(rootEval)

	s := strings.ToLower(strings.TrimSpace(p.DomainSuffix))
	s = strings.Trim(s, ".")
	p.suffixNorm = s

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
func (p *PermissionByPythonDir) CertificateAllowed(_ context.Context, name string) error {
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

	return nil
}

// UnmarshalCaddyfile implements [caddyfile.Unmarshaler].
//
//	python_dir {
//	    root /home/server
//	    domain_suffix appdomain.com
//	}
func (p *PermissionByPythonDir) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
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
		default:
			return d.Errf("unknown subdirective %q", d.Val())
		}
	}
	return nil
}

// Interface guards
var (
	_ caddytls.OnDemandPermission = (*PermissionByPythonDir)(nil)
	_ caddy.Provisioner           = (*PermissionByPythonDir)(nil)
	_ caddyfile.Unmarshaler       = (*PermissionByPythonDir)(nil)
)
