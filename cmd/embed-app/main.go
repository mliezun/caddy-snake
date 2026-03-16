package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed python-standalone.tar.gz
var pythonStandalonePkg []byte

//go:embed caddy
var caddyBinary []byte

//go:embed app.zip
var appZip []byte

// Set at build time via -ldflags
// Set via -ldflags "-X main.appEntry=main:app -X main.serverType=wsgi"
var (
	appEntry   = "main:app"
	serverType = "wsgi"
)

func extractTarGz(data []byte, targetDir string) error {
	zsr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer zsr.Close()

	tr := tar.NewReader(zsr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		path := filepath.Join(targetDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, os.FileMode(header.Mode)); err != nil {
				return err
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}

			outFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()

		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, path); err != nil {
				return err
			}
		}
	}

	return nil
}

func extractZip(data []byte, targetDir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}

	for _, f := range zr.File {
		path := filepath.Join(targetDir, f.Name)

		// Guard against zip slip
		if !strings.HasPrefix(path, filepath.Clean(targetDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid path in zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, f.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}

		if _, err := io.Copy(outFile, rc); err != nil {
			outFile.Close()
			rc.Close()
			return err
		}
		outFile.Close()
		rc.Close()
	}

	return nil
}

func run() int {
	tmpDirPkg, err := os.MkdirTemp("", "python-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating temporary directory for Python standalone package:", err)
		return 1
	}
	defer os.RemoveAll(tmpDirPkg)

	if err := extractTarGz(pythonStandalonePkg, tmpDirPkg); err != nil {
		fmt.Fprintln(os.Stderr, "Error extracting Python standalone package:", err)
		return 1
	}

	tmpDirCaddy, err := os.MkdirTemp("", "caddy-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating temporary directory for Caddy binary:", err)
		return 1
	}
	defer os.RemoveAll(tmpDirCaddy)

	caddyPath := filepath.Join(tmpDirCaddy, "caddy")
	if err := os.WriteFile(caddyPath, caddyBinary, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "Error writing Caddy binary:", err)
		return 1
	}
	if err := os.Chmod(caddyPath, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "Error changing permissions for Caddy binary:", err)
		return 1
	}

	tmpDirApp, err := os.MkdirTemp("", "app-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating temporary directory for app:", err)
		return 1
	}
	defer os.RemoveAll(tmpDirApp)

	if err := extractZip(appZip, tmpDirApp); err != nil {
		fmt.Fprintln(os.Stderr, "Error extracting embedded app:", err)
		return 1
	}

	env := []string{}
	ldLibraryPath := ""
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			ldLibraryPath = strings.SplitN(e, "=", 2)[1]
		}
		if strings.HasPrefix(e, "PYTHONHOME=") || strings.HasPrefix(e, "LD_LIBRARY_PATH=") || strings.HasPrefix(e, "DYLD_LIBRARY_PATH=") {
			continue
		}
		env = append(env, e)
	}
	pythonHome := filepath.Join(tmpDirPkg, "python")
	pythonBin := filepath.Join(pythonHome, "bin", "python3")
	env = append(env, fmt.Sprintf("PYTHONHOME=%s", pythonHome))
	env = append(env, fmt.Sprintf("LD_LIBRARY_PATH=%s:%s", filepath.Join(pythonHome, "lib"), ldLibraryPath))
	if runtime.GOOS == "darwin" {
		env = append(env, fmt.Sprintf("DYLD_LIBRARY_PATH=%s:%s", filepath.Join(pythonHome, "lib"), os.Getenv("DYLD_LIBRARY_PATH")))
	}

	args := []string{"python-server", "--app", appEntry, "--server-type", serverType, "--python-path", pythonBin}
	args = append(args, os.Args[1:]...)

	cmd := exec.Command(caddyPath, args...)
	cmd.Env = env
	cmd.Dir = tmpDirApp
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "Error running Caddy:", err)
		return 1
	}

	return 0
}

func main() {
	os.Exit(run())
}
