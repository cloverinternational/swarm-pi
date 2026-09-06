// Package inject provides event injection for headless TUI automation.
package inject

import (
	tea "charm.land/bubbletea/v2"
)

// Injector creates Bubbletea messages for automation.
type Injector struct {
	width  int
	height int
}

// NewInjector creates a new event injector.
func NewInjector(width, height int) *Injector {
	return &Injector{
		width:  width,
		height: height,
	}
}

// Resize updates the injector's dimensions.
func (i *Injector) Resize(width, height int) {
	i.width = width
	i.height = height
}

// Key creates a key press message from a key string.
func (i *Injector) Key(key string) tea.Msg {
	return keyFromString(key)
}

// Keys creates multiple key messages.
func (i *Injector) Keys(keys ...string) []tea.Msg {
	msgs := make([]tea.Msg, len(keys))
	for idx, key := range keys {
		msgs[idx] = i.Key(key)
	}
	return msgs
}

// WindowSize creates a window size message.
func (i *Injector) WindowSize(width, height int) tea.Msg {
	i.width = width
	i.height = height
	return tea.WindowSizeMsg{
		Width:  width,
		Height: height,
	}
}

// Rune creates a key message for a single rune.
func (i *Injector) Rune(r rune) tea.Msg {
	return tea.KeyPressMsg{
		Code: r,
		Text: string(r),
	}
}

// Text creates one text input message for an entire string.
// This is different from sending Rune repeatedly: the chat input has rapid-input
// buffering for paste detection, so back-to-back rune events can leave the 2nd+
// characters buffered until a later key arrives.
func (i *Injector) Text(s string) tea.Msg {
	runes := []rune(s)
	if len(runes) == 0 {
		return tea.KeyPressMsg{}
	}
	return tea.KeyPressMsg{
		Code: runes[0],
		Text: s,
	}
}

// Runes creates key messages for a string of runes.
func (i *Injector) Runes(s string) []tea.Msg {
	msgs := make([]tea.Msg, len(s))
	for idx, r := range s {
		msgs[idx] = i.Rune(r)
	}
	return msgs
}

// keyFromString converts a key string to a tea.KeyPressMsg.
func keyFromString(key string) tea.KeyPressMsg {
	// Check for special keys first
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "delete":
		return tea.KeyPressMsg{Code: tea.KeyDelete}
	case "esc", "escape":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}

	// Arrow keys
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}

	// Page navigation
	case "pgup", "pageup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown", "pagedown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}

	// Ctrl combinations
	case "ctrl+a":
		return tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	case "ctrl+b":
		return tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+e":
		return tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}
	case "ctrl+f":
		return tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}
	case "ctrl+g":
		return tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	case "ctrl+h":
		return tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl}
	case "ctrl+i":
		return tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl}
	case "ctrl+j":
		return tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	case "ctrl+l":
		return tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}
	case "ctrl+m":
		return tea.KeyPressMsg{Code: 'm', Mod: tea.ModCtrl}
	case "ctrl+n":
		return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	case "ctrl+o":
		return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	case "ctrl+q":
		return tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}
	case "ctrl+r":
		return tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	case "ctrl+v":
		return tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	case "ctrl+w":
		return tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}
	case "ctrl+x":
		return tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}
	case "ctrl+y":
		return tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}
	case "ctrl+z":
		return tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}

	// Function keys
	case "f1":
		return tea.KeyPressMsg{Code: tea.KeyF1}
	case "f2":
		return tea.KeyPressMsg{Code: tea.KeyF2}
	case "f3":
		return tea.KeyPressMsg{Code: tea.KeyF3}
	case "f4":
		return tea.KeyPressMsg{Code: tea.KeyF4}
	case "f5":
		return tea.KeyPressMsg{Code: tea.KeyF5}
	case "f6":
		return tea.KeyPressMsg{Code: tea.KeyF6}
	case "f7":
		return tea.KeyPressMsg{Code: tea.KeyF7}
	case "f8":
		return tea.KeyPressMsg{Code: tea.KeyF8}
	case "f9":
		return tea.KeyPressMsg{Code: tea.KeyF9}
	case "f10":
		return tea.KeyPressMsg{Code: tea.KeyF10}
	case "f11":
		return tea.KeyPressMsg{Code: tea.KeyF11}
	case "f12":
		return tea.KeyPressMsg{Code: tea.KeyF12}

	default:
		// Single character
		if len(key) == 1 {
			r := rune(key[0])
			return tea.KeyPressMsg{
				Code: r,
				Text: key,
			}
		}
		// Multi-character, treat as text
		if len(key) > 0 {
			r := []rune(key)[0]
			return tea.KeyPressMsg{
				Code: r,
				Text: key,
			}
		}
		return tea.KeyPressMsg{}
	}
}

// MouseButton represents a mouse button.
type MouseButton int

const (
	MouseLeft MouseButton = iota
	MouseMiddle
	MouseRight
	MouseWheelUp
	MouseWheelDown
)

// Mouse creates a mouse release message.
func (i *Injector) Mouse(x, y int, button MouseButton) tea.Msg {
	var btn tea.MouseButton
	switch button {
	case MouseLeft:
		btn = tea.MouseLeft
	case MouseMiddle:
		btn = tea.MouseMiddle
	case MouseRight:
		btn = tea.MouseRight
	case MouseWheelUp:
		btn = tea.MouseWheelUp
	case MouseWheelDown:
		btn = tea.MouseWheelDown
	}

	return tea.MouseReleaseMsg{
		X:      x,
		Y:      y,
		Button: btn,
	}
}

// MouseClick creates mouse click and release messages.
func (i *Injector) MouseClick(x, y int, button MouseButton) []tea.Msg {
	var btn tea.MouseButton
	switch button {
	case MouseLeft:
		btn = tea.MouseLeft
	case MouseMiddle:
		btn = tea.MouseMiddle
	case MouseRight:
		btn = tea.MouseRight
	}

	return []tea.Msg{
		tea.MouseClickMsg{X: x, Y: y, Button: btn},
		tea.MouseReleaseMsg{X: x, Y: y, Button: btn},
	}
}

// Scroll creates a mouse wheel message.
func (i *Injector) Scroll(x, y int, up bool) tea.Msg {
	btn := tea.MouseWheelDown
	if up {
		btn = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{
		X:      x,
		Y:      y,
		Button: btn,
	}
}
