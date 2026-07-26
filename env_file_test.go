package caddysnake

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# comment
export APP_TEST_FROM_FILE=file_value
APP_TEST_QUOTED="hello world"
APP_TEST_SINGLE='single'

APP_TEST_OVERRIDE=from_file
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	vars, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if vars["APP_TEST_FROM_FILE"] != "file_value" {
		t.Errorf("APP_TEST_FROM_FILE = %q", vars["APP_TEST_FROM_FILE"])
	}
	if vars["APP_TEST_QUOTED"] != "hello world" {
		t.Errorf("APP_TEST_QUOTED = %q", vars["APP_TEST_QUOTED"])
	}
	if vars["APP_TEST_SINGLE"] != "single" {
		t.Errorf("APP_TEST_SINGLE = %q", vars["APP_TEST_SINGLE"])
	}
	if vars["APP_TEST_OVERRIDE"] != "from_file" {
		t.Errorf("APP_TEST_OVERRIDE = %q", vars["APP_TEST_OVERRIDE"])
	}
}

func TestParseEnvFile_InvalidLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("NOT_VALID\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := parseEnvFile(path)
	if err == nil {
		t.Fatal("expected error for invalid line")
	}
}

func TestBuildWorkerEnv_Precedence(t *testing.T) {
	base := []string{"SHARED=from_process", "ONLY_BASE=1"}
	fileVars := map[string]string{
		"SHARED":             "from_file",
		"APP_TEST_FROM_FILE": "file_value",
	}
	inlineVars := map[string]string{
		"SHARED":            "from_inline",
		"APP_TEST_INLINE":   "inline_value",
		"APP_TEST_OVERRIDE": "from_inline",
	}
	extra := []string{"PYTHONUNBUFFERED=1", "CADDYSNAKE_CACHE_ADDR=unix://test"}

	env := buildWorkerEnv(base, fileVars, inlineVars, extra...)
	m := parseEnvSlice(env)

	if m["SHARED"] != "from_inline" {
		t.Errorf("SHARED = %q, want from_inline", m["SHARED"])
	}
	if m["APP_TEST_FROM_FILE"] != "file_value" {
		t.Errorf("APP_TEST_FROM_FILE = %q", m["APP_TEST_FROM_FILE"])
	}
	if m["APP_TEST_INLINE"] != "inline_value" {
		t.Errorf("APP_TEST_INLINE = %q", m["APP_TEST_INLINE"])
	}
	if m["ONLY_BASE"] != "1" {
		t.Errorf("ONLY_BASE = %q", m["ONLY_BASE"])
	}
	if m["PYTHONUNBUFFERED"] != "1" {
		t.Errorf("PYTHONUNBUFFERED = %q", m["PYTHONUNBUFFERED"])
	}
	if m["CADDYSNAKE_CACHE_ADDR"] != "unix://test" {
		t.Errorf("CADDYSNAKE_CACHE_ADDR = %q", m["CADDYSNAKE_CACHE_ADDR"])
	}
}

func TestWorkerInternalEnv_IncludesWorkerID(t *testing.T) {
	env := workerInternalEnv("wsgi", "unix://cache.sock", "", "2", "tok")
	m := parseEnvSlice(env)
	if m[EnvCaddysnakeWorkerID] != "2" {
		t.Errorf("CADDYSNAKE_WORKER_ID = %q, want 2", m[EnvCaddysnakeWorkerID])
	}
	if m[EnvCaddysnakeWorkerInterface] != "wsgi" {
		t.Errorf("interface = %q", m[EnvCaddysnakeWorkerInterface])
	}
}

func TestWorkerInternalEnv_OmitsWorkerIDWhenEmpty(t *testing.T) {
	env := workerInternalEnv("asgi", "", "", "", "")
	m := parseEnvSlice(env)
	if _, ok := m[EnvCaddysnakeWorkerID]; ok {
		t.Error("expected no worker id without cache")
	}
}

func TestResolveEnvFilePath(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("configs", "app.env")
	abs, err := resolveEnvFilePath(dir, rel)
	if err != nil {
		t.Fatalf("resolveEnvFilePath: %v", err)
	}
	want := filepath.Join(dir, rel)
	wantAbs, _ := filepath.Abs(want)
	if abs != wantAbs {
		t.Errorf("got %q, want %q", abs, wantAbs)
	}

	_, err = resolveEnvFilePath(dir, "../escape.env")
	if err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestLoadEnvFiles_Multiple(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.env")
	second := filepath.Join(dir, "second.env")
	if err := os.WriteFile(first, []byte("A=1\nB=from_first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("B=from_second\nC=3\n"), 0600); err != nil {
		t.Fatal(err)
	}

	vars, err := loadEnvFiles(dir, []string{first, second})
	if err != nil {
		t.Fatalf("loadEnvFiles: %v", err)
	}
	if vars["A"] != "1" || vars["B"] != "from_second" || vars["C"] != "3" {
		t.Errorf("vars = %#v", vars)
	}
}

func TestValidateEnvVarName(t *testing.T) {
	if err := validateEnvVarName("APP_TEST_OK"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := validateEnvVarName("1BAD"); err == nil {
		t.Error("expected error for invalid name")
	}
	if err := validateEnvVarName("CADDYSNAKE_CACHE_ADDR"); err == nil {
		t.Error("expected error for reserved name")
	}
	if err := validateEnvVarName("PYTHONUNBUFFERED"); err == nil {
		t.Error("expected error for reserved name")
	}
	if err := validateEnvVarName("LD_PRELOAD"); err == nil {
		t.Error("expected error for LD_PRELOAD")
	}
	if err := validateEnvVarName("LD_LIBRARY_PATH"); err == nil {
		t.Error("expected error for LD_LIBRARY_PATH")
	}
	if err := validateEnvVarName("DYLD_INSERT_LIBRARIES"); err == nil {
		t.Error("expected error for DYLD_INSERT_LIBRARIES")
	}
}

func TestParseEnvFile_RejectsDangerousKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("LD_PRELOAD=/tmp/evil.so\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := parseEnvFile(path)
	if err == nil || !strings.Contains(err.Error(), "LD_PRELOAD") {
		t.Fatalf("expected LD_PRELOAD rejection, got %v", err)
	}
}

func TestParseEnvFile_RejectsReservedKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("CADDYSNAKE_CACHE_ADDR=evil\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := parseEnvFile(path)
	if err == nil || !strings.Contains(err.Error(), "CADDYSNAKE_CACHE_ADDR") {
		t.Fatalf("expected reserved name rejection, got %v", err)
	}
}

func TestResolveEnvFilePath_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	working := filepath.Join(root, "tenant")
	outside := filepath.Join(root, "secret.env")
	if err := os.Mkdir(working, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("SECRET=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(working, ".env")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err := resolveEnvFilePath(working, ".env")
	if err == nil || !strings.Contains(err.Error(), "outside working_dir") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestResolveEnvFilePath_RelativeInsideWorkingDir(t *testing.T) {
	working := t.TempDir()
	path := filepath.Join(working, ".env")
	if err := os.WriteFile(path, []byte("OK=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	abs, err := resolveEnvFilePath(working, ".env")
	if err != nil {
		t.Fatalf("resolveEnvFilePath: %v", err)
	}
	want, _ := filepath.Abs(path)
	if abs != want {
		t.Errorf("got %q, want %q", abs, want)
	}
}

func TestUnmarshalCaddyfile_EnvFile(t *testing.T) {
	input := `python {
		module_wsgi main:app
		env_file /tmp/a.env
		env_file ./b.env
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	if err := cs.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cs.EnvFiles) != 2 || cs.EnvFiles[0] != "/tmp/a.env" || cs.EnvFiles[1] != "./b.env" {
		t.Errorf("EnvFiles = %#v", cs.EnvFiles)
	}
}

func TestUnmarshalCaddyfile_EnvVar(t *testing.T) {
	input := `python {
		module_asgi main:app
		env_var APP_TEST_INLINE inline_value
		env_var APP_TEST_OVERRIDE override_value
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	if err := cs.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.EnvVars["APP_TEST_INLINE"] != "inline_value" {
		t.Errorf("APP_TEST_INLINE = %q", cs.EnvVars["APP_TEST_INLINE"])
	}
	if cs.EnvVars["APP_TEST_OVERRIDE"] != "override_value" {
		t.Errorf("APP_TEST_OVERRIDE = %q", cs.EnvVars["APP_TEST_OVERRIDE"])
	}
}

func TestUnmarshalCaddyfile_EnvVarReserved(t *testing.T) {
	input := `python {
		module_wsgi main:app
		env_var CADDYSNAKE_CACHE_ADDR evil
	}`
	d := caddyfile.NewTestDispenser(input)
	var cs CaddySnake
	err := cs.UnmarshalCaddyfile(d)
	if err == nil || !strings.Contains(err.Error(), "invalid env_var name") {
		t.Fatalf("expected reserved name error, got %v", err)
	}
}

func TestValidate_EnvVarReserved(t *testing.T) {
	cs := CaddySnake{
		ModuleWsgi: "main:app",
		EnvVars:    map[string]string{"PYTHONUNBUFFERED": "0"},
	}
	err := cs.Validate()
	if err == nil || !strings.Contains(err.Error(), "PYTHONUNBUFFERED") {
		t.Fatalf("expected validation error, got %v", err)
	}
}
