package chat

import (
	"bytes"
	"fmt"
	"image/png"
	"os/exec"
	"runtime"
	"strings"

	"golang.design/x/clipboard"
)

// writeClipboard writes content to ALL available clipboards
// Returns the number of successful clipboard writes
func writeClipboard(content string) int {
	successCount := 0

	switch runtime.GOOS {
	case "linux":
		// Try ALL Linux clipboard mechanisms

		// 1. Wayland clipboard (wl-copy)
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd := exec.Command("wl-copy")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
				logDebug("✓ Copied to Wayland clipboard (wl-copy)")
			}
		}

		// 2. X11 CLIPBOARD selection (xclip)
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd := exec.Command("xclip", "-selection", "clipboard", "-i")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
				logDebug("✓ Copied to X11 CLIPBOARD (xclip)")
			}
		}

		// 3. X11 PRIMARY selection (xclip) - middle-click paste
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd := exec.Command("xclip", "-selection", "primary", "-i")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
				logDebug("✓ Copied to X11 PRIMARY (xclip)")
			}
		}

		// 4. X11 CLIPBOARD selection (xsel)
		if _, err := exec.LookPath("xsel"); err == nil {
			cmd := exec.Command("xsel", "--clipboard", "--input")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
				logDebug("✓ Copied to X11 CLIPBOARD (xsel)")
			}
		}

		// 5. X11 PRIMARY selection (xsel)
		if _, err := exec.LookPath("xsel"); err == nil {
			cmd := exec.Command("xsel", "--primary", "--input")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
				logDebug("✓ Copied to X11 PRIMARY (xsel)")
			}
		}

	case "darwin":
		// macOS clipboard (pbcopy)
		if _, err := exec.LookPath("pbcopy"); err == nil {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err == nil {
				successCount++
				logDebug("✓ Copied to macOS clipboard (pbcopy)")
			}
		}

	case "windows":
		// Windows clipboard (PowerShell)
		cmd := exec.Command("powershell", "-command", fmt.Sprintf("Set-Clipboard -Value '%s'",
			strings.ReplaceAll(content, "'", "''"))) // Escape single quotes
		if err := cmd.Run(); err == nil {
			successCount++
			logDebug("✓ Copied to Windows clipboard")
		}
	}

	return successCount
}

// ClipboardContent represents clipboard data with type detection
type ClipboardContent struct {
	IsImage   bool
	ImageData []byte // PNG encoded
	Text      string
	MimeType  string
}

var clipboardInitialized = false

// initClipboard initializes the clipboard library (call once at startup)
func initClipboard() error {
	if clipboardInitialized {
		return nil
	}

	err := clipboard.Init()
	if err != nil {
		return fmt.Errorf("failed to initialize clipboard: %w", err)
	}

	clipboardInitialized = true
	logDebug("✓ Clipboard initialized for image support")
	return nil
}

// readClipboardImageViaCLI reads image/png data from the clipboard using
// xclip or wl-paste as a fallback when golang.design/x/clipboard returns nothing.
//
// This is essential inside tmux because:
//   - The X11 library may fail to open a secondary display connection
//   - XInternAtom("image/png", True) returns None if the atom doesn't exist on
//     the X server yet, causing the library to silently return -2 (errUnsupported)
//   - tmux blocks OSC 52 passthrough when allow-passthrough is off
//
// xclip and wl-paste bypass all of that by speaking directly to the X11/Wayland
// clipboard daemon.
func readClipboardImageViaCLI() ([]byte, error) {
	// xclip: request image/png MIME type directly from X11 CLIPBOARD selection
	if _, err := exec.LookPath("xclip"); err == nil {
		out, err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output()
		if err == nil && len(out) > 0 {
			if _, decErr := png.Decode(bytes.NewReader(out)); decErr == nil {
				logDebug("✓ readClipboardImageViaCLI: xclip returned %d bytes of image/png", len(out))
				return out, nil
			}
			logDebug("✗ readClipboardImageViaCLI: xclip data failed PNG decode")
		}
		logDebug("✗ readClipboardImageViaCLI: xclip failed or returned no data: %v", err)
	}

	// wl-paste: Wayland clipboard daemon
	if _, err := exec.LookPath("wl-paste"); err == nil {
		out, err := exec.Command("wl-paste", "--type", "image/png").Output()
		if err == nil && len(out) > 0 {
			if _, decErr := png.Decode(bytes.NewReader(out)); decErr == nil {
				logDebug("✓ readClipboardImageViaCLI: wl-paste returned %d bytes of image/png", len(out))
				return out, nil
			}
			logDebug("✗ readClipboardImageViaCLI: wl-paste data failed PNG decode")
		}
		logDebug("✗ readClipboardImageViaCLI: wl-paste failed or returned no data: %v", err)
	}

	return nil, fmt.Errorf("no CLI clipboard tool could read image/png data")
}

// readClipboardText reads plain-text clipboard content using CLI tools.
//
// This is called by the Ctrl+V keyboard handler as a fallback when bubbletea's
// bracketed-paste (tea.PasteMsg) never fires, which happens in some tmux
// configurations because:
//   - tmux's assume-paste-time intercepts rapid input and handles it itself
//   - tmux's allow-passthrough=off strips OSC 52 clipboard escape sequences
func readClipboardText() (string, error) {
	switch runtime.GOOS {
	case "linux":
		// Wayland first
		if _, err := exec.LookPath("wl-paste"); err == nil {
			out, err := exec.Command("wl-paste", "--no-newline").Output()
			if err == nil && len(out) > 0 {
				logDebug("✓ readClipboardText: wl-paste returned %d chars", len(out))
				return string(out), nil
			}
		}
		// X11 via xclip
		if _, err := exec.LookPath("xclip"); err == nil {
			out, err := exec.Command("xclip", "-selection", "clipboard", "-o").Output()
			if err == nil && len(out) > 0 {
				logDebug("✓ readClipboardText: xclip returned %d chars", len(out))
				return string(out), nil
			}
		}
		// X11 via xsel
		if _, err := exec.LookPath("xsel"); err == nil {
			out, err := exec.Command("xsel", "--clipboard", "--output").Output()
			if err == nil && len(out) > 0 {
				logDebug("✓ readClipboardText: xsel returned %d chars", len(out))
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
		// PowerShell Get-Clipboard for Windows
		if _, err := exec.LookPath("powershell"); err == nil {
			out, err := exec.Command("powershell", "-command", "Get-Clipboard").Output()
			if err == nil && len(out) > 0 {
				logDebug("✓ readClipboardText: powershell returned %d chars", len(out))
				return string(out), nil
			}
		}
	}
	return "", fmt.Errorf("no CLI clipboard tool could read text data")
}

// readClipboardContent reads clipboard and detects if it contains image or text.
//
// Strategy (most-to-least reliable inside tmux):
//  1. CLI tools (xclip/wl-paste) for image/png  – work even inside tmux where
//     the golang.design/x/clipboard X11 connection may fail or the image/png
//     atom may not be registered yet.
//  2. golang.design/x/clipboard (CGo / X11 library) for image – works outside
//     tmux and in environments with a proper X11 session.
//  3. CLI tools for text.
//  4. golang.design/x/clipboard for text – final fallback.
func readClipboardContent() (*ClipboardContent, error) {
	content := &ClipboardContent{}

	// ── Step 1: CLI image read (tmux-safe) ──────────────────────────────────
	if runtime.GOOS == "linux" {
		if imgData, err := readClipboardImageViaCLI(); err == nil && len(imgData) > 0 {
			content.IsImage = true
			content.ImageData = imgData
			content.MimeType = "image/png"
			logDebug("✓ readClipboardContent: image from CLI tools (%d bytes)", len(imgData))
			return content, nil
		}
		logDebug("readClipboardContent: CLI image read found no image, falling back to X11 library")
	}

	// ── Step 2: X11 library image read ──────────────────────────────────────
	if err := initClipboard(); err == nil {
		imageData := clipboard.Read(clipboard.FmtImage)
		if len(imageData) > 0 {
			if _, pngErr := png.Decode(bytes.NewReader(imageData)); pngErr == nil {
				content.IsImage = true
				content.ImageData = imageData
				content.MimeType = "image/png"
				logDebug("✓ readClipboardContent: image from X11 library (%d bytes)", len(imageData))
				return content, nil
			}
			logDebug("✗ readClipboardContent: X11 library image data failed PNG decode")
		}
	}

	// ── Step 3: CLI text read (tmux-safe) ───────────────────────────────────
	if runtime.GOOS == "linux" {
		if text, err := readClipboardText(); err == nil && text != "" {
			content.IsImage = false
			content.Text = text
			content.MimeType = "text/plain"
			logDebug("✓ readClipboardContent: text from CLI tools (%d chars)", len(text))
			return content, nil
		}
	}

	// ── Step 4: X11 library text read ───────────────────────────────────────
	if err := initClipboard(); err == nil {
		textData := clipboard.Read(clipboard.FmtText)
		if len(textData) > 0 {
			content.IsImage = false
			content.Text = string(textData)
			content.MimeType = "text/plain"
			logDebug("✓ readClipboardContent: text from X11 library (%d chars)", len(content.Text))
			return content, nil
		}
	}

	return nil, fmt.Errorf("clipboard is empty or contains unsupported format")
}
