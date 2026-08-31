package chat

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/safego"
)

type a2aExecutionSource string

const (
	a2aExecutionSourceLocal   a2aExecutionSource = "local"
	a2aExecutionSourceInbound a2aExecutionSource = "a2a_inbound"
)

type a2aExecutionSourceKey struct{}
type a2aConversationIDKey struct{}
type a2aPeerHandleKey struct{}

// A2AEventKind identifies the kind of host-facing A2A callback event.
type A2AEventKind string

const (
	A2AEventPeerMessage    A2AEventKind = "peer_message"
	A2AEventInboundStarted A2AEventKind = "inbound_started"
	A2AEventInboundUpdate  A2AEventKind = "inbound_update"
	A2AEventInboundDone    A2AEventKind = "inbound_done"
)

// A2AConversationResolution tells the SDK where projected peer traffic should land.
type A2AConversationResolution struct {
	ConversationID string
	AutoFocus      bool
}

// A2AEvent is emitted by SDKIntegration for TUI-facing A2A activity.
type A2AEvent struct {
	Kind           A2AEventKind
	ConversationID string
	PeerHandle     string
	Message        *conversation.Message
	Update         agent.IntermediateUpdate
	Err            error
	AutoFocus      bool
}

func defaultTUIA2AHandle(override, rootSessionID string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "swarm"
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if idx := strings.Index(host, "."); idx > 0 {
		host = host[:idx]
	}
	shortID := strings.TrimSpace(rootSessionID)
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID == "" {
		shortID = fmt.Sprintf("%d", time.Now().Unix()%1_000_000)
	}
	return fmt.Sprintf("%s-%s", host, shortID)
}

func withA2AExecutionContext(ctx context.Context, source a2aExecutionSource, conversationID, peerHandle string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, a2aExecutionSourceKey{}, source)
	if strings.TrimSpace(conversationID) != "" {
		ctx = context.WithValue(ctx, a2aConversationIDKey{}, strings.TrimSpace(conversationID))
	}
	if strings.TrimSpace(peerHandle) != "" {
		ctx = context.WithValue(ctx, a2aPeerHandleKey{}, strings.TrimSpace(peerHandle))
	}
	return ctx
}

func executionConversationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if raw, ok := ctx.Value(a2aConversationIDKey{}).(string); ok {
		return strings.TrimSpace(raw)
	}
	return ""
}

func executionPeerHandle(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if raw, ok := ctx.Value(a2aPeerHandleKey{}).(string); ok {
		return strings.TrimSpace(raw)
	}
	return ""
}

func executionSource(ctx context.Context) a2aExecutionSource {
	if ctx == nil {
		return ""
	}
	if raw, ok := ctx.Value(a2aExecutionSourceKey{}).(a2aExecutionSource); ok {
		return raw
	}
	return ""
}

func (sdk *SDKIntegration) installPermanentAgentCallbacks(ag *agent.Agent) {
	if sdk == nil || ag == nil {
		return
	}
	ag.SetMessageCallback(func(ctx context.Context, msg *conversation.Message) error {
		return sdk.handlePermanentAgentMessage(ctx, msg)
	})
	ag.SetCompactionPersistCallback(sdk.persistInLoopCompaction)
	ag.SetIntermediateCallback(func(ctx context.Context, update agent.IntermediateUpdate) error {
		return sdk.handlePermanentIntermediateUpdate(ctx, update)
	})
}

func (sdk *SDKIntegration) installTUIA2ARequestHandler(ag *agent.Agent) {
	if sdk == nil || ag == nil || ag.A2ARuntime() == nil {
		return
	}
	ag.A2ARuntime().SetRequestHandler(func(ctx context.Context, req *a2a.InboundRequest) (*conversation.Message, error) {
		if req == nil || req.ProjectedMessage == nil {
			return nil, fmt.Errorf("a2a inbound request is missing projected message")
		}

		// ─── L1 LOOP GUARD + BROADCAST PROJECTION ──────────────────────────
		// If the inbound message is itself a reply (carries referenceTaskIds)
		// or is a broadcast (swarm_type=="broadcast"), we project it into
		// the appropriate conversation but DO NOT trigger an agent run.
		//
		// Why:
		//   - Replies must not auto-execute or we get an infinite chatter loop
		//     between two agents (A's reply triggers B's agent which triggers
		//     A's reply...). The recipient still gets to see the message on
		//     its next natural turn — see plan.md for the L1+L4 reasoning.
		//   - Broadcasts are informational. Auto-executing on every broadcast
		//     amplifies cost (N peers × M broadcasts/min = huge agent volume).
		//     The agent sees broadcasts on its next user-driven turn.
		//
		// We resolve the conversation, persist the message, and return a
		// no-op acknowledgment. handlePermanentAgentMessage is the persist
		// path; it also emits the A2AEventPeerMessage that the TUI uses to
		// display the message in the chat list.
		var swarmType string
		var referenceTaskIDs []string
		if req.Request != nil && req.Request.Message != nil {
			referenceTaskIDs = req.Request.Message.GetReferenceTaskIds()
			if md := req.Request.Message.Metadata; md != nil {
				if v, ok := md.AsMap()[a2a.SwarmTypeKey]; ok {
					if s, ok := v.(string); ok {
						swarmType = strings.TrimSpace(s)
					}
				}
			}
		}
		isReply := len(referenceTaskIDs) > 0
		isBroadcast := swarmType == a2a.SwarmTypeBroadcast
		if isReply || isBroadcast {
			peerHandle := strings.TrimSpace(req.Peer.Handle)
			conversationID := sdk.projectOnlyConversationID(ctx, req, referenceTaskIDs, peerHandle)
			if sdk.logger != nil {
				sdk.logger.Info(ctx, "tui.a2a.request_handler.project_only",
					observability.F("reason", projectOnlyReason(isReply, isBroadcast)),
					observability.F("conversation_id", conversationID),
					observability.F("peer_handle", peerHandle),
					observability.F("reference_task_ids", strings.Join(referenceTaskIDs, ",")),
				)
			}
			if conversationID != "" {
				peerMessage := req.ProjectedMessage.Clone()
				if peerMessage.Metadata == nil {
					peerMessage.Metadata = make(map[string]any)
				}
				peerMessage.Metadata["conversation_id"] = conversationID
				if peerMessage.A2A == nil {
					peerMessage.A2A = &conversation.A2AMetadata{}
				}
				peerMessage.A2A.RemoteAgentHandle = peerHandle
				if req.Request != nil && req.Request.Message != nil && req.Request.Message.Metadata != nil {
					metadataMap := req.Request.Message.Metadata.AsMap()
					if status, ok := metadataMap["from_status"].(string); ok {
						peerMessage.A2A.RemoteAgentStatus = status
					}
					if task, ok := metadataMap["from_task"].(string); ok {
						peerMessage.A2A.RemoteAgentTask = task
					}
				}
				if err := sdk.handlePermanentAgentMessage(ctx, peerMessage); err != nil {
					return nil, err
				}
				sdk.emitA2AEvent(A2AEvent{
					Kind:           A2AEventPeerMessage,
					ConversationID: conversationID,
					PeerHandle:     peerHandle,
					Message:        peerMessage.Clone(),
				})
			}
			// Return a tiny ack so the sender's RPC envelope completes cleanly.
			// We deliberately do NOT enqueue the message or call
			// ExecuteWhenIdle — that's the whole point of the guard.
			return &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "projected",
				Metadata: map[string]any{
					"conversation_id":     conversationID,
					"peer_handle":         peerHandle,
					"project_only":        true,
					"project_only_reason": projectOnlyReason(isReply, isBroadcast),
				},
			}, nil
		}
		// ───────────────────────────────────────────────────────────────────

		conversationID := ""
		activeConversationID := sdk.currentActiveConversationID()
		if sdk.logger != nil {
			sdk.logger.Info(ctx, "tui.a2a.request_handler.start",
				observability.F("requested_conversation_id", strings.TrimSpace(req.ConversationID)),
				observability.F("active_conversation_id", activeConversationID),
				observability.F("peer_handle", strings.TrimSpace(req.Peer.Handle)),
			)
		}
		if activeConversationID != "" && sdk.conversationExists(ctx, activeConversationID) {
			if strings.TrimSpace(req.ConversationID) == "" || strings.TrimSpace(req.ConversationID) == activeConversationID {
				conversationID = activeConversationID
			}
		}
		peerHandle := strings.TrimSpace(req.Peer.Handle)
		autoFocus := false
		if conversationID == "" {
			resolution, err := sdk.resolveA2AConversation(ctx, conversationID, peerHandle)
			if err != nil {
				return nil, err
			}
			if resolvedConversationID := strings.TrimSpace(resolution.ConversationID); resolvedConversationID != "" {
				conversationID = resolvedConversationID
				autoFocus = resolution.AutoFocus
			}
		}
		if sdk.logger != nil {
			sdk.logger.Info(ctx, "tui.a2a.request_handler.resolved",
				observability.F("conversation_id", conversationID),
				observability.F("auto_focus", autoFocus),
				observability.F("peer_handle", strings.TrimSpace(req.Peer.Handle)),
			)
		}

		// BUG-FIX: propagate the inbound request context so ExecuteWhenIdle is
		// cancelled if the remote peer disconnects or the HTTP server times out.
		// Previously context.Background() caused infinite blocking on disconnect.
		execCtx, execCancel := context.WithTimeout(ctx, 5*time.Minute)
		defer execCancel()
		execCtx = withA2AExecutionContext(execCtx, a2aExecutionSourceInbound, conversationID, peerHandle)
		// Tag the execution context with the unified EventSource so every
		// IntermediateUpdate fired during this inbound task is labelled
		// SourcePeer on the client.Event bus.  Consumers (conductor, analytics,
		// hooks) receive the update through client.Subscribe without any extra
		// wiring.
		if sdk.sdkClient != nil {
			execCtx = sdkclient.WithSource(execCtx, sdkclient.EventSource{
				Kind:       sdkclient.SourcePeer,
				PeerHandle: peerHandle,
				ConvID:     conversationID,
			})
		}
		peerMessage := req.ProjectedMessage.Clone()
		if peerMessage.Metadata == nil {
			peerMessage.Metadata = make(map[string]any)
		}
		peerMessage.Metadata["conversation_id"] = conversationID
		// Note: A2APersistedMetadataKey is set AFTER handlePermanentAgentMessage below,
		// not here. Setting it here would cause handlePermanentAgentMessage to skip
		// persisting entirely (bug). It will be set post-persist, before EnqueueMessage.

		// Extract sender info from incoming message metadata and populate A2A fields
		// This allows the TUI to display who sent the message along with their status
		if peerMessage.A2A == nil {
			peerMessage.A2A = &conversation.A2AMetadata{}
		}
		peerMessage.A2A.RemoteAgentHandle = peerHandle
		// Extract from_status, from_task from the incoming protobuf metadata
		if req.Request != nil && req.Request.Message != nil && req.Request.Message.Metadata != nil {
			metadataMap := req.Request.Message.Metadata.AsMap()
			if status, ok := metadataMap["from_status"].(string); ok {
				peerMessage.A2A.RemoteAgentStatus = status
			}
			if task, ok := metadataMap["from_task"].(string); ok {
				peerMessage.A2A.RemoteAgentTask = task
			}
		}
		if err := sdk.handlePermanentAgentMessage(execCtx, peerMessage); err != nil {
			return nil, err
		}

		sdk.emitA2AEvent(A2AEvent{
			Kind:           A2AEventInboundStarted,
			ConversationID: conversationID,
			PeerHandle:     peerHandle,
			AutoFocus:      autoFocus,
		})

		// Mark as already persisted BEFORE enqueuing, so if the agent's permanent
		// message callback fires for this peer message it will skip re-persisting.
		if peerMessage.Metadata == nil {
			peerMessage.Metadata = make(map[string]any)
		}
		peerMessage.Metadata[conversation.A2APersistedMetadataKey] = true
		executionAgent := sdk.activeAgentWithFreshCompactionConfig()
		if executionAgent == nil {
			return nil, fmt.Errorf("agent execution is unavailable")
		}
		// Enqueue the peer message to the A2A runtime's pending queue
		// The agent will pick it up via TakePendingMessages in its normal message loop
		executionAgent.A2ARuntime().EnqueueMessage(peerMessage)

		// Trigger agent execution and wait for the response
		// We need to return the actual response to the requesting peer,
		// not just nil, nil which causes the peer to hang
		execResult, execErr := executionAgent.ExecuteWhenIdle(execCtx, agent.ExecuteRequest{
			ConversationID: conversationID,
		})
		if execErr != nil {
			return nil, fmt.Errorf("agent execution failed: %w", execErr)
		}

		// Emit completion event
		sdk.emitA2AEvent(A2AEvent{
			Kind:           A2AEventInboundDone,
			ConversationID: conversationID,
			PeerHandle:     peerHandle,
			AutoFocus:      autoFocus,
		})

		// Return the agent's response as a conversation message
		if execResult != nil && execResult.Message != "" {
			return &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: execResult.Message,
				Metadata: map[string]any{
					"conversation_id": conversationID,
					"peer_handle":     peerHandle,
					"finish_reason":   execResult.FinishReason,
					"tokens_used":     execResult.TokensUsed,
				},
			}, nil
		}

		// Fallback: return an empty acknowledgment message
		return &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: "Message received and processed",
			Metadata: map[string]any{
				"conversation_id": conversationID,
				"peer_handle":     peerHandle,
			},
		}, nil
	})

	// Wire the InboundMessageCallback so the conductor's event bus is notified
	// every time a peer sends a task to this TUI instance.  This is
	// informational only — the request handler above still handles execution.
	if sdk.sdkClient != nil {
		sdk.activeAgent().A2ARuntime().SetInboundMessageCallback(func(peer a2a.PeerIdentity, conversationID string) {
			sdk.sdkClient.InjectEvent(sdkclient.Event{
				Kind: sdkclient.EventPeerStatus,
				Source: sdkclient.EventSource{
					Kind:       sdkclient.SourcePeer,
					PeerHandle: peer.Handle,
					ConvID:     conversationID,
				},
				Payload: sdkclient.PeerStatusPayload{
					Handle:      peer.Handle,
					Status:      "working",
					CurrentTask: "inbound a2a task",
				},
			})
		})
	}
}

func (sdk *SDKIntegration) handlePermanentAgentMessage(ctx context.Context, msg *conversation.Message) error {
	if sdk == nil || sdk.sdkClient == nil || msg == nil {
		return nil
	}

	conversationID := ""
	if msg.Metadata != nil {
		if raw, ok := msg.Metadata["conversation_id"].(string); ok {
			conversationID = strings.TrimSpace(raw)
		}
	}
	if conversationID == "" {
		conversationID = executionConversationID(ctx)
	}
	if conversationID == "" {
		conversationID = sdk.currentActiveConversationID()
	}

	autoFocus := false
	peerHandle := executionPeerHandle(ctx)
	if msg.A2A != nil && strings.TrimSpace(msg.A2A.RemoteAgentHandle) != "" {
		peerHandle = strings.TrimSpace(msg.A2A.RemoteAgentHandle)
	}
	if msg.Role == conversation.RolePeer && (conversationID == "" || strings.HasPrefix(conversationID, "a2a:")) {
		if sdk.logger != nil {
			sdk.logger.Info(ctx, "tui.a2a.peer_message.resolve_conversation",
				observability.F("requested_conversation_id", conversationID),
				observability.F("peer_handle", peerHandle),
			)
		}
		resolution, err := sdk.resolveA2AConversation(ctx, conversationID, peerHandle)
		if err != nil {
			return err
		}
		if strings.TrimSpace(resolution.ConversationID) != "" {
			conversationID = strings.TrimSpace(resolution.ConversationID)
			autoFocus = resolution.AutoFocus
		}
		if sdk.logger != nil {
			sdk.logger.Info(ctx, "tui.a2a.peer_message.resolved_conversation",
				observability.F("conversation_id", conversationID),
				observability.F("auto_focus", autoFocus),
				observability.F("peer_handle", peerHandle),
			)
		}
	}
	if conversationID == "" {
		return nil
	}

	if msg.Metadata == nil {
		msg.Metadata = make(map[string]any)
	}
	msg.Metadata["conversation_id"] = conversationID
	// BUG-FIX: honor the A2APersistedMetadataKey flag. If the inbound request handler
	// already persisted this message, skip AddMessage to prevent duplicate entries.
	if alreadyPersisted, _ := msg.Metadata[conversation.A2APersistedMetadataKey].(bool); !alreadyPersisted {
		if err := sdk.AddMessage(ctx, conversationID, msg); err != nil {
			return err
		}
	}

	if msg.Role == conversation.RolePeer {
		sdk.emitA2AEvent(A2AEvent{
			Kind:           A2AEventPeerMessage,
			ConversationID: conversationID,
			PeerHandle:     peerHandle,
			Message:        msg.Clone(),
			AutoFocus:      autoFocus,
		})
	}
	return nil
}

func (sdk *SDKIntegration) handlePermanentIntermediateUpdate(ctx context.Context, update agent.IntermediateUpdate) error {
	if sdk == nil || update == nil {
		return nil
	}
	logDebug("[A2A] handlePermanentIntermediateUpdate start source=%s type=%s conv=%s peer=%s",
		executionSource(ctx), update.UpdateType(), executionConversationID(ctx), executionPeerHandle(ctx))
	switch executionSource(ctx) {
	case a2aExecutionSourceInbound:
		logDebug("[A2A] emitting inbound update type=%s conv=%s", update.UpdateType(), executionConversationID(ctx))
		sdk.emitA2AEvent(A2AEvent{
			Kind:           A2AEventInboundUpdate,
			ConversationID: executionConversationID(ctx),
			PeerHandle:     executionPeerHandle(ctx),
			Update:         update,
		})
		logDebug("[A2A] emitted inbound update type=%s conv=%s", update.UpdateType(), executionConversationID(ctx))
		return nil
	default:
		sdk.a2aMu.RLock()
		updateChan := sdk.localUpdateChan
		sdk.a2aMu.RUnlock()
		if updateChan == nil {
			logDebug("[A2A] updateChan is nil, dropping update type=%s", update.UpdateType())
			return nil
		}
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logDebug("[SDK] Permanent intermediate callback recovered from panic: %v\n%s", r, stack)
				CapturePanicEvent(r, stack, "a2a_intermediate_callback", nil)
			}
		}()
		// Bounded send. The intermediate callback runs on the shared tool
		// worker pool (a sub-agent's heartbeat/terminal update is emitted from
		// the pool worker executing the Subagent tool). A bare blocking send
		// here can freeze that worker indefinitely if the Bubble Tea reader
		// stalls or the buffered channel fills — which in turn wedges every
		// sibling tool waiting on the same parallel batch's wg.Wait(). So we
		// never block unboundedly:
		//   1. fast path: non-blocking send (succeeds while buffer has room),
		//   2. fallback: bounded wait so a briefly-busy UI still gets the update,
		//   3. last resort: drop with a log rather than hang the worker forever.
		select {
		case updateChan <- update:
			logDebug("[A2A] forwarded local update type=%s", update.UpdateType())
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		timer := time.NewTimer(localUpdateSendTimeout)
		defer timer.Stop()
		select {
		case updateChan <- update:
			logDebug("[A2A] forwarded local update type=%s (after wait)", update.UpdateType())
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			logDebug("[A2A] dropping local update type=%s: UI channel full for %s", update.UpdateType(), localUpdateSendTimeout)
			return nil
		}
	}
}

// localUpdateSendTimeout bounds how long the intermediate callback will wait to
// hand an update to the TUI render loop before dropping it. Dropping a
// streaming/heartbeat update is harmless (the next one supersedes it) and is
// vastly preferable to freezing the worker-pool goroutine that owes sibling
// tools their turn.
const localUpdateSendTimeout = 2 * time.Second

func (sdk *SDKIntegration) SetActiveConversation(convID string) {
	if sdk == nil {
		return
	}
	convID = strings.TrimSpace(convID)

	// Lock to update active conversation ID
	sdk.a2aMu.Lock()
	sdk.activeConversationID = convID
	sdk.a2aMu.Unlock()

	// Keep the unified client's snapshot in lockstep with the TUI's legacy
	// mirror. The TUI already validated this ID, so this synchronous state-only
	// path avoids storage I/O and cannot complete out of order after a later
	// selection.
	if sdk.sdkClient != nil {
		if convID == "" {
			sdk.sdkClient.ClearActiveConversation()
		} else {
			sdk.sdkClient.SyncActiveConversation(convID)
		}
	}

	// BUG-FIX: run the wait+reset in a goroutine so the caller (possibly the UI
	// goroutine) returns immediately. Also fixes the original select-break bug:
	// 'break' inside a select case only exits the select, not the outer for-loop,
	// making the 3-second timeout a dead letter (loop kept spinning forever).
	if sdk.activeAgent() != nil {
		safego.Go("chat.a2a.conversationSwitchWait", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			becameIdle := false
		waitLoop:
			for {
				stats := sdk.activeAgent().Stats()
				if stats.State == agent.StateIdle {
					becameIdle = true
					break waitLoop
				}
				select {
				case <-ctx.Done():
					if sdk.logger != nil {
						sdk.logger.Warn(ctx, "sdk.conversation_switch_timeout",
							observability.F("conversation_id", convID),
							observability.F("agent_state", string(stats.State)),
						)
					}
					break waitLoop // labeled: exits the for-loop, not just the select
				default:
					time.Sleep(100 * time.Millisecond)
				}
			}
			// Only reset conversation-scoped agent state when the agent actually
			// went idle. Resetting after a timeout used to zero turnCount /
			// inputTokens / conversationID while executeLoop was still running,
			// corrupting token tracking and rerouting in-flight message
			// persistence. Skipping is safe: Execute() re-stamps conversationID
			// and re-seeds token counters from the request history on its next run.
			if becameIdle {
				sdk.activeAgent().ResetConversationState(convID)
			} else if sdk.logger != nil {
				sdk.logger.Warn(ctx, "sdk.conversation_switch_reset_skipped_agent_busy",
					observability.F("conversation_id", convID),
				)
			}
			if sdk.activeAgent().A2ARuntime() != nil {
				_ = sdk.activeAgent().A2ARuntime().UpdateConversationID(context.Background(), convID)
			}
		})
	}
}

func (sdk *SDKIntegration) ClearActiveConversation() {
	sdk.SetActiveConversation("")
}

func (sdk *SDKIntegration) currentActiveConversationID() string {
	if sdk == nil {
		return ""
	}
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()
	return strings.TrimSpace(sdk.activeConversationID)
}

func (sdk *SDKIntegration) setLocalUpdateChannel(updateChan chan<- agent.IntermediateUpdate) {
	sdk.a2aMu.Lock()
	defer sdk.a2aMu.Unlock()
	sdk.localUpdateChan = updateChan
}

func (sdk *SDKIntegration) clearLocalUpdateChannel(updateChan chan<- agent.IntermediateUpdate) {
	sdk.a2aMu.Lock()
	defer sdk.a2aMu.Unlock()
	if sdk.localUpdateChan == updateChan {
		sdk.localUpdateChan = nil
	}
}

func (sdk *SDKIntegration) SetA2AEventCallback(callback func(A2AEvent)) {
	sdk.a2aMu.Lock()
	sdk.a2aEventCallback = callback
	if sdk.a2aEventQueue == nil {
		sdk.a2aEventQueue = make(chan A2AEvent, 4096)
	}
	sdk.a2aMu.Unlock()
	// BUG-FIX: start the drain goroutine immediately so it is ready
	// before the first emitA2AEvent call, not lazily on first emit.
	sdk.ensureA2AEventDispatcher()
}

func (sdk *SDKIntegration) emitA2AEvent(event A2AEvent) {
	if sdk == nil {
		return
	}
	sdk.ensureA2AEventDispatcher()
	// BUG-FIX: read a2aEventQueue under lock to prevent data race with
	// SetA2AEventCallback which writes the field under a2aMu.Lock().
	sdk.a2aMu.RLock()
	queue := sdk.a2aEventQueue
	sdk.a2aMu.RUnlock()
	if queue == nil {
		sdk.dispatchA2AEvent(event)
		return
	}
	select {
	case queue <- event:
	default:
		if sdk.logger != nil {
			sdk.logger.Warn(context.Background(), "a2a.event_queue_full_dispatching_async",
				observability.F("kind", string(event.Kind)),
				observability.F("conversation_id", event.ConversationID),
				observability.F("peer_handle", event.PeerHandle),
			)
		}
		go sdk.dispatchA2AEvent(event)
	}
}

func (sdk *SDKIntegration) ensureA2AEventDispatcher() {
	if sdk == nil {
		return
	}
	sdk.a2aEventDispatcher.Do(func() {
		sdk.a2aMu.Lock()
		if sdk.a2aEventQueue == nil {
			sdk.a2aEventQueue = make(chan A2AEvent, 4096)
		}
		queue := sdk.a2aEventQueue
		sdk.a2aMu.Unlock()

		safego.Go("chat.a2a.eventDispatch", func() {
			for event := range queue {
				sdk.dispatchA2AEvent(event)
			}
		})
	})
}

func (sdk *SDKIntegration) dispatchA2AEvent(event A2AEvent) {
	sdk.a2aMu.RLock()
	callback := sdk.a2aEventCallback
	sdk.a2aMu.RUnlock()
	if callback == nil {
		return
	}
	if sdk.logger != nil {
		sdk.logger.Debug(context.Background(), "a2a.dispatch_event.start",
			observability.F("kind", string(event.Kind)),
			observability.F("conversation_id", event.ConversationID),
			observability.F("peer_handle", event.PeerHandle),
		)
	}
	callback(event)
	if sdk.logger != nil {
		sdk.logger.Debug(context.Background(), "a2a.dispatch_event.done",
			observability.F("kind", string(event.Kind)),
			observability.F("conversation_id", event.ConversationID),
			observability.F("peer_handle", event.PeerHandle),
		)
	}
}

func (sdk *SDKIntegration) SetA2AConversationAllocator(fn func(context.Context, string, string) (A2AConversationResolution, error)) {
	sdk.a2aMu.Lock()
	defer sdk.a2aMu.Unlock()
	sdk.a2aConversationFn = fn
}

func (sdk *SDKIntegration) resolveA2AConversation(ctx context.Context, requestedConversationID, peerHandle string) (A2AConversationResolution, error) {
	sdk.a2aMu.RLock()
	fn := sdk.a2aConversationFn
	sdk.a2aMu.RUnlock()
	if fn == nil {
		return A2AConversationResolution{}, nil
	}
	return fn(ctx, requestedConversationID, peerHandle)
}

func (sdk *SDKIntegration) A2AEnabled() bool {
	return sdk != nil && sdk.a2aEnabled && sdk.activeAgent() != nil && sdk.activeAgent().A2ARuntime() != nil
}

func (sdk *SDKIntegration) ListA2APeers() []a2a.PeerIdentity {
	peers, err := sdk.ListA2APeersContext(context.Background())
	if err != nil {
		return nil
	}
	return peers
}

func (sdk *SDKIntegration) ListA2APeersContext(ctx context.Context) ([]a2a.PeerIdentity, error) {
	if !sdk.A2AEnabled() {
		return nil, nil
	}
	peers, err := sdk.activeAgent().A2ARuntime().ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]a2a.PeerIdentity, 0, len(peers))
	self := sdk.activeAgent().A2ARuntime().Peer()
	for _, peer := range peers {
		if peer.SessionID == self.SessionID {
			continue
		}
		filtered = append(filtered, peer)
	}
	return a2a.CanonicalPeers(filtered), nil
}

func parsePreferredA2APeerHandle(text string) string {
	for part := range strings.FieldsSeq(text) {
		if !strings.HasPrefix(part, "@agent:peer:") {
			continue
		}
		handle := strings.TrimPrefix(part, "@agent:peer:")
		handle = strings.Trim(handle, " \t\r\n.,:;!?)]}\"'")
		if handle != "" {
			return handle
		}
	}
	return ""
}

func (sdk *SDKIntegration) buildA2ARuntimeGuidance(ctx context.Context, userMessage string) string {
	if !sdk.A2AEnabled() {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()

	peers, err := sdk.ListA2APeersContext(ctx)
	if err != nil || len(peers) == 0 {
		return ""
	}

	preferred := parsePreferredA2APeerHandle(userMessage)
	lines := []string{
		"A2A peers currently available in this workspace:",
	}
	for _, peer := range peers {
		lines = append(lines, "- "+peer.Handle+" ("+peer.EndpointURL+")")
	}
	lines = append(lines, "")
	lines = append(lines, "Important: a mention in the form @agent:peer:<handle> refers to a remote top-level A2A peer session.")
	lines = append(lines, "Do not interpret @agent:peer:<handle> as a local custom agent and do not use the Subagent tool for it.")
	if preferred != "" {
		preferredPeer := findA2APeerByHandle(peers, preferred)
		lines = append(lines, "")
		if preferredPeer != nil {
			lines = append(lines, "Preferred peer from the user's message: "+preferredPeer.Handle+" ("+preferredPeer.EndpointURL+")")
			lines = append(lines, "Because the user explicitly targeted @agent:peer:"+preferredPeer.Handle+", contact that peer with a2a_send_message or a2a_send_streaming_message.")
			lines = append(lines, "Do not use Subagent for this request.")
		} else {
			lines = append(lines, "Preferred peer from the user's message: "+preferred)
			lines = append(lines, "If that peer appears in the registry, contact it with a2a_send_message rather than Subagent.")
		}
	}
	lines = append(lines, "")
	lines = append(lines, "Use the a2a_* tools when collaborating with another TUI session would materially help.")
	return strings.Join(lines, "\n")
}

func findA2APeerByHandle(peers []a2a.PeerIdentity, handle string) *a2a.PeerIdentity {
	handle = a2a.NormalizeHandle(handle)
	if handle == "" {
		return nil
	}
	for i := range peers {
		if a2a.NormalizeHandle(peers[i].Handle) == handle {
			return &peers[i]
		}
	}
	return nil
}

// projectOnlyConversationID resolves the conversation an inbound reply or
// broadcast should land in. For replies, we consult the A2A runtime's
// RemoteTaskBinding store first — when peer-A sent the original DM, it
// recorded a binding (RemoteTaskID → ConversationID). That mapping is the
// authoritative answer for "where does this reply belong?" If no binding
// exists (orphaned reply, or the original sender was a different process),
// we fall back to the same peer-handle-based resolution the normal request
// path uses. Returns "" if no conversation can be determined; the caller
// drops the message in that case.
func (sdk *SDKIntegration) projectOnlyConversationID(ctx context.Context, req *a2a.InboundRequest, referenceTaskIDs []string, peerHandle string) string {
	if sdk == nil || sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return ""
	}
	// 1. Reply correlation via RemoteTaskBinding.
	for _, taskID := range referenceTaskIDs {
		if strings.TrimSpace(taskID) == "" {
			continue
		}
		binding, err := sdk.activeAgent().A2ARuntime().LookupBindingByRemoteTaskID(ctx, taskID)
		if err != nil {
			if sdk.logger != nil {
				sdk.logger.Warn(ctx, "tui.a2a.binding_lookup_failed",
					observability.F("task_id", taskID),
					observability.F("error", err.Error()))
			}
			continue
		}
		if binding != nil && strings.TrimSpace(binding.ConversationID) != "" {
			if sdk.conversationExists(ctx, binding.ConversationID) {
				return strings.TrimSpace(binding.ConversationID)
			}
		}
	}
	// 2. Fall back to the requested conversation if it exists.
	requested := strings.TrimSpace(req.ConversationID)
	if requested != "" && sdk.conversationExists(ctx, requested) {
		return requested
	}
	// 3. Fall back to the current active conversation.
	active := sdk.currentActiveConversationID()
	if active != "" && sdk.conversationExists(ctx, active) {
		return active
	}
	// 4. Last resort: ask the host for a peer-handle-scoped conversation.
	resolution, err := sdk.resolveA2AConversation(ctx, "", peerHandle)
	if err != nil {
		if sdk.logger != nil {
			sdk.logger.Warn(ctx, "tui.a2a.fallback_resolve_failed",
				observability.F("peer_handle", peerHandle),
				observability.F("error", err.Error()))
		}
		return ""
	}
	return strings.TrimSpace(resolution.ConversationID)
}

func projectOnlyReason(isReply, isBroadcast bool) string {
	switch {
	case isReply && isBroadcast:
		return "reply+broadcast"
	case isReply:
		return "reply"
	case isBroadcast:
		return "broadcast"
	default:
		return ""
	}
}

// InstallDaemonA2ARequestHandler installs a simplified inbound request handler
// suitable for headless daemon workers. Unlike installTUIA2ARequestHandler it
// has no TUI conversation management or UI events. The optional onStart/onDone
// callbacks are invoked around each agent execution so the caller can update
// swarm presence status (working ↔ idle) without coupling this package to the
// filesystem discovery layer.
func (sdk *SDKIntegration) InstallDaemonA2ARequestHandler(
	onStart func(peerHandle, taskText string),
	onDone func(peerHandle string, err error),
) {
	if sdk == nil || sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return
	}
	sdk.activeAgent().A2ARuntime().SetRequestHandler(func(ctx context.Context, req *a2a.InboundRequest) (*conversation.Message, error) {
		if req == nil || req.ProjectedMessage == nil {
			return nil, fmt.Errorf("a2a: missing projected message")
		}

		// L1 loop guard: replies and broadcasts get a lightweight ack only.
		var swarmType string
		var referenceTaskIDs []string
		if req.Request != nil && req.Request.Message != nil {
			referenceTaskIDs = req.Request.Message.GetReferenceTaskIds()
			if md := req.Request.Message.Metadata; md != nil {
				if v, ok := md.AsMap()[a2a.SwarmTypeKey]; ok {
					if s, ok := v.(string); ok {
						swarmType = strings.TrimSpace(s)
					}
				}
			}
		}
		isReply := len(referenceTaskIDs) > 0
		isBroadcast := swarmType == a2a.SwarmTypeBroadcast
		if isReply || isBroadcast {
			return &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "ack",
				Metadata: map[string]any{
					"project_only":        true,
					"project_only_reason": projectOnlyReason(isReply, isBroadcast),
					"peer_handle":         req.Peer.Handle,
				},
			}, nil
		}

		peerHandle := strings.TrimSpace(req.Peer.Handle)
		taskText := strings.TrimSpace(req.ProjectedMessage.Content)

		if onStart != nil {
			onStart(peerHandle, taskText)
		}
		var execErr error
		defer func() {
			if onDone != nil {
				onDone(peerHandle, execErr)
			}
		}()

		execCtx, execCancel := context.WithTimeout(ctx, 10*time.Minute)
		defer execCancel()

		peerMessage := req.ProjectedMessage.Clone()
		if peerMessage.A2A == nil {
			peerMessage.A2A = &conversation.A2AMetadata{}
		}
		peerMessage.A2A.RemoteAgentHandle = peerHandle
		if req.Request != nil && req.Request.Message != nil && req.Request.Message.Metadata != nil {
			metadataMap := req.Request.Message.Metadata.AsMap()
			if status, ok := metadataMap["from_status"].(string); ok {
				peerMessage.A2A.RemoteAgentStatus = status
			}
			if task, ok := metadataMap["from_task"].(string); ok {
				peerMessage.A2A.RemoteAgentTask = task
			}
		}
		if peerMessage.Metadata == nil {
			peerMessage.Metadata = make(map[string]any)
		}
		peerMessage.Metadata[conversation.A2APersistedMetadataKey] = true

		sdk.activeAgent().A2ARuntime().EnqueueMessage(peerMessage)

		var execResult *agent.ExecuteResponse
		execResult, execErr = sdk.activeAgent().ExecuteWhenIdle(execCtx, agent.ExecuteRequest{})
		if execErr != nil {
			return nil, fmt.Errorf("daemon: agent execution: %w", execErr)
		}
		if execResult != nil && execResult.Message != "" {
			return &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: execResult.Message,
				Metadata: map[string]any{
					"peer_handle":   peerHandle,
					"finish_reason": execResult.FinishReason,
					"tokens_used":   execResult.TokensUsed,
				},
			}, nil
		}
		return &conversation.Message{
			Role:     conversation.RoleAssistant,
			Content:  "task completed",
			Metadata: map[string]any{"peer_handle": peerHandle},
		}, nil
	})
}
