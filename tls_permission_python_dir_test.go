package caddysnake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddytls"
)

func provisionPerm(t testing.TB, p *PermissionByPythonDir) {
	t.Helper()
	ctx := caddy.Context{}
	if err := p.Provision(ctx); err != nil {
		t.Fatalf("Provision: %v", err)
	}
}

func TestPermissionByPythonDir_CaddyModule(t *testing.T) {
	info := PermissionByPythonDir{}.CaddyModule()
	if info.ID != "tls.permission.python_dir" {
		t.Errorf("unexpected module ID: %s", info.ID)
	}
	mod := info.New()
	if _, ok := mod.(*PermissionByPythonDir); !ok {
		t.Errorf("New() type: %T", mod)
	}
}

func TestPermissionByPythonDir_CertificateAllowed(t *testing.T) {
	td := t.TempDir()
	root := filepath.Join(td, "apps")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}

	featureOK := filepath.Join(root, "featureb")
	if err := os.Mkdir(featureOK, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureOK, "main.py"), []byte("# ok"), 0644); err != nil {
		t.Fatal(err)
	}

	p := &PermissionByPythonDir{
		Root:               root,
		DomainSuffix:       "project.example.net",
		RequireRegularFile: "",
	}
	provisionPerm(t, p)

	ctx := context.Background()

	t.Run("allow_when_dir_exists", func(t *testing.T) {
		if err := p.CertificateAllowed(ctx, "featureb.project.example.net"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("wrong_suffix_denied", func(t *testing.T) {
		err := p.CertificateAllowed(ctx, "featureb.other.example")
		if !errors.Is(err, caddytls.ErrPermissionDenied) {
			t.Fatalf("expected ErrPermissionDenied, got %v", err)
		}
	})

	t.Run("nested_subdomain_denied", func(t *testing.T) {
		err := p.CertificateAllowed(ctx, "a.featureb.project.example.net")
		if err == nil {
			t.Fatal("expected denial")
		}
	})

	t.Run("missing_dir_denied", func(t *testing.T) {
		err := p.CertificateAllowed(ctx, "nothere.project.example.net")
		if err == nil {
			t.Fatal("expected denial")
		}
	})
}

func TestPermissionByPythonDir_RequireRegularFile(t *testing.T) {
	td := t.TempDir()
	root := filepath.Join(td, "apps")
	branchDir := filepath.Join(root, "app1")
	if err := os.MkdirAll(branchDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(branchDir, "app.py"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	p := &PermissionByPythonDir{
		Root:               root,
		DomainSuffix:       "dev.example.test",
		RequireRegularFile: "app.py",
	}
	provisionPerm(t, p)

	if err := p.CertificateAllowed(context.Background(), "app1.dev.example.test"); err != nil {
		t.Fatal(err)
	}

	p2 := &PermissionByPythonDir{
		Root:               root,
		DomainSuffix:       "dev.example.test",
		RequireRegularFile: "absent.py",
	}
	provisionPerm(t, p2)
	if err := p2.CertificateAllowed(context.Background(), "app1.dev.example.test"); err == nil {
		t.Fatal("expected denial when marker missing")
	}
}

func TestPermissionByPythonDir_SymlinkDirOutsideDenied(t *testing.T) {
	td := t.TempDir()
	root := filepath.Join(td, "apps")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(td, "outside")
	if err := os.Mkdir(outDir, 0755); err != nil {
		t.Fatal(err)
	}

	_ = os.Remove(filepath.Join(root, "leak"))
	if err := os.Symlink(outDir, filepath.Join(root, "leak")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	p := &PermissionByPythonDir{
		Root:         root,
		DomainSuffix: "srv.test",
	}
	provisionPerm(t, p)

	if err := p.CertificateAllowed(context.Background(), "leak.srv.test"); err == nil {
		t.Fatal("expected denial when slug dir escapes root via symlink")
	}
}

func TestPermissionByPythonDir_ProvisionInvalid(t *testing.T) {
	ctx := caddy.Context{}

	p := &PermissionByPythonDir{DomainSuffix: "x.com"}
	if err := p.Provision(ctx); err == nil {
		t.Fatal("expected error for empty root")
	}

	p = &PermissionByPythonDir{Root: t.TempDir()}
	if err := p.Provision(ctx); err == nil {
		t.Fatal("expected error for empty domain_suffix")
	}
}

func TestPermissionByPythonDir_UnmarshalCaddyfile(t *testing.T) {
	base := filepath.Join(t.TempDir(), "deploy-root")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}

	input := `root ` + base + `
	domain_suffix apps.example.dev
	require_regular_file main.py
`

	d := caddyfile.NewTestDispenser(input)
	var p PermissionByPythonDir
	if err := p.UnmarshalCaddyfile(d); err != nil {
		t.Fatal(err)
	}
	if p.Root != base || p.DomainSuffix != "apps.example.dev" || p.RequireRegularFile != "main.py" {
		t.Fatalf("parsed unexpected values: %+v", p)
	}
}
