package utils

import (
	"net/url"
	"os"
	"path/filepath"
)

// FileURI converts an absolute filesystem path to a proper file:// URI
// using net/url for cross-platform correctness.
//
// Examples:
//
//	Unix:    /tmp/file.png        → file:///tmp/file.png
//	Windows: C:\Users\f\file.png  → file:///C:/Users/f/file.png
func FileURI(absPath string) string {
	p := filepath.ToSlash(absPath)
	// On Windows, absPath is like "C:/Users/..." (no leading slash).
	// file:// URI requires the path component to be absolute (start with /),
	// otherwise url.URL treats the drive letter as a host component.
	if len(p) > 0 && p[0] != '/' {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// FileURIToPath converts a file:// URI (or a bare path) to an OS-native
// filesystem path using net/url.Parse for cross-platform correctness.
//
// Examples:
//
//	file:///C:/Users/file.png  → C:\Users\file.png  (Windows)
//	file:///tmp/file.png       → /tmp/file.png       (Unix)
//	/tmp/file.png              → /tmp/file.png       (bare path fallback)
func FileURIToPath(raw string) string {
	u, err := url.Parse(raw)
	if err == nil && u.Scheme == "file" {
		return fileURIPathToNative(u.Path)
	}
	// Fallback: treat as a plain path.
	return filepath.FromSlash(raw)
}

// fileURIPathToNative converts the path component of a parsed file URI to
// an OS-native filesystem path. On Windows, file:///C:/path parses to
// u.Path="/C:/path"; filepath.FromSlash yields "\C:\path" — the leading
// separator before the drive letter must be stripped.
func fileURIPathToNative(p string) string {
	native := filepath.FromSlash(p)
	// If the native path starts with a separator followed by a volume
	// name (e.g. "\C:\..."), strip the leading separator.
	// On Unix filepath.VolumeName always returns "", so the leading "/"
	// is preserved.
	if len(native) > 1 && os.IsPathSeparator(native[0]) && filepath.VolumeName(native[1:]) != "" {
		native = native[1:]
	}
	return native
}
