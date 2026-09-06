package chat

import (
	"context"

	conversation "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// loadConversationMessages loads messages for a specific conversation
func (a *App) loadConversationMessages(convID string) ([]*conversation.Message, error) {
	// Use the SDK to load conversation messages
	ctx := context.Background()
	return a.sdk.GetMessages(ctx, convID)
}
