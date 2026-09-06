# Compaction Bug Report

**Date:** 2025-01-20  
**Status:** Open  
**Priority:** High  
**Component:** `sdk/compaction`, `internal/chat`

---

## Executive Summary

The TUI compaction feature (`/compact` command) creates malformed conversations that break the Anthropic API requirements. After compaction, the new conversation contains multiple consecutive user messages and lacks assistant responses, causing the conversation to fail when the user sends subsequent messages.

---

## Table of Contents

1. [Bug Overview](#bug-overview)
2. [Affected Files](#affected-files)
3. [Detailed Bug Analysis](#detailed-bug-analysis)
4. [Root Cause](#root-cause)
5. [Reproduction Steps](#reproduction-steps)
6. [Fix Specifications](#fix-specifications)
7. [Implementation Checklist](#implementation-checklist)
8. [Testing Requirements](#testing-requirements)

---

## Bug Overview

### Symptoms
- Conversation breaks after compaction
- API errors when sending messages after `/compact`
- "first message must be from user" or alternation errors
- Context loss between agents

### Impact
- Users cannot continue conversations after compaction
- Token reduction feature is effectively broken
- Long conversations become unusable

---

## Affected Files

| File | Function | Issue |
|------|----------|-------|
| `sdk/compaction/compaction.go` | `BuildCompactedMessages()` | Creates consecutive user messages |
| `sdk/compaction/compaction.go` | `Compact()` | Missing message field initialization |
| `internal/chat/app.go` | `performCompaction()` | UI/SDK message sync issues |
| `internal/chat/sdk_integration.go` | `CreateCompactedConversation()` | Token count not initialized |
| `sdk/provider/anthropic/translate.go` | `translateMessages()` | Validates alternating messages |

---

## Detailed Bug Analysis

### Bug 1: Consecutive User Messages Break API Requirements

**Location:** `sdk/compaction/compaction.go:367-406` - `BuildCompactedMessages()`

**Problem:** The compacted conversation creates multiple consecutive user messages:

```go
// Current implementation creates:
func (s *Service) BuildCompactedMessages(result *CompactionResult) []*conversation.Message {
    var messages []*conversation.Message
    
    // 1. Recent user messages (multiple user messages)
    for _, userMsg := range result.RecentUserMessages {
        messages = append(messages, &conversation.Message{
            Role:    conversation.RoleUser,  // ← User
            Content: userMsg,
        })
    }
    
    // 2. Summary (another user message)
    messages = append(messages, &conversation.Message{
        Role:    conversation.RoleUser,  // ← User again!
        Content: result.Summary,
    })
    
    // 3. Recovered files (more user messages)
    for _, file := range result.RecoveredFiles {
        messages = append(messages, &conversation.Message{
            Role:    conversation.RoleUser,  // ← User again!
            Content: content,
        })
    }
    
    return messages
}
```

**Result after compaction:**
```
[user] "Recent message 1"
[user] "Recent message 2"      ← VIOLATION: Consecutive user
[user] "Summary: ..."          ← VIOLATION: Consecutive user  
[user] "Recovered file..."     ← VIOLATION: Consecutive user
```

**API Requirement (translate.go:487-491):**
```go
// Anthropic requires messages to alternate user/assistant
// First message must be from user
if len(anthropicMessages) > 0 && anthropicMessages[0].Role != "user" {
    return nil, sdkerror.Permanent(
        "anthropic.invalid_first_message",
        "first message must be from user",
    )
}
```

---

### Bug 2: No Assistant Response in Compacted History

**Location:** `sdk/compaction/compaction.go:367-406`

**Problem:** After compaction, the new conversation has ONLY user messages. There is no assistant message acknowledging the context handoff.

**Why this matters:**
- The model expects conversation context from previous assistant responses
- Without assistant messages, the model doesn't know what work was done
- The "handoff" summary is presented as if the user wrote it, confusing the model

**Current broken flow:**
```
BEFORE COMPACTION:
[user] "Help me build X"
[assistant] "Sure, I'll help..." + tool calls
[tool] results
[user] "Now do Y"
[assistant] "Done with Y..."
... (100 messages)

AFTER COMPACTION:
[user] "Now do Y"                    ← Recent user message
[user] "Summary: We built X..."      ← Summary (NO ASSISTANT!)
[user] "New message from user"       ← User's next message

MODEL SEES: 3 consecutive user messages with no assistant context
```

---

### Bug 3: Messages Lack Required Fields

**Location:** `sdk/compaction/compaction.go:371-376`

**Problem:** Compacted messages are created as minimal structs without required fields:

```go
// Current (broken):
messages = append(messages, &conversation.Message{
    Role:    conversation.RoleUser,
    Content: userMsg,
    // MISSING: ID, Timestamp, Tokens
})
```

**Required fields in `conversation.Message` (message.go):**
```go
type Message struct {
    ID        string         `json:"id"`        // ← MISSING
    Timestamp time.Time      `json:"timestamp"` // ← MISSING
    Role      Role           `json:"role"`
    Content   string         `json:"content"`
    Tokens    *TokenUsage    `json:"tokens,omitempty"` // ← MISSING
    // ... other fields
}
```

**Impact:**
- Message tracking fails (no unique ID)
- Ordering issues (no timestamp)
- Token counting broken (no token info)
- Conversation stats incorrect

---

### Bug 4: UI Messages Don't Match SDK Messages

**Location:** `internal/chat/app.go:5280-5287`

**Problem:** After compaction, `a.messages` (UI state) is rebuilt with only Role and Content:

```go
// Update UI messages to show the compacted conversation
a.messages = nil
for _, msg := range compactedMessages {
    a.messages = append(a.messages, Message{
        Role:    string(msg.Role),
        Content: msg.Content,
        // MISSING: Timestamp, Metadata, ToolCalls, ToolResults, Thinking
    })
}
```

**UI Message struct has more fields:**
```go
type Message struct {
    Role          string
    Content       string
    Timestamp     time.Time           // ← NOT SET
    Metadata      map[string]interface{} // ← NOT SET
    ToolCalls     []ToolCallDisplay   // ← NOT SET
    ToolResults   []ToolResultDisplay // ← NOT SET
    Thinking      string              // ← NOT SET
    OrderedBlocks []OrderedBlock      // ← NOT SET
}
```

---

### Bug 5: Race Condition in Conversation ID Update

**Location:** `internal/chat/app.go:2139-2143` and `app.go:5269-5278`

**Problem:** `currentConvID` is updated in two places with potential race:

```go
// In performCompaction() - runs in goroutine (app.go:5278)
result.NewConvID = newConvID

// In Update() message handler (app.go:2139-2143)
case commands.CompactCompletedMsg:
    if msg.NewConvID != "" {
        oldConvID := a.currentConvID
        a.currentConvID = msg.NewConvID  // ← Update here
    }
```

**The race:**
```go
// app.go:2106 - performCompaction runs in goroutine
return a, func() tea.Msg {
    result, err := a.performCompaction(msg.Manual)  // ← Modifies a.messages!
    // ...
}
```

`performCompaction()` modifies `a.messages` directly (line 5281-5287) while running in a background goroutine, but the main Update loop also accesses `a.messages`.

---

### Bug 6: Token Count Not Properly Initialized

**Location:** `internal/chat/sdk_integration.go:3608-3624`

**Problem:** New conversation is created without token count:

```go
func (sdk *SDKIntegration) CreateCompactedConversation(...) (string, error) {
    // Create new conversation - TotalTokens = 0
    conv, err := sdk.manager.Create(ctx, manager.CreateOptions{
        Mode: "chat",
    })
    
    // Add messages but DON'T update conv.TotalTokens
    for _, msg := range messages {
        sdk.manager.AddMessage(ctx, conv.ID, msg)
        // AddMessage updates conv internally, but msg.Tokens is nil!
    }
    
    // conv.TotalTokens is still 0 or incorrect
    return conv.ID, nil
}
```

**In AddMessage (manager.go:167):**
```go
func (c *Conversation) AddMessage(msg *Message) {
    c.Messages = append(c.Messages, msg)
    c.UpdatedAt = time.Now()
    
    // Update token count
    if msg.Tokens != nil {  // ← msg.Tokens is nil for compacted messages!
        c.TotalTokens += msg.Tokens.Total
    }
}
```

---

## Root Cause

The compaction system was designed without considering:

1. **Anthropic API message alternation requirement** - Messages must alternate user/assistant
2. **Conversation continuity** - Model needs assistant responses for context
3. **Message completeness** - All fields must be populated for proper tracking
4. **Concurrency safety** - Background operations modifying shared state

---

## Reproduction Steps

1. Start a new conversation
2. Have a long conversation with multiple tool calls (use many tokens)
3. Run `/compact` command
4. Observe: Compaction appears to succeed
5. Send a new message
6. **ERROR:** API fails with message alternation or context errors

---

## Fix Specifications

### Fix 1: Combine All User Content Into Single Message + Add Assistant Acknowledgment

**File:** `sdk/compaction/compaction.go`

**Replace `BuildCompactedMessages()` with:**

```go
import (
    "fmt"
    "strings"
    "time"
    
    "github.com/google/uuid"
    "github.com/Swarm-Code/mono/swarm-sdk/conversation"
)

// BuildCompactedMessages creates the compacted message array.
// CRITICAL: Must produce alternating user/assistant messages for API compatibility.
// Structure:
//   1. Single user message containing: summary + recent context + recovered files
//   2. Assistant acknowledgment message
func (s *Service) BuildCompactedMessages(result *CompactionResult) []*conversation.Message {
    var messages []*conversation.Message
    
    // ==========================================
    // BUILD SINGLE COMBINED USER MESSAGE
    // ==========================================
    var combinedContent strings.Builder
    
    // Section 1: Handoff summary (most important)
    combinedContent.WriteString(result.Summary)
    
    // Section 2: Recent user messages for continuity
    if len(result.RecentUserMessages) > 0 {
        combinedContent.WriteString("\n\n---\n\n")
        combinedContent.WriteString("## Recent User Requests\n\n")
        for i, userMsg := range result.RecentUserMessages {
            combinedContent.WriteString(fmt.Sprintf("**Request %d:**\n%s\n\n", i+1, userMsg))
        }
    }
    
    // Section 3: Recovered files (auto-compaction only)
    if len(result.RecoveredFiles) > 0 {
        combinedContent.WriteString("\n\n---\n\n")
        combinedContent.WriteString("## Recovered Files\n\n")
        for _, file := range result.RecoveredFiles {
            truncatedNote := ""
            if file.Truncated {
                truncatedNote = " [truncated]"
            }
            combinedContent.WriteString(fmt.Sprintf(
                "### %s%s\n\n```\n%s\n```\n\n",
                file.Path,
                truncatedNote,
                addLineNumbers(file.Content),
            ))
        }
    }
    
    // Calculate tokens for the combined message
    combinedText := combinedContent.String()
    userTokens := EstimateTokens(combinedText)
    
    // Create the single user message with all required fields
    userMessage := &conversation.Message{
        ID:        uuid.New().String(),
        Timestamp: time.Now(),
        Role:      conversation.RoleUser,
        Content:   combinedText,
        Tokens: &conversation.TokenUsage{
            Total: userTokens,
        },
    }
    messages = append(messages, userMessage)
    
    // ==========================================
    // ADD ASSISTANT ACKNOWLEDGMENT
    // ==========================================
    // This is CRITICAL for:
    // 1. API compliance (alternating messages)
    // 2. Model context (knows handoff occurred)
    // 3. Conversation continuity
    
    ackContent := `I've received the context handoff summary and understand the current state of our work. I have access to:

- The technical context and project overview
- Recent code changes and their locations  
- Current status and pending tasks
- Key decisions that were made

I'm ready to continue. What would you like to work on next?`
    
    ackTokens := EstimateTokens(ackContent)
    
    assistantMessage := &conversation.Message{
        ID:        uuid.New().String(),
        Timestamp: time.Now().Add(time.Millisecond), // Slightly after user message
        Role:      conversation.RoleAssistant,
        Content:   ackContent,
        Tokens: &conversation.TokenUsage{
            Output: ackTokens,
            Total:  ackTokens,
        },
    }
    messages = append(messages, assistantMessage)
    
    return messages
}
```

---

### Fix 2: Update Token Count in CreateCompactedConversation

**File:** `internal/chat/sdk_integration.go`

**Modify `CreateCompactedConversation()`:**

```go
func (sdk *SDKIntegration) CreateCompactedConversation(ctx context.Context, oldConvID string, messages []*conversation.Message) (string, error) {
    if sdk == nil || sdk.manager == nil {
        return "", fmt.Errorf("SDK not initialized")
    }

    // Create new conversation
    conv, err := sdk.manager.Create(ctx, manager.CreateOptions{
        Mode: "chat",
    })
    if err != nil {
        return "", fmt.Errorf("failed to create compacted conversation: %w", err)
    }

    // Track total tokens for the new conversation
    totalTokens := 0

    // Add all compacted messages to the new conversation
    for _, msg := range messages {
        if err := sdk.manager.AddMessage(ctx, conv.ID, msg); err != nil {
            sdk.logger.Warn(ctx, "failed_to_add_compacted_message",
                observability.Field{Key: "conversation_id", Value: conv.ID},
                observability.Field{Key: "error", Value: err.Error()})
            // Continue adding other messages even if one fails
        }
        
        // Accumulate tokens
        if msg.Tokens != nil {
            totalTokens += msg.Tokens.Total
        } else {
            // Estimate if not provided
            totalTokens += estimateTokens(msg.Content)
        }
    }

    // Update conversation token counts
    conv, err = sdk.manager.Resume(ctx, conv.ID)
    if err == nil {
        conv.TotalTokens = totalTokens
        conv.CurrentContextSize = totalTokens
        if saveErr := sdk.manager.Save(ctx, conv); saveErr != nil {
            sdk.logger.Warn(ctx, "failed_to_save_token_count",
                observability.Field{Key: "conversation_id", Value: conv.ID},
                observability.Field{Key: "error", Value: saveErr.Error()})
        }
    }

    // Archive the old conversation (don't delete in case user wants to recover)
    if oldConvID != "" {
        if err := sdk.manager.Archive(ctx, oldConvID); err != nil {
            sdk.logger.Warn(ctx, "failed_to_archive_old_conversation",
                observability.Field{Key: "old_conversation_id", Value: oldConvID},
                observability.Field{Key: "error", Value: err.Error()})
        }
    }

    sdk.logger.Info(ctx, "compacted_conversation_created",
        observability.Field{Key: "old_conversation_id", Value: oldConvID},
        observability.Field{Key: "new_conversation_id", Value: conv.ID},
        observability.Field{Key: "message_count", Value: len(messages)},
        observability.Field{Key: "total_tokens", Value: totalTokens})

    return conv.ID, nil
}

// Helper function for token estimation
func estimateTokens(text string) int {
    return int(float64(len(text)) * 0.25) // 4 chars ≈ 1 token
}
```

---

### Fix 3: Fix Race Condition in performCompaction

**File:** `internal/chat/app.go`

**Problem:** `performCompaction()` modifies `a.messages` directly while running in a goroutine.

**Solution:** Return the new messages in the result, update UI state in the main Update() handler.

```go
// In performCompaction() - DON'T modify a.messages here
func (a *App) performCompaction(manual bool) (*compaction.CompactionResult, error) {
    // ... existing code ...
    
    if result.Compacted && result.Error == nil {
        compactedMessages := a.compactionService.BuildCompactedMessages(result)
        
        newConvID, err := a.sdk.CreateCompactedConversation(ctx, a.currentConvID, compactedMessages)
        if err != nil {
            // ... error handling ...
        }
        
        result.NewConvID = newConvID
        
        // REMOVE THIS - don't modify a.messages in goroutine:
        // a.messages = nil
        // for _, msg := range compactedMessages { ... }
        
        // Instead, store messages in result for the handler to use
        result.CompactedMessages = compactedMessages  // Add this field to CompactionResult
    }
    
    return result, nil
}

// In Update() handler - modify a.messages here (main goroutine)
case commands.CompactCompletedMsg:
    a.isCompacting = false
    a.loadingIndicator.Stop()
    
    if msg.NewConvID != "" {
        a.currentConvID = msg.NewConvID
        
        // Reload messages from SDK (safe, in main goroutine)
        a.loadMessagesFromSDK(msg.NewConvID)
    }
    
    // ... rest of handler ...
```

**Also add to CompactionResult struct:**

```go
// In sdk/compaction/compaction.go
type CompactionResult struct {
    // ... existing fields ...
    
    // CompactedMessages holds the messages for the new conversation
    // Used by UI to update state after compaction completes
    CompactedMessages []*conversation.Message
}
```

---

### Fix 4: Improve UI Message Sync

**File:** `internal/chat/app.go`

**Replace the manual message building with `loadMessagesFromSDK()`:**

```go
// In CompactCompletedMsg handler:
case commands.CompactCompletedMsg:
    a.isCompacting = false
    a.loadingIndicator.Stop()
    a.loadingIndicator.SetText("Agent is thinking")

    if msg.NewConvID != "" {
        oldConvID := a.currentConvID
        a.currentConvID = msg.NewConvID
        logDebug("[COMPACT] Switched from conversation %s to %s", oldConvID, msg.NewConvID)
        
        // CRITICAL: Reload messages from SDK to ensure UI is in sync
        // This properly populates all Message fields (Timestamp, Metadata, etc.)
        a.loadMessagesFromSDK(msg.NewConvID)
    }

    // Update token count
    if a.sdk != nil && a.currentConvID != "" {
        ctx := context.Background()
        a.tokenCount = a.sdk.GetConversationTokens(ctx, a.currentConvID)
    }

    // Clear and rebuild viewport
    a.invalidateViewportCache()
    a.updateViewportContent()
    
    // Show success notification
    reduction := 0
    if msg.OriginalTokens > 0 {
        reduction = 100 - (msg.CompactedTokens * 100 / msg.OriginalTokens)
    }
    a.addNotification("success", fmt.Sprintf("Context compacted: %d → %d tokens (%d%% reduction)",
        msg.OriginalTokens, msg.CompactedTokens, reduction))
    
    return a, nil
```

---

## Implementation Checklist

- [ ] **Bug 1:** Modify `BuildCompactedMessages()` to create single user message
- [ ] **Bug 2:** Add assistant acknowledgment message after summary
- [ ] **Bug 3:** Add ID, Timestamp, Tokens fields to all compacted messages
- [ ] **Bug 4:** Use `loadMessagesFromSDK()` instead of manual message building
- [ ] **Bug 5:** Remove direct `a.messages` modification from `performCompaction()`
- [ ] **Bug 6:** Update token counts in `CreateCompactedConversation()`
- [ ] **Add field:** `CompactedMessages` to `CompactionResult` struct
- [ ] **Update tests:** Add test for alternating message validation
- [ ] **Add logging:** Debug logs for compaction message structure

---

## Testing Requirements

### Unit Tests

```go
// sdk/tests/compaction/compaction_test.go

func TestBuildCompactedMessages_AlternatingRoles(t *testing.T) {
    config := compaction.DefaultConfig(200000)
    service := compaction.NewService(config)
    
    result := &compaction.CompactionResult{
        Summary: "## Summary\nTest summary content",
        RecentUserMessages: []string{
            "First request",
            "Second request",
        },
        RecoveredFiles: []compaction.RecoveredFile{
            {Path: "/test/file.go", Content: "package main", Tokens: 10},
        },
    }
    
    messages := service.BuildCompactedMessages(result)
    
    // Must have exactly 2 messages: 1 user + 1 assistant
    if len(messages) != 2 {
        t.Errorf("Expected 2 messages, got %d", len(messages))
    }
    
    // First must be user
    if messages[0].Role != conversation.RoleUser {
        t.Errorf("First message must be user, got %s", messages[0].Role)
    }
    
    // Second must be assistant
    if messages[1].Role != conversation.RoleAssistant {
        t.Errorf("Second message must be assistant, got %s", messages[1].Role)
    }
    
    // All messages must have ID
    for i, msg := range messages {
        if msg.ID == "" {
            t.Errorf("Message %d missing ID", i)
        }
    }
    
    // All messages must have Timestamp
    for i, msg := range messages {
        if msg.Timestamp.IsZero() {
            t.Errorf("Message %d missing Timestamp", i)
        }
    }
    
    // All messages must have Tokens
    for i, msg := range messages {
        if msg.Tokens == nil {
            t.Errorf("Message %d missing Tokens", i)
        }
    }
}

func TestBuildCompactedMessages_ContainsAllContent(t *testing.T) {
    config := compaction.DefaultConfig(200000)
    service := compaction.NewService(config)
    
    result := &compaction.CompactionResult{
        Summary: "## Technical Context\nGo project",
        RecentUserMessages: []string{"Fix the bug"},
        RecoveredFiles: []compaction.RecoveredFile{
            {Path: "/main.go", Content: "package main"},
        },
    }
    
    messages := service.BuildCompactedMessages(result)
    userContent := messages[0].Content
    
    // User message must contain summary
    if !strings.Contains(userContent, "Technical Context") {
        t.Error("User message missing summary content")
    }
    
    // User message must contain recent request
    if !strings.Contains(userContent, "Fix the bug") {
        t.Error("User message missing recent user message")
    }
    
    // User message must contain recovered file
    if !strings.Contains(userContent, "/main.go") {
        t.Error("User message missing recovered file path")
    }
}
```

### Integration Test

```go
func TestCompaction_EndToEnd(t *testing.T) {
    // 1. Create conversation with many messages
    // 2. Run compaction
    // 3. Verify new conversation has alternating messages
    // 4. Send a new message
    // 5. Verify API call succeeds (no alternation errors)
}
```

### Manual Testing

1. Start conversation, use many tokens (read files, etc.)
2. Run `/compact`
3. Check debug screen for message structure
4. Send new message - should succeed
5. Verify context is maintained

---

## Related Documentation

- `sdk/compaction/prompt.go` - Compression prompts
- `sdk/provider/anthropic/translate.go` - Message translation and validation
- `sdk/conversation/message.go` - Message struct definition
- `internal/chat/chat_state.go` - UI message handling

---

## Notes

- The fix prioritizes API compatibility over preserving exact message history
- Combining user content into single message loses individual message boundaries but ensures API compliance
- The assistant acknowledgment is synthetic but necessary for proper context flow
- Consider adding a "compaction marker" in metadata to identify compacted conversations
