//go:build linux

package main

import (
	"fmt"
	"os/exec"
)

func openBrowser(url string) error {
	commands := [][]string{
		{"xdg-open", url},
		{"gio", "open", url},
	}

	var lastErr error
	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err != nil {
			lastErr = err
			continue
		}
		if err := exec.Command(command[0], command[1:]...).Start(); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no browser opener available")
	}
	return fmt.Errorf("open browser: %w", lastErr)
}
