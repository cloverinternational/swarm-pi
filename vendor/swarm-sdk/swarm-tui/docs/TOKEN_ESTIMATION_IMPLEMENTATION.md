# Token Estimation with System Prompt - Implementation Summary

**Branch**: `feature/token-estimation-with-system-prompt`  
**Status**: COMPLETE - All 7 phases implemented  
**Commits**: 3 surgical commits  

## Overview

This feature enables the context bar to show an **estimated** token count immediately when a conversation loads, using the system prompt. The estimate seamlessly transitions to **real** tokens when they arrive from the API.

## Implementation Details

### Phase 1: Estimation Helpers (Commit 6a1e56e)

**File**: `internal/chat/app.go`  
**Location**: Lines 700-750 (new section before NewApp)  

Three helper functions added:
1. **`estimateTokens(text string) int`** - Uses 3.7 chars/token ratio
2. **`estimateTokensFromMessages(messages []Message) int`** - Sums tokens across messages
3. **`(a *App) getSystemPromptEstimate() int`** - Extracts system prompt tokens

Also added `"math"` import for `math.Ceil()`.

### Phase 2: Model State Fields (Commit 6a1e56e)

**File**: `internal/chat/app.go`  
**Location**: App struct around line 544  

Added two fields:
```go
tokenCountIsEstimate bool  // True if estimated, false if real
lastRealTokenCount   int   // Track real value from API
```

### Phase 3: Conversation Load Estimation (Commit cb676e6)

**File**: `internal/chat/chat_state.go`  
**Function**: `openConversation()`  
**Location**: After line 473  

When conversation loads:
1. Reset `tokenCount = 0`
2. **NEW**: Estimate system prompt tokens
3. **NEW**: Set `tokenCountIsEstimate = true`
4. **NEW**: Mark side panel cache as invalid

**Key Code**:
```go
systemPromptTokens := a.getSystemPromptEstimate()
a.tokenCount = systemPromptTokens
a.tokenCountIsEstimate = true
logDebug("[openConversation] Estimated system prompt: %d tokens", systemPromptTokens)
```

### Phase 4: Override Estimate with Real Tokens (Commit cb676e6)

**File**: `internal/chat/chat_state.go`  
**Function**: `openConversation()`  
**Location**: Lines 486-489  

If the conversation has `CurrentContextSize` (real tokens from a prior API call):
- Replace the estimate with real count
- Set `tokenCountIsEstimate = false`
- Track `lastRealTokenCount`

### Phase 5: Send-Time Estimation (Commit 6a1e56e)

**File**: `internal/chat/app.go`  
**Location**: Before `sdk.ExecuteMessage()` call (~line 10283)  

When user sends a message:
1. Calculate: system + message + context tokens
2. Set `tokenCount = total estimate`
3. Set `tokenCountIsEstimate = true`
4. **CRITICAL**: Invalidate side panel cache

**Key Code**:
```go
systemPromptTokens := a.getSystemPromptEstimate()
messageTokens := estimateTokens(msgText)
contextTokens := estimateTokensFromMessages(a.messages)
estimatedTotal := systemPromptTokens + messageTokens + contextTokens

a.tokenCount = estimatedTotal
a.tokenCountIsEstimate = true
a.sidePanelCache.valid = false  // Force UI update
```

### Phase 6: Replace Estimate with Real Tokens (Commit 6a1e56e) - CRITICAL FIX

**File**: `internal/chat/app.go`  
**Function**: `Update()` - `tokenUpdateMsg` case  
**Location**: Lines 3832-3850  

**THE CRITICAL ISSUE**: The original code updated `tokenCount` but **never invalidated the side panel cache**, so the UI never re-rendered.

**What was changed**:
```go
if msg.inputTokens > 0 {
    oldCount := a.tokenCount
    wasEstimate := a.tokenCountIsEstimate
    
    a.tokenCount = msg.inputTokens
    a.tokenCountIsEstimate = false         // Mark as real
    a.lastRealTokenCount = msg.inputTokens
    a.sidePanelCache.valid = false         // *** CRITICAL FIX ***
    
    logDebug("[APP] Updated tokenCount=%d (replaced estimate)", a.tokenCount)
}
```

The **`a.sidePanelCache.valid = false`** is the key line that forces the UI to re-render with the new real token count.

### Phase 7: UI Rendering with Visual Distinction (Commit 9b3ba29)

**File**: `internal/chat/sidepanel.go`  
**Location**: Token display section (~lines 316-388)  

**What was changed**:
```go
// PHASE 7: Format token display - show "~" prefix for estimates
tokenCountDisplay := formatTokenCount(inputTokens)
if app.tokenCountIsEstimate {
    tokenCountDisplay = "~ " + tokenCountDisplay  // Shows "~ 2500" for estimate
}
```

Now when rendered:
- **Estimated**: `~ 2500` (with tilde prefix)
- **Real**: `2500` (clean, no prefix)

## Data Flow

```
CONVERSATION LOAD
├─ getSystemPromptEstimate() → 5000 tokens
├─ tokenCount = 5000
├─ tokenCountIsEstimate = true
└─ Side panel shows "~ 5000"

USER SENDS MESSAGE
├─ Calculate: system (5000) + message (100) + context (200) = 5300
├─ tokenCount = 5300
├─ tokenCountIsEstimate = true
├─ sidePanelCache.valid = false (force re-render)
└─ Side panel shows "~ 5300"

FIRST message_start ARRIVES (API)
├─ inputTokens = 4987 (real value)
├─ tokenCount = 4987
├─ tokenCountIsEstimate = false
├─ sidePanelCache.valid = false (force re-render)
└─ Side panel shows "4987" (no tilde)

STREAM CONTINUES
├─ tokenCount unchanged
├─ isEstimate = false
└─ Side panel shows "4987"

message_stop ARRIVES
├─ Final tokens available
├─ tokenCount updated if needed
└─ Message saved to history
```

## Key Design Decisions

1. **3.7 chars/token ratio**: Validated against token-counting-experiment historical data
2. **Estimate on load**: Users see context bar immediately when opening old conversations
3. **Estimate on send**: Instant feedback on approximate context size before API responds
4. **Visual distinction**: "~" prefix makes it crystal clear what's estimated vs real
5. **Cache invalidation**: Side panel cache must be invalidated for UI to actually update

## Testing Checklist

- [ ] Conversation load shows estimated tokens for system prompt
- [ ] Sending message shows combined estimate (system + message + context)
- [ ] Real tokens from API replace estimate seamlessly (~100ms transition)
- [ ] "~" prefix appears/disappears correctly in side panel
- [ ] No UI flicker during estimate → real transition
- [ ] Token count persists in conversation history
- [ ] Edge cases: empty system prompt, very long system prompt, rapid sends
- [ ] No race conditions with mid-flight real tokens

## Files Modified

1. **`internal/chat/app.go`** (Commit 6a1e56e)
   - Added: 3 helper functions, 2 model fields
   - Modified: tokenUpdateMsg handler (critical cache invalidation fix)
   - Modified: Message send (add estimation before ExecuteMessage)

2. **`internal/chat/chat_state.go`** (Commit cb676e6)
   - Modified: openConversation() to estimate system prompt
   - Modified: Real token replacement logic

3. **`internal/chat/sidepanel.go`** (Commit 9b3ba29)
   - Modified: Token display to show "~" for estimates

## Surgical Precision

This implementation:
- ✅ Makes **minimal changes** to existing code
- ✅ **No breaking changes** to public APIs
- ✅ Adds features **incrementally** (3 commits)
- ✅ Preserves all existing functionality
- ✅ Uses **existing patterns** (cache invalidation, message types)
- ✅ No external dependencies beyond standard library

## Future Enhancements

1. **Store estimate accuracy**: Track how often estimates differ from real
2. **Improve ratio based on model**: Use model-specific char/token ratios
3. **Show confidence**: Display "~ 2500 ±50" with range
4. **Cache estimates**: Remember system prompt token counts
5. **Performance optimization**: Pre-calculate system prompt tokens on startup
