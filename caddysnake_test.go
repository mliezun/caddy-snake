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

func TestNewMapKeyVal(t *testing.T) {
	m := NewMapKeyVal(3)
	if m == nil {
		t.Fatal("Expected non-nil MapKeyVal")
	}
	if m.Len() != 3 {
		t.Fatalf("Expected length 3, got %d", m.Len())
	}
	defer m.Cleanup()
}

func TestNewMapKeyValFromSource(t *testing.T) {
	m := NewMapKeyValFromSource(NewMapKeyVal(3).m)
	if m == nil {
		t.Fatal("Expected non-nil MapKeyVal")
	}
	if m.Len() != 3 {
		t.Fatalf("Expected length 3, got %d", m.Len())
	}
	defer m.Cleanup()
}

func TestSetAndGet(t *testing.T) {
	m := NewMapKeyVal(2)
	defer m.Cleanup()

	m.Set("Content-Type", "application/json", 0)
	m.Set("Accept", "text/plain", 1)

	k0, v0 := m.Get(0)
	if k0 != "Content-Type" || v0 != "application/json" {
		t.Errorf("Unexpected result at pos 0: got (%s, %s)", k0, v0)
	}

	k1, v1 := m.Get(1)
	if k1 != "Accept" || v1 != "text/plain" {
		t.Errorf("Unexpected result at pos 1: got (%s, %s)", k1, v1)
	}
}

func TestSetGetBounds(t *testing.T) {
	m := NewMapKeyVal(1)
	defer m.Cleanup()

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic for out-of-bounds Set, but did not panic")
		}
	}()
	m.Set("Overflow", "Oops", 2)
}

func TestGetBounds(t *testing.T) {
	m := NewMapKeyVal(1)
	defer m.Cleanup()

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic for out-of-bounds Get, but did not panic")
		}
	}()
	m.Get(5)
}

func TestLenNull(t *testing.T) {
	m := MapKeyVal{}

	if m.Len() != 0 {
		t.Errorf("Expected length 0, got %d", m.Len())
	}
}
