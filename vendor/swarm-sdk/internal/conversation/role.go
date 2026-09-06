// Package conversation provides message and conversation primitives.
// This is Ring 0 - pure data structures with no business logic.
package conversation

// Role represents the role of a message in a conversation.
type Role string

const (
	// RoleUser indicates a message from the user/human.
	RoleUser Role = "user"

	// RoleAssistant indicates a message from the AI assistant/agent.
	RoleAssistant Role = "assistant"

	// RoleSystem indicates a system message (instructions, context).
	RoleSystem Role = "system"

	// RoleTool indicates a message containing tool execution results.
	RoleTool Role = "tool"

	// RolePeer indicates a projected A2A-authored update from another top-level agent.
	RolePeer Role = "peer"
)

// String returns the string representation of the role.
func (r Role) String() string {
	return string(r)
}

// IsValid checks if the role is a valid role.
func (r Role) IsValid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleSystem, RoleTool, RolePeer:
		return true
	default:
		return false
	}
}
