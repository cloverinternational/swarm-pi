//go:build windows

package chrome

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func platformDiscoverChrome() (string, error) {
	for _, root := range []string{
		os.Getenv("PROGRAMFILES"),
		os.Getenv("PROGRAMFILES(X86)"),
		os.Getenv("LOCALAPPDATA"),
	} {
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath("chrome.exe"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("chrome.exe was not found in PATH or standard install locations")
}
