package caddysnake

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindSitePackagesInVenv(t *testing.T) {
	// Set up a temporary directory for the virtual environment simulation
	tempDir := t.TempDir()
	venvLibPath := filepath.Join(tempDir, "lib", "python3.12", "site-packages")

	// Create the directory structure
	err := os.MkdirAll(venvLibPath, 0755)
	if err != nil {
		t.Fatalf("failed to create test directory structure: %v", err)
	}

	// Test the function
	result, err := findSitePackagesInVenv(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the result
	expectedPath := venvLibPath
	if result != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, result)
	}

	// Clean up is handled automatically by t.TempDir()
}

func TestFindSitePackagesInVenv_NoPythonDirectory(t *testing.T) {
	// Set up a temporary directory for the virtual environment simulation
	tempDir := t.TempDir()

	// Test the function
	_, err := findSitePackagesInVenv(tempDir)
	if err == nil {
		t.Fatalf("expected an error, but got none")
	}

	// Verify the error message
	expectedError := "unable to find a python3.* directory in the venv"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestFindSitePackagesInVenv_NoSitePackages(t *testing.T) {
	// Set up a temporary directory for the virtual environment simulation
	tempDir := t.TempDir()
	libPath := filepath.Join(tempDir, "lib", "python3.12")

	// Create the lib/python3.12 directory, but omit site-packages
	err := os.MkdirAll(libPath, 0755)
	if err != nil {
		t.Fatalf("failed to create test directory structure: %v", err)
	}

	// Test the function
	_, err = findSitePackagesInVenv(tempDir)
	if err == nil {
		t.Fatalf("expected an error, but got none")
	}

	// Verify the error message
	expectedError := "site-packages directory does not exist"
	if !strings.HasPrefix(err.Error(), expectedError) {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}
