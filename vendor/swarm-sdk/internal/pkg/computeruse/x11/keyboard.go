package x11

import (
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

// Type types text at the current cursor position.
func (b *Backend) Type(text string, viaClipboard bool) error {
	if viaClipboard {
		return b.typeViaClipboard(text)
	}

	// Use xdotool type with delay for reliability
	// xdotool type has issues with special characters and fast typing
	args := []string{"type", "--delay", "8", text}

	if _, err := b.runCommand("xdotool", args...); err != nil {
		return &computeruse.InputError{Operation: "type", Err: err}
	}

	return nil
}

// typeViaClipboard types text by pasting from clipboard.
// This is more reliable for complex text with special characters.
func (b *Backend) typeViaClipboard(text string) error {
	// Save current clipboard
	saved, err := b.ReadClipboard()
	if err != nil {
		saved = "" // Proceed without restore if read fails
	}

	// Write text to clipboard
	if err := b.WriteClipboard(text); err != nil {
		return err
	}

	// Wait for clipboard to settle
	time.Sleep(50 * time.Millisecond)

	// Paste using Ctrl+V (or Shift+Insert as fallback)
	if _, err := b.runCommand("xdotool", "key", "--delay", "8", "ctrl+v"); err != nil {
		// Try Shift+Insert as fallback
		if _, err2 := b.runCommand("xdotool", "key", "--delay", "8", "shift+Insert"); err2 != nil {
			return &computeruse.InputError{Operation: "paste", Err: err}
		}
	}

	// Wait for paste to complete
	time.Sleep(100 * time.Millisecond)

	// Restore clipboard
	if saved != "" {
		_ = b.WriteClipboard(saved) // Ignore error on restore
	}

	return nil
}

// Key presses a key combination.
// keySequence is in xdotool format: "ctrl+shift+a"
func (b *Backend) Key(keySequence string, repeat int) error {
	if repeat <= 0 {
		repeat = 1
	}

	for i := 0; i < repeat; i++ {
		if i > 0 {
			time.Sleep(8 * time.Millisecond)
		}

		args := []string{"key", "--delay", "8", keySequence}
		if _, err := b.runCommand("xdotool", args...); err != nil {
			return &computeruse.InputError{Operation: "key", Err: err}
		}
	}

	return nil
}

// HoldKey holds keys for a specified duration.
func (b *Backend) HoldKey(keys []string, durationMs int) error {
	// Map keys to xdotool format
	xdotoolKeys := make([]string, len(keys))
	for i, k := range keys {
		xdotoolKeys[i] = mapKeyName(k)
	}

	// Press all keys
	for _, k := range xdotoolKeys {
		if _, err := b.runCommand("xdotool", "keydown", k); err != nil {
			// Release any keys we pressed
			for _, rk := range xdotoolKeys {
				b.runCommand("xdotool", "keyup", rk) //nolint:errcheck
			}
			return &computeruse.InputError{Operation: "keydown", Err: err}
		}
	}

	// Wait for duration
	time.Sleep(time.Duration(durationMs) * time.Millisecond)

	// Release all keys (in reverse order)
	for i := len(xdotoolKeys) - 1; i >= 0; i-- {
		if _, err := b.runCommand("xdotool", "keyup", xdotoolKeys[i]); err != nil {
			return &computeruse.InputError{Operation: "keyup", Err: err}
		}
	}

	return nil
}

// mapKeyName maps a key name to xdotool format.
func mapKeyName(key string) string {
	key = strings.ToLower(key)

	// Common key mappings
	mappings := map[string]string{
		"escape":     "Escape",
		"esc":        "Escape",
		"enter":      "Return",
		"return":     "Return",
		"tab":        "Tab",
		"space":      "space",
		"backspace":  "BackSpace",
		"delete":     "Delete",
		"insert":     "Insert",
		"home":       "Home",
		"end":        "End",
		"pageup":     "Page_Up",
		"pagedown":   "Page_Down",
		"up":         "Up",
		"down":       "Down",
		"left":       "Left",
		"right":      "Right",
		"f1":         "F1",
		"f2":         "F2",
		"f3":         "F3",
		"f4":         "F4",
		"f5":         "F5",
		"f6":         "F6",
		"f7":         "F7",
		"f8":         "F8",
		"f9":         "F9",
		"f10":        "F10",
		"f11":        "F11",
		"f12":        "F12",
		"ctrl":       "ctrl",
		"alt":        "alt",
		"shift":      "shift",
		"super":      "super",
		"meta":       "super",
		"cmd":        "super",
		"command":    "super",
		"win":        "super",
		"capslock":   "Caps_Lock",
		"numlock":    "Num_Lock",
		"print":      "Print",
		"scrolllock": "Scroll_Lock",
		"pause":      "Pause",
	}

	if mapped, ok := mappings[key]; ok {
		return mapped
	}

	// Single character keys
	if len(key) == 1 {
		return key
	}

	// Return as-is for unknown keys
	return key
}

// KeyNamesFromSequence parses a key sequence like "ctrl+shift+a" into individual keys.
func KeyNamesFromSequence(seq string) []string {
	parts := strings.Split(seq, "+")
	keys := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			keys = append(keys, p)
		}
	}
	return keys
}

// BuildKeySequence builds a key sequence from key names.
func BuildKeySequence(keys []string) string {
	return strings.Join(keys, "+")
}
