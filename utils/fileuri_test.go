package utils

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileURI(t *testing.T) {
	tests := []struct {
		name    string
		absPath string
		want    string
	}{
		{
			name:    "unix absolute path",
			absPath: "/tmp/file.png",
			want:    "file:///tmp/file.png",
		},
	}

	// Windows-specific test cases.
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name    string
			absPath string
			want    string
		}{
			name:    "windows absolute path",
			absPath: `C:\Users\test\file.png`,
			want:    "file:///C:/Users/test/file.png",
		})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FileURI(tc.absPath)
			if got != tc.want {
				t.Errorf("FileURI(%q) = %q, want %q", tc.absPath, got, tc.want)
			}
		})
	}
}

func TestFileURIToPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unix file URI",
			raw:  "file:///tmp/file.png",
			want: filepath.FromSlash("/tmp/file.png"),
		},
		{
			name: "bare unix path",
			raw:  "/tmp/other.png",
			want: filepath.FromSlash("/tmp/other.png"),
		},
	}

	if runtime.GOOS == "windows" {
		tests = append(tests, []struct {
			name string
			raw  string
			want string
		}{
			{
				name: "windows file URI",
				raw:  "file:///C:/Users/test/file.png",
				want: `C:\Users\test\file.png`,
			},
			{
				name: "windows file URI lowercase drive",
				raw:  "file:///e:/workspace/kocort/local-config/1.png",
				want: `e:\workspace\kocort\local-config\1.png`,
			},
		}...)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FileURIToPath(tc.raw)
			if got != tc.want {
				t.Errorf("FileURIToPath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	// On Unix, test a round-trip.
	if runtime.GOOS != "windows" {
		path := "/tmp/test/image.png"
		uri := FileURI(path)
		back := FileURIToPath(uri)
		if back != path {
			t.Errorf("round-trip failed: %q → %q → %q", path, uri, back)
		}
	}

	// On Windows, test a round-trip.
	if runtime.GOOS == "windows" {
		path := `E:\workspace\kocort\local-config\1.png`
		uri := FileURI(path)
		back := FileURIToPath(uri)
		if back != path {
			t.Errorf("round-trip failed: %q → %q → %q", path, uri, back)
		}
	}
}
