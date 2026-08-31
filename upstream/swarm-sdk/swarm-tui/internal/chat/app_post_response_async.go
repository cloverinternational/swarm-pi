package chat

import (
	sdkobs "github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

type lastMessageCompletionUpdatedMsg struct {
	convID string
	err    error
}

type conversationTokenCountMsg struct {
	convID             string
	currentContextSize int
	err                error
}

type errorLineageLookupResultMsg struct {
	convID  string
	errorID string
	report  *sdkobs.LineageReport
	err     error
}

func (a *App) assistantMessageWithLineageErrorID(errorID string) *Message {
	if errorID == "" {
		return nil
	}

	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role != "assistant" || msg.ErrorLineage == nil {
			continue
		}
		if msg.ErrorLineage.ErrorID == errorID {
			return msg
		}
	}

	return nil
}
