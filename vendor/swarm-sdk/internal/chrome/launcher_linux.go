//go:build linux

package chrome

import (
	"fmt"
	"os/exec"
)

func platformDiscoverChrome() (string, error) {
	for _, candidate := range []string{"google-chrome-stable", "google-chrome"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("google-chrome-stable and google-chrome are not on PATH")
}
