package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed python-standalone.tar.gz
var pythonStandalonePkg []byte

//go:embed caddy
var caddyBinary []byte

// extractTarGz extracts an embedded tar.gz into a target directory
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
			break // end of archive
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
			// Ensure parent directories exist
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
		}
	}

	return nil
}

func run() int {
	tmpDirPkg, err := os.MkdirTemp("", "python-*")
	if err != nil {
		fmt.Println("Error creating temporary directory for Python standalone package:", err)
		return 1
	}
	defer os.RemoveAll(tmpDirPkg)

	if err := extractTarGz(pythonStandalonePkg, tmpDirPkg); err != nil {
		fmt.Println("Error extracting Python standalone package:", err)
		return 1
	}
	tmpDir, err := os.MkdirTemp("", "caddy-*")
	if err != nil {
		fmt.Println("Error creating temporary directory for Caddy binary:", err)
		return 1
	}
	defer os.RemoveAll(tmpDir)

	caddyPath := filepath.Join(tmpDir, "caddy")

	if err := os.WriteFile(caddyPath, caddyBinary, 0755); err != nil {
		fmt.Println("Error writing Caddy binary:", err)
		return 1
	}

	if err := os.Chmod(caddyPath, 0755); err != nil {
		fmt.Println("Error changing permissions for Caddy binary:", err)
		return 1
	}

	args := os.Args[1:]

	env := []string{}
	ld_library_path := ""
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "PYTHONHOME=") {
			env = append(env, e)
		} else if strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			ld_library_path = strings.Split(e, "=")[1]
		}
	}
	env = append(env, fmt.Sprintf("PYTHONHOME=%s", filepath.Join(tmpDirPkg, "python")))
	env = append(env, fmt.Sprintf("LD_LIBRARY_PATH=%s:%s", filepath.Join(tmpDirPkg, "python", "lib"), ld_library_path))

	cmd := exec.Command(caddyPath, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Println("Error running Caddy binary:", err)
		return cmd.ProcessState.ExitCode()
	}

	return 0
}

func main() {
	os.Exit(run())
}
