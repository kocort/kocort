//go:build !windows

package main

import (
	"os"
	"strings"
)

// detectSystemLang reads POSIX locale environment variables and returns
// LangZH when the system is configured for Chinese; LangEN otherwise.
func detectSystemLang() Lang {
	for _, key := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if val := os.Getenv(key); val != "" {
			if strings.HasPrefix(strings.ToLower(val), "zh") {
				return LangZH
			}
		}
	}
	return LangEN
}
