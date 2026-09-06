// Package client — messaging.go
//
// Session-level messaging surface lifted from session.ClientSession.
// These methods drive an agent turn, manage the active conversation,
// and emit themed events.  Together with state.go and events.go they
// let *client.Client satisfy session.Session.
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
)

// SendMessage runs an agent turn against convID with the given message.
// Pass an empty convID to start a new conversation.  The reply is
// streamed via Subscribe; this call blocks until the turn completes.
func (c *Client) SendMessage(ctx context.Context, convID, message string, opts SendMessageOptions) error {
	if c.sessStopped.Load() {
		return errors.New("client: session stopped")
	}
	callerSupplied := convID != ""
	if !callerSupplied {
		c.stateMu.RLock()
		convID = c.sessState.ActiveConvID
		c.stateMu.RUnlock()
		// No active conversation yet (fresh daemon / serve / thin-UI first
		// message): mint a workspace-scoped one BEFORE the turn so ChatCtx
		// persists into it, instead of running the turn statelessly and then
		// adopting the globally most-recent conversation from another
		// workspace. Mirrors the in-process TUI's first-message behaviour.
		if convID == "" {
			var cerr error
			if convID, cerr = c.ensureActiveConversation(ctx); cerr != nil {
				c.recordError(cerr, "SendMessage: ensure conversation")
				return cerr
			}
		}
	}

	// Make convID the active conversation BEFORE running the turn. The agent's
	// message callback (persistTurnMessage) saves the assistant/tool reply to
	// c.sessState.ActiveConvID — if we only set it AFTER the turn (as before),
	// a caller-supplied convID meant the reply was persisted to the stale/empty
	// previous conversation, so freshly-started conversations lost every agent
	// message. Setting it up front routes persistence to the right conversation.
	c.stateMu.Lock()
	c.sessState.ActiveConvID = convID
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()

	// Set up cancel hook for Cancel().
	turnCtx, cancel := context.WithCancel(ctx)
	c.sessExecMu.Lock()
	c.sessExecCancel = cancel
	c.sessExecMu.Unlock()
	defer func() {
		cancel()
		c.sessExecMu.Lock()
		c.sessExecCancel = nil
		c.sessExecMu.Unlock()
	}()

	c.dispatchEvent(Event{Kind: EventStreamStart, At: time.Now()})
	c.setStreaming(true)
	defer func() {
		c.setStreaming(false)
		c.dispatchEvent(Event{Kind: EventStreamEnd, At: time.Now()})
	}()

	// Drive the chat through the lower-level entry point; intermediate
	// updates flow through SubscribeUpdates → themed dispatch wired by
	// Start(). opts carries per-turn controls (system-prompt override, turn
	// cap, tool/hook kill switches) that are applied to this execution only.
	if _, err := c.chatCtxWithOpts(turnCtx, convID, message, opts); err != nil {
		c.recordError(err, "SendMessage failed")
		return err
	}

	// convID is now authoritative — caller-supplied, already active, or freshly
	// minted above — so the active pointer is just set to it. (No back-fill from
	// the global store: that crossed workspace boundaries.)
	c.stateMu.Lock()
	c.sessState.ActiveConvID = convID
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	return nil
}

// ensureActiveConversation returns the active conversation id, creating a new
// workspace-scoped conversation (and marking it active) when none exists yet.
//
// This is the headless equivalent of the TUI's "create a conversation on the
// first message" step (app_messaging.go). Keeping it on the client means the
// daemon, the serve thin clients, and any embedded UI all get a correctly
// workspace-scoped conversation instead of running the first turn statelessly
// and inheriting an unrelated conversation from the global store.
func (c *Client) ensureActiveConversation(ctx context.Context) (string, error) {
	c.stateMu.RLock()
	active := c.sessState.ActiveConvID
	c.stateMu.RUnlock()
	if active != "" {
		return active, nil
	}
	conv, err := c.NewConversation(ctx)
	if err != nil {
		return "", err
	}
	c.stateMu.Lock()
	c.sessState.ActiveConvID = conv.ID
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	return conv.ID, nil
}

// SwitchConversation makes id the active conversation.  Loads it from
// storage to validate existence and updates State.ActiveConvID.
func (c *Client) SwitchConversation(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("SwitchConversation: id is required")
	}
	if _, err := c.LoadConversation(ctx, id); err != nil {
		return fmt.Errorf("SwitchConversation: load %s: %w", id, err)
	}
	c.stateMu.Lock()
	c.sessState.ActiveConvID = id
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	c.dispatchEvent(Event{Kind: EventConvSwitched, Payload: ConvPayload{ConvID: id}, At: time.Now()})
	return nil
}

// CreateConversation creates a new conversation and makes it active.
//
// Returns the new conversation's ID.  The opts.ProjectID/Tags fields are
// captured on the State snapshot but their on-disk persistence awaits
// an SDK extension.
func (c *Client) CreateConversation(ctx context.Context, opts CreateOptions) (string, error) {
	conv, err := c.NewConversation(ctx)
	if err != nil {
		return "", err
	}
	c.stateMu.Lock()
	c.sessState.ActiveConvID = conv.ID
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	c.dispatchEvent(Event{Kind: EventConvCreated, Payload: ConvPayload{ConvID: conv.ID}, At: time.Now()})
	_ = opts
	return conv.ID, nil
}

// ListConversations returns conversation summaries for the active
// workspace (or all workspaces when opts.WorkspacePath is empty).
//
// (Distinct from the lower-level ListAllConversations() and
// ListConversationsMeta() that return *conversation.Conversation values.
// This form returns the IPC-friendly summary shape and updates the
// cached snapshot.)
func (c *Client) ListConversations(ctx context.Context, opts ListOptions) ([]ConversationSummary, error) {
	convs, err := c.listConversationsMeta(ctx, opts.WorkspacePath, opts.IncludeHeadless)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationSummary, 0, len(convs))
	for i, conv := range convs {
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
		if opts.Offset > 0 && i < opts.Offset {
			continue
		}
		out = append(out, summaryFromConv(conv))
	}
	c.stateMu.Lock()
	c.sessState.Conversations = out
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	return out, nil
}

// EditMessage updates a single message's content within convID.
//
// The SDK does not yet expose a first-class EditMessage primitive; this
// implementation loads the conversation, mutates the matching message in
// place, drops every message after it, and saves.
func (c *Client) EditMessage(ctx context.Context, convID, messageID, newText string) error {
	conv, err := c.LoadConversation(ctx, convID)
	if err != nil {
		return err
	}
	idx := -1
	for i, m := range conv.Messages {
		if m == nil {
			continue
		}
		if m.ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("EditMessage: message %s not found in conversation %s", messageID, convID)
	}
	conv.Messages[idx].Content = newText
	if idx+1 < len(conv.Messages) {
		conv.Messages = conv.Messages[:idx+1]
	}
	mgr := c.ConversationManager()
	if mgr == nil {
		return errors.New("EditMessage: client has no conversation manager")
	}
	return mgr.Save(ctx, conv)
}

// summaryFromConv adapts a low-level *conversation.Conversation to the
// IPC-friendly ConversationSummary.
func summaryFromConv(cv *conversation.Conversation) ConversationSummary {
	if cv == nil {
		return ConversationSummary{}
	}
	// Prefer the cached first *user* prompt (already the real intent) and clean
	// machine-noise (system-reminders, task-nudge boilerplate, naming-agent
	// envelopes) BEFORE truncating — otherwise a long leading reminder fills the
	// entire preview window and history search has nothing meaningful to match.
	source := ""
	if substantive := historytools.FirstSubstantiveUserText(cv.Messages); substantive != "" {
		source = substantive
	} else if cv.Summary != nil && cv.Summary.FirstUserPrompt != "" {
		source = cv.Summary.FirstUserPrompt
	} else if len(cv.Messages) > 0 && cv.Messages[0] != nil {
		source = cv.Messages[0].Content
	}
	preview := truncateForPreview(historytools.CleanText(source), 200)
	totalTokens := 0
	for _, m := range cv.Messages {
		if m == nil || m.Tokens == nil {
			continue
		}
		totalTokens += m.Tokens.Input + m.Tokens.Output
	}
	messageCount := len(cv.Messages)
	if cv.Summary != nil && len(cv.Messages) <= 1 {
		messageCount = cv.Summary.MessageCount
	}
	recap := ""
	if cv.Summary != nil {
		recap = cv.Summary.Recap
	}
	return ConversationSummary{
		ID:            cv.ID,
		Title:         cv.Title,
		Preview:       preview,
		Recap:         recap,
		MessageCount:  messageCount,
		TotalTokens:   totalTokens,
		UpdatedAt:     cv.UpdatedAt,
		Status:        string(cv.Status),
		WorkspacePath: cv.WorkspacePath,
		Tags:          append([]string(nil), cv.Metadata.Tags...),
		Origin:        conversationOrigin(cv),
	}
}

func conversationOrigin(cv *conversation.Conversation) string {
	if cv == nil {
		return ""
	}
	if cv.Metadata.Custom != nil {
		if origin, ok := cv.Metadata.Custom["origin"].(string); ok {
			switch origin {
			case "interactive", "subagent", "headless":
				return origin
			}
		}
	}
	for _, tag := range cv.Metadata.Tags {
		switch tag {
		case "subagent":
			return "subagent"
		case conversation.HeadlessTag:
			return "headless"
		}
	}
	return "interactive"
}
