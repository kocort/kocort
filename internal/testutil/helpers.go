package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TempDir creates a temporary directory that is automatically cleaned up
// when the test finishes. It is a thin wrapper around t.TempDir() but
// provides a consistent pattern for tests that need workspace directories.
func TempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// TempFileWithContent creates a temporary file with the given content and
// returns its path. The file is automatically cleaned up when the test
// finishes.
func TempFileWithContent(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("testutil.TempFileWithContent: mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("testutil.TempFileWithContent: write %s: %v", path, err)
	}
	return path
}

// MustNotError fails the test immediately if err is non-nil.
func MustNotError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// MustError fails the test immediately if err is nil.
func MustError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error but got nil")
	}
}
