package main
//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"
)

// detectSystemLang checks environment variables first, then falls back to
// the Windows GetUserDefaultUILanguage API.
func detectSystemLang() Lang {
	// Environment variables take precedence (allows user override).
	for _, key := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if val := os.Getenv(key); val != "" {
			if strings.HasPrefix(strings.ToLower(val), "zh") {
				return LangZH
			}
			// Explicit non-Chinese locale set — respect it.
			return LangEN
		}
	}

	// Fall back to the Windows user-default UI language.
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetUserDefaultUILanguage")
	langID, _, _ := proc.Call()

	// Primary language ID is the lower 10 bits of the LANGID.
	// LANG_CHINESE = 0x04
	const langChinese = 0x04
	if langID&0x3FF == langChinese {
		return LangZH
	}

	return LangEN
}
