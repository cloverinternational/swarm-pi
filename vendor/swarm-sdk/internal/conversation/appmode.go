package conversation

import "strings"

// AppMode represents the UI mode in which a conversation was created.
// It is stored as a tag on ConversationMetadata.Tags using the format "app_mode:<value>".
//
// This mirrors the Claude desktop sidebarMode pattern ("chat" | "code" | "task")
// and allows the UI sidebar and session restoration to classify conversations correctly.
type AppMode string

const (
	// AppModeChat is the lightweight conversational mode.
	// Safe tools only (read, search). Minimal MCP surface.
	AppModeChat AppMode = "chat"

	// AppModeWork is the full agent mode with skills, MCP servers and autonomous execution.
	// All tools available. Optimised for multi-step work tasks.
	AppModeWork AppMode = "work"

	// AppModeCode is the code-focused development mode.
	// File edits accepted automatically. Shell, git and editor tools active.
	AppModeCode AppMode = "code"
)

// AppModeTagPrefix is the tag prefix used in ConversationMetadata.Tags.
const AppModeTagPrefix = "app_mode:"

// AppModeTag returns the tag string for the given mode (e.g. "app_mode:chat").
func AppModeTag(mode AppMode) string {
	return AppModeTagPrefix + string(mode)
}

// ParseAppModeTag extracts the AppMode from a tag string.
// Returns ("", false) if the tag is not an app_mode tag.
func ParseAppModeTag(tag string) (AppMode, bool) {
	if !strings.HasPrefix(tag, AppModeTagPrefix) {
		return "", false
	}
	val := AppMode(strings.TrimPrefix(tag, AppModeTagPrefix))
	switch val {
	case AppModeChat, AppModeWork, AppModeCode:
		return val, true
	default:
		return "", false
	}
}

// GetAppMode returns the AppMode stored in a conversation's metadata tags.
// Returns AppModeChat as the default for legacy conversations with no tag.
func GetAppMode(tags []string) AppMode {
	for _, tag := range tags {
		if mode, ok := ParseAppModeTag(tag); ok {
			return mode
		}
	}
	return AppModeChat // default: legacy conversations are treated as Chat
}

// SetAppMode returns a new tags slice with the app_mode tag set to the given mode.
// Any existing app_mode tag is replaced.
func SetAppMode(tags []string, mode AppMode) []string {
	filtered := make([]string, 0, len(tags))
	for _, t := range tags {
		if !strings.HasPrefix(t, AppModeTagPrefix) {
			filtered = append(filtered, t)
		}
	}
	return append(filtered, AppModeTag(mode))
}

// IsValidAppMode reports whether the given string is a valid AppMode value.
func IsValidAppMode(s string) bool {
	switch AppMode(s) {
	case AppModeChat, AppModeWork, AppModeCode:
		return true
	default:
		return false
	}
}
