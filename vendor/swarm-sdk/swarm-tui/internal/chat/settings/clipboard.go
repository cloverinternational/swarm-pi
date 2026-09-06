package settings

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// readClipboardText reads plain-text clipboard content using CLI tools.
// This is a settings-local copy of chat.readClipboardText to avoid an
// import cycle (chat → settings → chat). It supports Wayland (wl-paste),
// X11 (xclip, xsel), macOS (pbpaste) and Windows (powershell).
//
// The previous implementation used github.com/atotto/clipboard v0.1.4
// which does NOT support Wayland, causing Ctrl+V to silently fail on
// KDE Plasma / GNOME Wayland sessions.
func readClipboardText() (string, error) {
	switch runtime.GOOS {
	case "linux":
		// Wayland first
		if _, err := exec.LookPath("wl-paste"); err == nil {
			out, err := exec.Command("wl-paste", "--no-newline").Output()
			if err == nil && len(out) > 0 {
				return string(out), nil
			}
		}
		// X11 via xclip
		if _, err := exec.LookPath("xclip"); err == nil {
			out, err := exec.Command("xclip", "-selection", "clipboard", "-o").Output()
			if err == nil && len(out) > 0 {
				return string(out), nil
			}
		}
		// X11 via xsel
		if _, err := exec.LookPath("xsel"); err == nil {
			out, err := exec.Command("xsel", "--clipboard", "--output").Output()
			if err == nil && len(out) > 0 {
				return string(out), nil
			}
		}
	case "darwin":
		if _, err := exec.LookPath("pbpaste"); err == nil {
			out, err := exec.Command("pbpaste").Output()
			if err == nil {
				return string(out), nil
			}
		}
	case "windows":
		if _, err := exec.LookPath("powershell"); err == nil {
			out, err := exec.Command("powershell", "-command", "Get-Clipboard").Output()
			if err == nil && len(out) > 0 {
				return string(out), nil
			}
		}
	}
	return "", fmt.Errorf("no CLI clipboard tool could read text data")
}

// writeClipboardText writes text to the system clipboard using CLI tools.
// Tries all available clipboard mechanisms for the current platform.
func writeClipboardText(content string) int {
	successCount := 0

	switch runtime.GOOS {
	case "linux":
		// Wayland (wl-copy)
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd := exec.Command("wl-copy")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
			}
		}
		// X11 CLIPBOARD (xclip)
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd := exec.Command("xclip", "-selection", "clipboard", "-i")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
			}
		}
		// X11 PRIMARY (xclip) - middle-click paste
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd := exec.Command("xclip", "-selection", "primary", "-i")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
			}
		}
		// X11 CLIPBOARD (xsel)
		if _, err := exec.LookPath("xsel"); err == nil {
			cmd := exec.Command("xsel", "--clipboard", "--input")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
			}
		}
	case "darwin":
		if _, err := exec.LookPath("pbcopy"); err == nil {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
			}
		}
	case "windows":
		cmd := exec.Command("powershell", "-command", fmt.Sprintf("Set-Clipboard -Value '%s'",
			strings.ReplaceAll(content, "'", "''")))
		if err := cmd.Run(); err == nil {
			successCount++
		}
	}

	return successCount
}
