package chat

import (
	"context"
)

// GetConversationTokens returns the token count for the specified conversation.
// This returns CurrentContextSize (the latest input_tokens from the API) if available,
// as it represents the actual conversation size sent to the model.
func (sdk *SDKIntegration) GetConversationTokens(ctx context.Context, convID string) int {
	if sdk == nil || convID == "" {
		return 0
	}
	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		return 0
	}
	if conv.CurrentContextSize > 0 {
		return conv.CurrentContextSize
	}
	return conv.TotalTokens
}

// UpdateConversationTokens updates the token count for the specified conversation.
func (sdk *SDKIntegration) UpdateConversationTokens(ctx context.Context, convID string, inputTokens int) error {
	if sdk == nil || convID == "" {
		return nil
	}
	if inputTokens <= 0 {
		return nil
	}
	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		logDebug("[SDK-TOKENS] UpdateConversationTokens: failed to resume conversation %s: %v", convID, err)
		return err
	}
	prevContextSize := conv.CurrentContextSize
	conv.UpdateContextSize(inputTokens)
	if err := sdk.saveConv(ctx, conv); err != nil {
		logDebug("[SDK-TOKENS] UpdateConversationTokens: failed to save: %v", err)
		return err
	}
	logDebug("[SDK-TOKENS] UpdateConversationTokens: updated %s: prev=%d new=%d", convID, prevContextSize, inputTokens)
	return nil
}

// SetTokenUpdateCallback sets a callback function for real-time token updates.
func (sdk *SDKIntegration) SetTokenUpdateCallback(callback func(TokenUpdate)) {
	if sdk == nil {
		return
	}
	sdk.tokenUpdateCallback = callback
}

// GetTokenUpdateChannel returns a channel for receiving token updates.
func (sdk *SDKIntegration) GetTokenUpdateChannel() <-chan TokenUpdate {
	if sdk == nil {
		return nil
	}
	if sdk.tokenUpdateCh == nil {
		sdk.tokenUpdateCh = make(chan TokenUpdate, 10)
	}
	return sdk.tokenUpdateCh
}

// publishTokenUpdate sends a token update to subscribers.
func (sdk *SDKIntegration) publishTokenUpdate(update TokenUpdate) {
	if sdk == nil {
		return
	}
	if sdk.tokenUpdateCallback != nil {
		sdk.tokenUpdateCallback(update)
	}
	if sdk.tokenUpdateCh != nil {
		select {
		case sdk.tokenUpdateCh <- update:
		default:
		}
	}
}
