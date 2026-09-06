//go:build darwin

package chrome

import (
	"fmt"
	"os"
	"os/exec"
)

func platformDiscoverChrome() (string, error) {
	candidates := []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, home+"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath("google-chrome"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("Google Chrome was not found in Applications or PATH")
}
