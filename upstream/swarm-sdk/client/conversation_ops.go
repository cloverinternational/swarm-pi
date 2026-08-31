// Package client — conversation_ops.go
//
// Manager-shaped conversation operations exposed on *Client so consumers
// (TUI, IPC servers, etc.) can drive every conversation mutation through
// the SDK client instead of reaching into the conversation Manager
// directly.  All methods delegate to the embedded convManager and use
// the same on-disk store as CreateConversation/LoadConversation.
//
// Naming convention: these methods accept manager-shaped types (e.g.
// manager.CreateOptions, manager.GetMessagesOptions) and return the
// rich *conversation.Conversation / *conversation.Message pointers
// suitable for read/modify/save flows.  They are intentionally
// distinct from the IPC-friendly CreateConversation(ctx, CreateOptions)
// in messaging.go which returns only an ID.
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
)

// errNoConvManager is returned when a conversation operation is invoked
// against a Client constructed without conversation persistence.
var errNoConvManager = errors.New("client: no conversation manager")

// CreateConversationWithOptions creates a new conversation using the
// full manager.CreateOptions shape (Mode, WorkspacePath, Metadata, …)
// and returns the resulting *conversation.Conversation.
//
// Prefer this over the lighter CreateConversation(ctx, CreateOptions)
// when the caller needs to seed metadata (workspace path, tags, custom
// fields) at creation time.
func (c *Client) CreateConversationWithOptions(ctx context.Context, opts manager.CreateOptions) (*conversation.Conversation, error) {
	c.mu.RLock()
	mgr := c.convManager
	agentDef := c.agentDef
	c.mu.RUnlock()
	if mgr == nil {
		return nil, errNoConvManager
	}
	// Fill the owning agent into the join key (PLAN.md gap G1) when the caller
	// did not supply one. The client is the lowest layer that knows which agent
	// definition is active, and this is the entry point the TUI uses for every
	// conversation it creates, so defaulting here is what makes agent_id present
	// without asking each call site to thread it. A caller-supplied AgentID is
	// always respected.
	if opts.AgentID == "" && agentDef != nil {
		opts.AgentID = agentDef.ID
	}
	conv, err := mgr.Create(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("client.CreateConversationWithOptions: %w", err)
	}
	return conv, nil
}

// ResumeConversation loads a conversation by ID via the conversation
// manager (which honours the same caching/locking as the manager's
// other read paths).  Equivalent to LoadConversation for most callers
// but goes through the manager rather than directly through storage.
func (c *Client) ResumeConversation(ctx context.Context, convID string) (*conversation.Conversation, error) {
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return nil, errNoConvManager
	}
	return mgr.Resume(ctx, convID)
}

// SaveConversation persists a conversation that was previously loaded
// (via Resume/LoadConversation) and mutated in place.  Used by the TUI
// title-update, metadata-update, and fork flows.
func (c *Client) SaveConversation(ctx context.Context, conv *conversation.Conversation) error {
	if conv == nil {
		return errors.New("client.SaveConversation: conv is nil")
	}
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return errNoConvManager
	}
	return mgr.Save(ctx, conv)
}

// AddMessageToConversation appends a message to convID via the
// conversation manager.  Named to avoid colliding with any future
// AddMessage primitive on the messaging surface.
func (c *Client) AddMessageToConversation(ctx context.Context, convID string, msg *conversation.Message) error {
	if msg == nil {
		return errors.New("client.AddMessageToConversation: msg is nil")
	}
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return errNoConvManager
	}
	lock := c.conversationMetadataLock(convID)
	lock.Lock()
	if err := mgr.AddMessage(ctx, convID, msg); err != nil {
		lock.Unlock()
		return err
	}
	lock.Unlock()
	// Persist a useful deterministic title as soon as the first substantive
	// prompt lands. The LLM enhancement runs at the completed-turn boundary.
	c.ensureConversationMetadataFallback(ctx, convID)
	return nil
}

// wireMessagePersistence sets the agent's MessageCallback so every message the
// agent generates during a turn is persisted to the active conversation's store
// — the same mechanism the TUI uses (SetMessageCallback -> AddMessage). Without
// this the conversation Manager stays empty and getMessages returns nothing, so
// a reattaching UI has no scrollback. Idempotent; call after Start() and after
// Reconfigure() rebuilds the agent.
func (c *Client) wireMessagePersistence() {
	// Read c.agent directly (no c.mu) — callers either just assigned it while
	// holding c.mu (buildAgent) or hold no lock (startAgentBridge); taking
	// c.mu.RLock() here would deadlock the former. Matches ChatCtx's access.
	ag := c.agent
	if ag == nil {
		return
	}
	ag.SetMessageCallback(func(ctx context.Context, msg *conversation.Message) error {
		return c.persistTurnMessage(ctx, msg)
	})
	ag.SetCompactionPersistCallback(c.persistCompactedGeneration)
}

// persistTurnMessage appends a generated message to the active conversation so
// it shows up in getMessages / on reattach.
func (c *Client) persistTurnMessage(ctx context.Context, msg *conversation.Message) error {
	if msg == nil {
		return nil
	}
	c.stateMu.RLock()
	convID := c.sessState.ActiveConvID
	c.stateMu.RUnlock()
	if convID == "" {
		return nil
	}
	return c.AddMessageToConversation(ctx, convID, msg)
}

func (c *Client) persistCompactedGeneration(
	ctx context.Context,
	convID string,
	compacted []*conversation.Message,
	summary string,
	contextSize int,
) error {
	if convID == "" {
		c.stateMu.RLock()
		convID = c.sessState.ActiveConvID
		c.stateMu.RUnlock()
	}
	if convID == "" {
		return errors.New("client.persistCompactedGeneration: no active conversation")
	}
	if len(compacted) == 0 {
		return errors.New("client.persistCompactedGeneration: compacted generation is empty")
	}

	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return errNoConvManager
	}

	lock := c.conversationMetadataLock(convID)
	lock.Lock()
	defer lock.Unlock()

	conv, err := mgr.Resume(ctx, convID)
	if err != nil {
		return fmt.Errorf("load conversation for compaction: %w", err)
	}
	working := cloneConversationForCompaction(conv)
	durableCompacted := make([]*conversation.Message, len(compacted))
	for i, msg := range compacted {
		if msg == nil {
			return fmt.Errorf("client.persistCompactedGeneration: message %d is nil", i)
		}
		durableCompacted[i] = msg.Clone()
		if durableCompacted[i].Metadata == nil {
			durableCompacted[i].Metadata = make(map[string]any)
		}
		durableCompacted[i].Metadata[conversation.CompactionGeneratedMetadataKey] = true
	}
	working.AdvanceActiveContext(durableCompacted, summary, contextSize)
	if err := mgr.Save(ctx, working); err != nil {
		return fmt.Errorf("save compacted conversation: %w", err)
	}
	return nil
}

func (c *Client) conversationHistoryForExecution(ctx context.Context, convID string) ([]*conversation.Message, error) {
	if convID == "" {
		return nil, nil
	}
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return nil, errNoConvManager
	}
	conv, err := mgr.Resume(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("load conversation history: %w", err)
	}
	return conv.ActiveMessages(), nil
}

func cloneConversationForCompaction(conv *conversation.Conversation) *conversation.Conversation {
	working := *conv
	working.Messages = append([]*conversation.Message(nil), conv.Messages...)
	if conv.CompactionState != nil {
		state := *conv.CompactionState
		working.CompactionState = &state
	}
	if conv.Summary != nil {
		summaryCopy := *conv.Summary
		summaryCopy.ModelsUsed = append([]string(nil), conv.Summary.ModelsUsed...)
		working.Summary = &summaryCopy
	}
	working.Metadata.Tags = append([]string(nil), conv.Metadata.Tags...)
	if conv.Metadata.Custom != nil {
		working.Metadata.Custom = make(map[string]any, len(conv.Metadata.Custom))
		for key, value := range conv.Metadata.Custom {
			working.Metadata.Custom[key] = value
		}
	}
	return &working
}

// GetConversationMessages returns the message history for convID with
// optional filtering/pagination via manager.GetMessagesOptions.
func (c *Client) GetConversationMessages(ctx context.Context, convID string, opts manager.GetMessagesOptions) ([]*conversation.Message, error) {
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return nil, errNoConvManager
	}
	return mgr.GetMessages(ctx, convID, opts)
}

// CompleteConversation marks convID as completed via the conversation
// manager.  Status changes are persisted immediately.
func (c *Client) CompleteConversation(ctx context.Context, convID string) error {
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return errNoConvManager
	}
	return mgr.Complete(ctx, convID)
}

// ArchiveConversation marks convID as archived via the conversation
// manager.  Archived conversations remain on disk and can still be
// loaded by ID; they are typically excluded from default listings.
func (c *Client) ArchiveConversation(ctx context.Context, convID string) error {
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return errNoConvManager
	}
	return mgr.Archive(ctx, convID)
}

// SetConversationTitle sets a user-selected title and saves it.
func (c *Client) SetConversationTitle(ctx context.Context, convID string, title string) error {
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return errNoConvManager
	}
	lock := c.conversationMetadataLock(convID)
	lock.Lock()
	conv, err := mgr.Resume(ctx, convID)
	if err != nil {
		lock.Unlock()
		return fmt.Errorf("client.SetConversationTitle: failed to load conversation: %w", err)
	}
	conv.Title = title
	state := conversationMetadataState(conv)
	state["title_source"] = "manual"
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := mgr.Save(ctx, conv); err != nil {
		lock.Unlock()
		return fmt.Errorf("client.SetConversationTitle: failed to save title: %w", err)
	}
	lock.Unlock()
	// Notify subscribers so a thin UI re-renders the conversation list.
	c.dispatchEvent(Event{Kind: EventConvUpdated, Payload: ConvPayload{ConvID: convID}, At: time.Now()})
	return nil
}

// SetConversationSummary sets the generated recap for a conversation and saves
// it. New automatic callers should prefer RefreshConversationMetadata so title,
// recap, generation status, and retry metadata remain consistent.
func (c *Client) SetConversationSummary(ctx context.Context, convID string, recap string) error {
	c.mu.RLock()
	mgr := c.convManager
	c.mu.RUnlock()
	if mgr == nil {
		return errNoConvManager
	}
	lock := c.conversationMetadataLock(convID)
	lock.Lock()
	conv, err := mgr.Resume(ctx, convID)
	if err != nil {
		lock.Unlock()
		return fmt.Errorf("client.SetConversationSummary: failed to load conversation: %w", err)
	}
	conv.EnsureSummary().Recap = recap
	if err := mgr.Save(ctx, conv); err != nil {
		lock.Unlock()
		return fmt.Errorf("client.SetConversationSummary: failed to save summary: %w", err)
	}
	lock.Unlock()
	// Notify subscribers so a thin UI re-renders the conversation list.
	c.dispatchEvent(Event{Kind: EventConvUpdated, Payload: ConvPayload{ConvID: convID}, At: time.Now()})
	return nil
}
