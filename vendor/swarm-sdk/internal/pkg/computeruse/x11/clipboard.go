package x11

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

// ReadClipboard reads the current clipboard content.
func (b *Backend) ReadClipboard() (string, error) {
	// Prefer xclip, fallback to xsel
	if b.hasXclip {
		output, err := b.runCommand("xclip", "-o", "-selection", "clipboard")
		if err != nil {
			return "", &computeruse.InputError{Operation: "read_clipboard", Err: err}
		}
		return output, nil
	}

	if b.hasXsel {
		output, err := b.runCommand("xsel", "-o", "-b")
		if err != nil {
			return "", &computeruse.InputError{Operation: "read_clipboard", Err: err}
		}
		return output, nil
	}

	return "", computeruse.ErrClipboardFailed
}

// WriteClipboard writes text to the clipboard.
func (b *Backend) WriteClipboard(text string) error {
	// Prefer xclip, fallback to xsel
	if b.hasXclip {
		err := b.runCommandWithInput("xclip", text, "-i", "-selection", "clipboard")
		if err != nil {
			return &computeruse.InputError{Operation: "write_clipboard", Err: err}
		}
		return nil
	}

	if b.hasXsel {
		err := b.runCommandWithInput("xsel", text, "-i", "-b")
		if err != nil {
			return &computeruse.InputError{Operation: "write_clipboard", Err: err}
		}
		return nil
	}

	return computeruse.ErrClipboardFailed
}
