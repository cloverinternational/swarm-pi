package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

func (a *App) sendImmediateToRuntime(msg tea.Msg) {
	if a != nil && a.sdk != nil && a.sdk.logger != nil {
		a.sdk.logger.Info(context.Background(), "tui.a2a.send_immediate",
			observability.F("message_type", fmt.Sprintf("%T", msg)),
		)
	}
	a.sendToRuntime(msg)
}

func (a *App) configureA2ABridge() {
	if a == nil || a.sdk == nil {
		return
	}

	// Route agent auto-compaction through the App's shared compaction service
	// so the %-threshold path and manual /compact run the same pipeline (same
	// summarizer, file tracking, progress bar, and post-compact budget).
	a.sdk.compactFuncWirer = a.wireSharedAgentCompactFunc

	a.sdk.SetA2AConversationAllocator(func(ctx context.Context, requestedConversationID, peerHandle string) (A2AConversationResolution, error) {
		if a.sdk != nil && a.sdk.logger != nil {
			a.sdk.logger.Info(ctx, "tui.a2a.allocate_conversation",
				observability.F("requested_conversation_id", strings.TrimSpace(requestedConversationID)),
				observability.F("peer_handle", strings.TrimSpace(peerHandle)),
			)
		}
		if conversationID := strings.TrimSpace(requestedConversationID); conversationID != "" && !strings.HasPrefix(conversationID, "a2a:") {
			if a.sdk.conversationExists(ctx, conversationID) {
				if a.sdk != nil && a.sdk.logger != nil {
					a.sdk.logger.Info(ctx, "tui.a2a.allocate_existing_conversation",
						observability.F("conversation_id", conversationID),
					)
				}
				a.sendImmediateToRuntime(a2aFocusConversationMsg{ConversationID: conversationID})
				return A2AConversationResolution{
					ConversationID: conversationID,
					AutoFocus:      true,
				}, nil
			}
		}

		conv, err := a.sdk.CreateConversationWithBranch(ctx, "chat", "")
		if err != nil {
			return A2AConversationResolution{}, err
		}
		if a.sdk != nil && a.sdk.logger != nil {
			a.sdk.logger.Info(ctx, "tui.a2a.allocate_new_conversation",
				observability.F("conversation_id", conv.ID),
			)
		}
		a.sendImmediateToRuntime(a2aFocusConversationMsg{ConversationID: conv.ID})
		return A2AConversationResolution{
			ConversationID: conv.ID,
			AutoFocus:      true,
		}, nil
	})

	a.sdk.SetA2AEventCallback(func(event A2AEvent) {
		switch event.Kind {
		case A2AEventPeerMessage:
			if event.Message == nil {
				return
			}
			converted := convertSDKMessages([]*conversation.Message{event.Message})
			if len(converted) == 0 {
				return
			}
			a.sendImmediateToRuntime(a2aPeerMessageMsg{
				ConversationID: event.ConversationID,
				Message:        converted[0],
				PeerHandle:     event.PeerHandle,
				AutoFocus:      event.AutoFocus,
			})
		case A2AEventInboundStarted:
			a.sendImmediateToRuntime(a2aInboundStartedMsg{
				ConversationID: event.ConversationID,
				PeerHandle:     event.PeerHandle,
			})
		case A2AEventInboundUpdate:
			if event.Update == nil {
				return
			}
			logDebug("[A2A] app callback inbound update start type=%s conv=%s peer=%s", event.Update.UpdateType(), event.ConversationID, event.PeerHandle)
			a.sendToRuntime(a2aInboundUpdateMsg{
				ConversationID: event.ConversationID,
				PeerHandle:     event.PeerHandle,
				Update:         event.Update,
			})
			logDebug("[A2A] app callback inbound update done type=%s conv=%s peer=%s", event.Update.UpdateType(), event.ConversationID, event.PeerHandle)
		case A2AEventInboundDone:
			a.sendImmediateToRuntime(a2aInboundFinishedMsg{
				ConversationID: event.ConversationID,
				PeerHandle:     event.PeerHandle,
				Err:            event.Err,
			})
		}
	})

	a.syncSDKActiveConversation()

	// Wire workspace hub peer presence callbacks.
	// These are called from goroutines, so we route through sendToRuntime.
	if a.hub != nil {
		a.hub.OnPeerJoined = func(handle string) {
			a.sendToRuntime(swarmChatPeerJoinedMsg{Handle: handle})
		}
		a.hub.OnPeerLeft = func(handle string) {
			a.sendToRuntime(swarmChatPeerLeftMsg{Handle: handle})
		}
		a.hub.OnMessage = func(from, content string) {
			a.sendToRuntime(swarmChatInboundMsg{From: from, Content: content})
		}
		// OnPrompt handles HubMsgPromptRequest — another terminal asking this
		// one to run a prompt and return the result. Show it inline so the
		// user can see it, and acknowledge so the requester isn't left hanging.
		// Full AI-execution promotion is future work.
		a.hub.OnPrompt = func(from, prompt string) (string, error) {
			a.sendToRuntime(swarmChatInboundMsg{
				From:    from,
				Content: fmt.Sprintf("[prompt request] %s", prompt),
			})
			return tr("classic.a2a.prompt_seen", a.a2aHandle), nil
		}
	}
}

func (a *App) setCurrentConversationID(convID string) {
	a.currentConvID = convID
	a.syncSDKActiveConversation()
	a.syncPlanModeSnapshotForConv(convID)
}

func (a *App) clearCurrentConversationID() {
	a.currentConvID = ""
	a.syncSDKActiveConversation()
	a.syncPlanModeSnapshotForConv("")
}

func (a *App) syncSDKActiveConversation() {
	if a == nil || a.sdk == nil {
		return
	}
	if strings.TrimSpace(a.currentConvID) == "" {
		a.sdk.ClearActiveConversation()
		return
	}
	a.sdk.SetActiveConversation(a.currentConvID)
}

func (a *App) nextUpdateSequence() int {
	a.updateSequenceMu.Lock()
	defer a.updateSequenceMu.Unlock()
	a.globalUpdateSequence++
	return a.globalUpdateSequence
}

func (a *App) queueAgentIntermediateUpdate(update agent.IntermediateUpdate) int {
	return a.queueAgentIntermediateUpdateWithContext("", "", update)
}

func (a *App) queueAgentIntermediateUpdateWithContext(source a2aExecutionSource, conversationID string, update agent.IntermediateUpdate) int {
	if a == nil || update == nil {
		return 0
	}

	seq := a.nextUpdateSequence()
	switch u := update.(type) {
	case agent.ToolCallUpdate:
		a.sendToRuntime(agentToolCallMsg{
			toolName:       u.Name,
			parameters:     u.Parameters,
			callID:         u.ID,
			sequence:       int(u.Sequence), // Use SDK-provided sequence for correct ordering
			source:         source,
			conversationID: conversationID,
		})
	case agent.ToolResultUpdate:
		a.sendToRuntime(agentToolResultMsg{
			callID:         u.ID,
			output:         u.Output,
			err:            u.Error,
			sequence:       int(u.Sequence), // Use SDK-provided sequence for correct ordering
			contentBlocks:  u.ContentBlocks,
			metadata:       u.Metadata,
			source:         source,
			conversationID: conversationID,
		})
	case agent.ThinkingUpdate:
		a.sendToRuntime(agentThinkingMsg{
			content:        u.Content,
			append:         u.Append,
			sequence:       int(u.Sequence), // Use SDK-provided sequence for correct ordering
			source:         source,
			conversationID: conversationID,
		})
	case agent.ContentUpdate:
		a.sendToRuntime(agentContentUpdateMsg{
			content:        u.Content,
			append:         u.Append,
			sequence:       int(u.Sequence), // Use SDK-provided sequence for correct ordering
			source:         source,
			conversationID: conversationID,
		})
	case agent.AssistantMessageUpdate:
		a.sendToRuntime(agentAssistantMessageCompleteMsg{
			content:        u.Content,
			thinking:       u.Thinking,
			finishReason:   u.FinishReason,
			turn:           u.Turn,
			inputTokens:    u.InputTokens,
			outputTokens:   u.OutputTokens,
			sequence:       int(u.Sequence),
			source:         source,
			conversationID: conversationID,
		})
	case agent.TokenCountUpdate:
		a.sendToRuntime(tokenUpdateMsg{
			inputTokens:          u.InputTokens,
			outputTokens:         u.OutputTokens,
			contextWindow:        u.ContextWindow,
			effectiveWindow:      u.EffectiveWindow,
			autoCompactThreshold: u.AutoCompactThreshold,
			pctUsed:              u.PctUsed,
			isFinal:              false,
			source:               source,
			conversationID:       conversationID,
		})
	case agent.CompactionNeededUpdate:
		// The agent crossed the auto-compaction threshold and (when a
		// CompactFunc is wired — see wireSharedAgentCompactFunc) is about to
		// compact in-loop. Surface it so the UI can show the determinate
		// compaction progress bar instead of appearing stalled.
		a.sendToRuntime(agentAutoCompactionStartedMsg{
			currentTokens:  u.CurrentTokens,
			threshold:      u.Threshold,
			contextLimit:   u.ContextLimit,
			source:         source,
			conversationID: conversationID,
		})
	case agent.CompactionDoneUpdate:
		// In-loop compaction finished; refresh token state and restore the
		// user's loading indicator.
		a.sendToRuntime(agentAutoCompactionDoneMsg{
			tokensBefore:   u.TokensBefore,
			tokensAfter:    u.TokensAfter,
			source:         source,
			conversationID: conversationID,
		})
	case agent.CompactionFailedUpdate:
		a.sendToRuntime(agentAutoCompactionFailedMsg{
			errMsg:         u.Error,
			fatal:          u.Fatal,
			source:         source,
			conversationID: conversationID,
		})
	case agent.HookExecutionUpdate:
		a.sendToRuntime(agentHookExecutionMsg{
			hookName:       u.HookName,
			toolName:       u.ToolName,
			toolCallID:     u.ToolCallID,
			phase:          u.Phase,
			success:        u.Success,
			output:         u.Output,
			blocked:        u.Blocked,
			errMsg:         u.Error,
			sequence:       int(u.Sequence), // Use SDK-provided sequence for correct ordering
			source:         source,
			conversationID: conversationID,
		})
	case agent.ToolOutputChunk:
		a.sendToRuntime(agentToolOutputChunkMsg{
			callID:         u.ID,
			chunk:          u.Chunk,
			stream:         u.Stream,
			sequence:       seq,
			source:         source,
			conversationID: conversationID,
		})
	case agent.SubAgentUpdate:
		a.sendToRuntime(agentSubAgentUpdateMsg{
			agentID:        u.AgentID,
			agentName:      u.AgentName,
			update:         u.Update,
			sequence:       seq,
			source:         source,
			conversationID: conversationID,
		})
		// Special handling: if the sub-agent's chain is exhausted, we need to
		// show a profile picker and send the FallbackDecision back to the sub-agent.
		if exhausted, ok := u.Update.(agent.ExhaustedUpdate); ok && exhausted.Response != nil {
			errMsg := ""
			if len(exhausted.Errors) > 0 && exhausted.Errors[len(exhausted.Errors)-1] != nil {
				errMsg = exhausted.Errors[len(exhausted.Errors)-1].Error()
			}
			a.sendToRuntime(subAgentExhaustedMsg{
				agentID:        u.AgentID,
				agentName:      u.AgentName,
				provider:       exhausted.Provider,
				model:          exhausted.Model,
				attempts:       exhausted.Attempts,
				errMsg:         errMsg,
				response:       exhausted.Response,
				source:         source,
				conversationID: conversationID,
			})
		}
	case agent.FallbackUpdate:
		errMsg := ""
		if u.Error != nil {
			errMsg = u.Error.Error()
		}
		a.sendToRuntime(agentFallbackMsg{
			provider:       u.Provider,
			model:          u.Model,
			fromProvider:   u.FromProvider,
			fromModel:      u.FromModel,
			attemptIndex:   u.AttemptIndex,
			status:         u.Status,
			errMsg:         errMsg,
			duration:       u.Duration,
			sequence:       seq,
			source:         source,
			conversationID: conversationID,
		})
	case agent.ExhaustedUpdate:
		errMsg := ""
		if len(u.Errors) > 0 && u.Errors[len(u.Errors)-1] != nil {
			errMsg = u.Errors[len(u.Errors)-1].Error()
		}
		a.sendToRuntime(agentExhaustedMsg{
			provider:       u.Provider,
			model:          u.Model,
			attempts:       u.Attempts,
			errMsg:         errMsg,
			response:       u.Response,
			source:         source,
			conversationID: conversationID,
		})
	default:
		logDebug("queueAgentIntermediateUpdate: unknown update type %T", update)
	}
	return seq
}

func (a *App) appendPeerMessageIfMissing(msg *Message) bool {
	if a == nil || msg == nil || msg.Role != string(conversation.RolePeer) {
		return false
	}
	for i := range a.messages {
		existing := &a.messages[i]
		if existing.Role != string(conversation.RolePeer) || existing.A2A == nil || msg.A2A == nil {
			continue
		}
		// Primary dedup: match by MessageID + session (most reliable).
		if existing.A2A.MessageID != "" && existing.A2A.MessageID == msg.A2A.MessageID &&
			existing.A2A.RemoteAgentSession == msg.A2A.RemoteAgentSession {
			return false
		}
		// BUG-FIX: fallback dedup when MessageID is empty.
		// Match by (handle + content) within a 5-second delivery window.
		if existing.A2A.MessageID == "" && msg.A2A.MessageID == "" &&
			existing.A2A.RemoteAgentHandle == msg.A2A.RemoteAgentHandle &&
			existing.Content == msg.Content &&
			!existing.Timestamp.IsZero() && !msg.Timestamp.IsZero() &&
			existing.Timestamp.Before(msg.Timestamp.Add(5*time.Second)) &&
			msg.Timestamp.Before(existing.Timestamp.Add(5*time.Second)) {
			return false
		}
	}
	a.messages = append(a.messages, *msg)
	return true
}

func (a *App) syncPeerMessagesFromSDK(convID string) bool {
	if a == nil || a.sdk == nil || strings.TrimSpace(convID) == "" {
		return false
	}
	sdkMessages, err := a.sdk.GetMessages(context.Background(), convID)
	if err != nil {
		return false
	}
	appended := false
	for _, msg := range convertSDKMessages(sdkMessages) {
		if msg.Role != string(conversation.RolePeer) {
			continue
		}
		if a.appendPeerMessageIfMissing(msg) {
			appended = true
		}
	}
	return appended
}

func (a *App) syncConversationFromSDK(convID string) bool {
	if a == nil || a.sdk == nil || strings.TrimSpace(convID) == "" {
		return false
	}

	wasAtBottom := false
	if a.msgViewport != nil {
		wasAtBottom = a.msgViewport.AtBottom()
	}

	a.loadMessagesFromSDK(convID)
	a.addSystemPromptToHistory()
	a.invalidateViewportCache()
	a.updateViewportContent()
	if wasAtBottom && a.msgViewport != nil {
		a.msgViewport.GotoBottom()
	}
	return true
}

func (a *App) visibleA2AConversation(conversationID string) bool {
	return strings.TrimSpace(conversationID) != "" && conversationID == a.currentConvID
}

func (a *App) shouldProcessA2AQueuedUpdate(source a2aExecutionSource, conversationID string) bool {
	if source != a2aExecutionSourceInbound {
		return true
	}
	conversationID = strings.TrimSpace(conversationID)
	return conversationID != "" && conversationID == a.a2aInboundConvID && a.visibleA2AConversation(conversationID)
}

func formatA2ANotification(peerHandle, suffix string) string {
	peerHandle = strings.TrimSpace(peerHandle)
	if peerHandle == "" {
		peerHandle = tr("classic.a2a.peer")
	}
	return fmt.Sprintf("A2A %s %s", peerHandle, suffix)
}
