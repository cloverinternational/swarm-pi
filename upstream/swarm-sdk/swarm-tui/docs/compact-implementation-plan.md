# Making SwarmOS `/compact` Work Like SwarmCode - Detailed Implementation Plan

## Problem Decomposition (First Principles)

### Part 1: Understanding the Problem Space

#### What is `/compact` fundamentally?

At the most basic level, `/compact` is a **function that transforms conversation state**:

```
INPUT:  Long conversation (100+ messages, 150K tokens)
OUTPUT: Short conversation (1-2 messages, 10-15K tokens)

CONSTRAINT: The output must preserve enough information to continue work effectively
```

This is a **lossy compression** problem where we need to:
1. Extract essential information
2. Discard non-essential information  
3. Package the essential information in a format the LLM can understand

#### What are the components?

```
Component 1: INPUT GATHERING
├── What: Get all conversation messages
├── Where: From current conversation state
└── How: Read from message array

Component 2: COMPRESSION
├── What: Transform messages into summary
├── Where: Send to LLM with special prompt
└── How: LLM generates summary text

Component 3: STATE REPLACEMENT
├── What: Replace old conversation with new one
├── Where: In conversation storage/state
└── How: Clear old, set new

Component 4: USER FEEDBACK
├── What: Show user what happened
├── Where: Terminal/UI
└── How: Display messages, indicators, stats
```

### Part 2: Current SwarmOS Flow (Granular Breakdown)

Let me trace every step of what happens now:

```
User types: "/compact standard"
    |
    v
[STEP 1: Command Parsing] (commands/compact.go:34-48)
├── Input: args = ["standard"]
├── Logic: Parse strategy from args[0]
├── Switch statement checks: "standard" → StrategyStandard
├── Output: strategy = compaction.StrategyStandard
    |
    v
[STEP 2: Message Creation] (commands/compact.go:50-57)
├── Input: strategy = StrategyStandard
├── Logic: Create CompactRequestMsg struct
├── Fields: Manual=true, Strategy=StrategyStandard
├── Output: CompactRequestMsg{}
    |
    v
[STEP 3: Message Handling] (app.go:2692-2773)
├── Input: msg = CompactRequestMsg
├── Logic: Check if compactionService exists && sdk exists && currentConvID exists
├── If true:
│   ├── Get current token count from SDK
│   ├── Set isCompacting = true
│   ├── Show loading indicator
│   ├── Add notification
│   └── Launch background task (func() tea.Msg)
├── Output: Background goroutine started
    |
    v
[STEP 4: Background Task] (app.go:2736-2773)
├── Input: compactReq = CompactRequestMsg
├── Call: result, compCtx, err := a.performCompaction(compactReq)
├── Logic: Handle errors or build CompactCompletedMsg
├── Output: CompactCompletedMsg or CompactErrorMsg
    |
    v
[STEP 5: performCompaction] (app.go:6571-6689)
├── Input: req = CompactRequestMsg
│
├── [5.1] Load conversation from SDK
│   ├── Input: currentConvID
│   ├── Call: conv := a.sdk.GetConversation(ctx, a.currentConvID)
│   ├── Output: conv = Conversation{Messages: [...], TotalTokens: 150000}
│
├── [5.2] Get compaction model from settings
│   ├── Call: compactionModel := a.settingsManager.GetAdvancedSettings().GetCompactionModel()
│   ├── Output: compactionModel = "claude-3-5-haiku-20241022"
│
├── [5.3] Build compaction context
│   ├── Call: compCtx := a.buildCompactionContext(req)
│   ├── Logic: Collect current mode, MCP servers, message count
│   ├── Output: CompactionContext{CurrentMode, ActiveMCPServers, MessageCount, Strategy}
│
├── [5.4] Configure compaction service
│   ├── Create new CompactionConfig with:
│   │   ├── ContextLimit: 200000
│   │   ├── AutoCompactThreshold: 0.92
│   │   ├── MaxFilesToRecover: 5
│   │   ├── MaxTokensPerFile: 10000
│   │   ├── MaxTotalFileTokens: 50000
│   │   ├── SummarizeFunc: lambda that calls a.sdk.GenerateCompactionSummary()
│   │   └── ReadFileFunc: lambda that calls os.ReadFile()
│   ├── Call: a.compactionService = compaction.NewService(config)
│   ├── Output: Service initialized with callbacks
│
├── [5.5] Perform compaction
│   ├── Call: result, err := a.compactionService.CompactWithContext(ctx, conv, req.Manual, compCtx)
│   ├── Output: CompactionResult{Summary, RecoveredFiles, OriginalTokens, CompactedTokens}
│
├── [5.6] Build compacted messages
│   ├── Call: compactedMessages := a.compactionService.BuildCompactedMessagesWithContext(result, compCtx)
│   ├── Logic: Create single user message with summary + files
│   ├── Output: []*conversation.Message (1 message)
│
├── [5.7] Create new conversation
│   ├── Call: newConvID, err := a.sdk.CreateCompactedConversation(ctx, a.currentConvID, compactedMessages)
│   ├── Logic: Create new conv, add messages, save, archive old
│   ├── Output: newConvID = "conv-xyz789"
│
├── [5.8] Update UI messages
│   ├── Clear: a.messages = nil
│   ├── Loop through compactedMessages
│   ├── Append to a.messages as Message{Role, Content}
│   └── Output: a.messages updated
│
└── Output: result, compCtx, nil
    |
    v
[STEP 6: CompactWithContext] (sdk/compaction/compaction.go:233-301)
├── Input: conv, manual, compCtx
│
├── [6.1] Validate SummarizeFunc exists
│   ├── Check: s.config.SummarizeFunc != nil
│   ├── If false: return error
│
├── [6.2] Set defaults
│   ├── If compCtx == nil: create default
│   ├── If compCtx.Strategy == "": set to StrategyStandard
│
├── [6.3] Log start
│   ├── Printf: conv.ID, message count, tokens, manual, strategy
│
├── [6.4] Create result struct
│   ├── result := &CompactionResult{OriginalTokens: conv.TotalTokens}
│
├── [6.5] Get compression prompt
│   ├── compressionPrompt := CompressionPrompt (constant)
│   ├── This is the 4-section prompt
│
├── [6.6] Call LLM for summary
│   ├── Call: summary, err := s.config.SummarizeFunc(ctx, conv.Messages, compressionPrompt)
│   ├── If error: set result.Error, return result (graceful failure)
│   ├── Output: summary = "**What We Were Working On**\n..."
│
├── [6.7] Add summary prefix
│   ├── result.Summary = SummaryPrefix + summary
│   ├── SummaryPrefix = "# Previous Session Summary\n\n..."
│   ├── result.Compacted = true
│
├── [6.8] Recover files based on strategy
│   ├── Switch on compCtx.Strategy:
│   │   ├── StrategyMinimal: recoverFilesLimited(3)
│   │   ├── StrategyComprehensive: recoverFilesWithContext(compCtx)
│   │   └── default: recoverFiles()
│   ├── For each file:
│   │   ├── Read file content
│   │   ├── Estimate tokens
│   │   ├── Truncate if > MaxTokensPerFile
│   │   └── Add to RecoveredFiles[]
│   ├── Output: result.RecoveredFiles = [{Path, Content, Tokens, Truncated}]
│
├── [6.9] Estimate compacted token count
│   ├── summaryTokens := EstimateTokens(result.Summary)
│   ├── Loop: fileTokens += each file's tokens
│   ├── result.CompactedTokens = summaryTokens + fileTokens + 100
│
└── Output: result, nil
    |
    v
[STEP 7: BuildCompactedMessagesWithContext] (sdk/compaction/compaction.go:688-735)
├── Input: result, compCtx
│
├── [7.1] Create content builder
│   ├── var content strings.Builder
│
├── [7.2] Write summary
│   ├── content.WriteString(result.Summary)
│   ├── This includes SummaryPrefix + LLM summary
│
├── [7.3] Add recovered files
│   ├── If len(result.RecoveredFiles) > 0:
│   │   ├── content.WriteString("\n\n---\n\n## Files for Context\n\n")
│   │   └── For each file:
│   │       ├── Write: "### <path> [truncated]?\n\n```\n<content>\n```\n\n"
│
├── [7.4] Create user message
│   ├── combinedText := content.String()
│   ├── userTokens := EstimateTokens(combinedText)
│   ├── Create Message with:
│   │   ├── ID: uuid.New().String()
│   │   ├── Timestamp: now
│   │   ├── Role: RoleUser
│   │   ├── Content: combinedText
│   │   └── Tokens: {Input: userTokens, Total: userTokens}
│
└── Output: []*conversation.Message (1 message)
    |
    v
[STEP 8: CreateCompactedConversation] (sdk_integration.go:4408-4469)
├── Input: oldConvID, messages
│
├── [8.1] Create new conversation
│   ├── Call: conv, err := sdk.manager.Create(ctx, CreateOptions{Mode: "chat"})
│   ├── Output: conv with new ID
│
├── [8.2] Add messages and track tokens
│   ├── totalTokens := 0
│   ├── For each message:
│   │   ├── Call: sdk.manager.AddMessage(ctx, conv.ID, msg)
│   │   ├── Accumulate: totalTokens += msg.Tokens.Total (or estimate)
│
├── [8.3] Update conversation token counts
│   ├── Reload: updatedConv := sdk.manager.Resume(ctx, conv.ID)
│   ├── Set: updatedConv.TotalTokens = totalTokens
│   ├── Set: updatedConv.CurrentContextSize = totalTokens
│   ├── Save: sdk.manager.Save(ctx, updatedConv)
│
├── [8.4] Archive old conversation
│   ├── Call: sdk.manager.Archive(ctx, oldConvID)
│
└── Output: conv.ID (new conversation ID)
    |
    v
[STEP 9: Completion Handling] (app.go:2780-2845)
├── Input: msg = CompactCompletedMsg
│
├── [9.1] Reset UI state
│   ├── Set: a.isCompacting = false
│   ├── Call: a.loadingIndicator.Stop(a.animationClock)
│   ├── Set: a.loadingIndicator.SetText("Agent is thinking")
│
├── [9.2] Switch conversation
│   ├── Set: a.currentConvID = msg.NewConvID
│
├── [9.3] Calculate reduction
│   ├── reduction := 100 - (msg.CompactedTokens * 100 / msg.OriginalTokens)
│
├── [9.4] Build system message
│   ├── sysMsg := "◆ Compacted: X → Y tokens (Z% reduction) | N files recovered"
│
├── [9.5] Add to messages
│   ├── Append: a.messages = append(a.messages, Message{Role: "system", Content: sysMsg})
│
├── [9.6] Show notification
│   ├── Call: a.addNotification("success", "Context compacted: ...")
│
├── [9.7] Update token count
│   ├── Call: a.tokenCount = a.sdk.GetConversationTokens(ctx, a.currentConvID)
│
├── [9.8] Rebuild viewport
│   ├── Call: a.invalidateViewportCache()
│   ├── Call: a.updateViewportContent()
│
└── Output: Updated UI
```

### Part 3: SwarmCode Flow (Granular Breakdown)

```
User types: "/compact"
    |
    v
[STEP 1: Command Execution] (commands/compact.ts:115-181)
├── Input: args (ignored), context
│
├── [1.1] Get current messages
│   ├── Call: messages = getMessagesGetter()()
│   ├── Output: Message[] (all conversation messages)
│
├── [1.2] Create compression request
│   ├── Call: summaryRequest = createUserMessage(COMPRESSION_PROMPT)
│   ├── COMPRESSION_PROMPT = 8-section structured prompt
│   ├── Output: UserMessage with 8-section prompt
│
├── [1.3] Call LLM for summary
│   ├── Normalize: normalizeMessagesForAPI([...messages, summaryRequest])
│   ├── Call: summaryResponse = await queryLLM(
│   │       normalizedMessages,
│   │       ["You are a helpful AI assistant..."],
│   │       0,  // maxThinkingTokens
│   │       tools,
│   │       signal,
│   │       {safeMode: false, model: 'main', prependCLISysprompt: true}
│   │   )
│   ├── Output: AssistantMessage with summary content
│
├── [1.4] Extract summary text
│   ├── content = summaryResponse.message.content
│   ├── If string: summary = content
│   ├── Else if array: summary = content[0].text
│   ├── Else: summary = null
│
├── [1.5] Validate summary
│   ├── If !summary: throw Error
│   ├── If summary.startsWith(API_ERROR_MESSAGE_PREFIX): throw Error
│
├── [1.6] Reset token usage (cosmetic)
│   ├── Set: summaryResponse.message.usage = {
│   │       input_tokens: 0,
│   │       output_tokens: original output_tokens,
│   │       cache_creation_input_tokens: 0,
│   │       cache_read_input_tokens: 0
│   │   }
│
├── [1.7] Clear terminal
│   ├── Call: await clearTerminal()
│   ├── Writes: '\x1b[2J\x1b[3J\x1b[H'
│
├── [1.8] Clear messages
│   ├── Call: getMessagesSetter()([])
│   ├── Sets React state to empty array
│
├── [1.9] Fork conversation
│   ├── Call: setForkConvoWithMessagesOnTheNextRender([
│   │       createUserMessage("Context has been compressed..."),
│   │       summaryResponse
│   │   ])
│   ├── Schedules new messages for next render
│
├── [1.10] Clear caches
│   ├── Call: getContext.cache.clear?.()
│   ├── Call: getCodeStyle.cache.clear?.()
│
├── [1.11] Reset services
│   ├── Call: resetFileFreshnessSession()
│   ├── Call: resetReminderSession()
│
└── Output: '' (empty string return)
```

### Part 4: Key Differences (Behavior-Level)

Let me identify exactly what behaviors differ:

| Aspect | SwarmCode | SwarmOS | Must Change? |
|--------|-----------|---------|--------------|
| **Prompt** | 8-section (600 words) | 4-section (150 words) | ✅ YES |
| **File Recovery** | None (manual compact) | 3-8 files with content | ✅ YES (remove/disable) |
| **Token Reset** | Cosmetic (set to 0) | Accurate tracking | ⚠️ MAYBE (keep accurate) |
| **Terminal Clear** | Yes (ANSI codes) | No (smooth transition) | ⚠️ MAYBE (add option) |
| **Message Structure** | 2 messages: user + assistant | 1 message: user only | ✅ YES |
| **Conversation Persistence** | No (in-memory fork) | Yes (new conv, archive old) | ⚠️ NO (keep persistence) |
| **Background Execution** | Blocking | Non-blocking | ⚠️ NO (keep non-blocking) |
| **UI Feedback** | None | Loading + notifications | ⚠️ NO (keep feedback) |
| **Strategy Options** | None | 3 strategies | ✅ YES (remove) |
| **CompactionContext** | N/A | Mode, todos, MCP, etc. | ⚠️ MAYBE (keep but don't use) |

### Part 5: Required Changes (Granular)

#### Change 1: Replace the Compression Prompt

**File**: `sdk/compaction/prompt.go`  
**Line**: 17-44  
**Current**:
```go
const CompressionPrompt = `You are summarizing a conversation to create a handoff for the next session.

Your summary should be INTELLIGENT and CONCISE. Focus on what matters:

1. **What We Were Working On**
   - The main task or goal
   - The specific problem being solved

2. **What Was Done**
   - Files modified or created (with paths)
   - Key code changes or implementations
   - Commands run and their results
   - Decisions made and why

3. **Current State**
   - What is working now
   - What is partially done
   - Any errors or issues encountered

4. **What's Next**
   - Immediate next step
   - Remaining tasks

Be specific with file paths, function names, and error messages.
Skip pleasantries and filler - just the essential information.
The next assistant will use this summary to continue the work.`
```

**New** (SwarmCode's 8-section):
```go
const CompressionPrompt = `Please provide a comprehensive summary of our conversation structured as follows:

## Technical Context
Development environment, tools, frameworks, and configurations in use. Programming languages, libraries, and technical constraints. File structure, directory organization, and project architecture.

## Project Overview  
Main project goals, features, and scope. Key components, modules, and their relationships. Data models, APIs, and integration patterns.

## Code Changes
Files created, modified, or analyzed during our conversation. Specific code implementations, functions, and algorithms added. Configuration changes and structural modifications.

## Debugging & Issues
Problems encountered and their root causes. Solutions implemented and their effectiveness. Error messages, logs, and diagnostic information.

## Current Status
What we just completed successfully. Current state of the codebase and any ongoing work. Test results, validation steps, and verification performed.

## Pending Tasks
Immediate next steps and priorities. Planned features, improvements, and refactoring. Known issues, technical debt, and areas needing attention.

## User Preferences
Coding style, formatting, and organizational preferences. Communication patterns and feedback style. Tool choices and workflow preferences.

## Key Decisions
Important technical decisions made and their rationale. Alternative approaches considered and why they were rejected. Trade-offs accepted and their implications.

Focus on information essential for continuing the conversation effectively, including specific details about code, files, errors, and plans.`
```

**Logic Change**:
- Replace entire constant
- No conditional logic needed
- Affects all strategies uniformly

**Validation**:
- Run `/compact` and check summary structure
- Should see 8 section headers in output

---

#### Change 2: Disable File Recovery

**File**: `sdk/compaction/compaction.go`  
**Function**: `CompactWithContext`  
**Lines**: 278-289  

**Current Logic**:
```go
// Recover files based on strategy - always recover files intelligently
switch compCtx.Strategy {
case StrategyMinimal:
    // Minimal: fewer files
    result.RecoveredFiles = s.recoverFilesLimited(3)
case StrategyComprehensive:
    // Comprehensive: more files
    result.RecoveredFiles = s.recoverFilesWithContext(compCtx)
default:
    // Standard: intelligent file selection
    result.RecoveredFiles = s.recoverFiles()
}
```

**New Logic** (Option A: Disable entirely):
```go
// File recovery disabled - summary only approach (like SwarmCode)
result.RecoveredFiles = nil
```

**New Logic** (Option B: Make opt-in):
```go
// Only recover files if explicitly requested
if compCtx.IncludeFiles {
    result.RecoveredFiles = s.recoverFiles()
} else {
    result.RecoveredFiles = nil
}
```

**Supporting Change** (if Option B):
**File**: `sdk/compaction/compaction.go`  
**Struct**: `CompactionContext`  
**Add Field**:
```go
type CompactionContext struct {
    // ... existing fields ...
    
    // IncludeFiles indicates whether to recover file content
    IncludeFiles bool
}
```

**Command Change** (if Option B):
**File**: `internal/chat/commands/compact.go`  
**Add Flag Parsing**:
```go
func (c *CompactCommand) Execute(args []string) tea.Cmd {
    strategy := compaction.StrategyStandard
    includeFiles := false  // Default to false (SwarmCode behavior)
    
    for _, arg := range args {
        argLower := strings.ToLower(arg)
        if argLower == "--with-files" || argLower == "-f" {
            includeFiles = true
        }
        // ... existing strategy parsing ...
    }
    
    return func() tea.Msg {
        return CompactRequestMsg{
            Manual:       true,
            Strategy:     strategy,
            IncludeFiles: includeFiles,  // New field
        }
    }
}
```

**Validation**:
- Run `/compact` → should have no files
- Run `/compact --with-files` → should have files (if Option B)
- Check token count → should be much lower

---

#### Change 3: Remove Strategy Complexity

**File**: `sdk/compaction/prompt.go`  
**Lines**: 6-15  

**Current**:
```go
type CompactionStrategy string

const (
    StrategyMinimal       CompactionStrategy = "minimal"
    StrategyStandard      CompactionStrategy = "standard"
    StrategyComprehensive CompactionStrategy = "comprehensive"
)
```

**Decision**: Keep the type but simplify usage

**File**: `internal/chat/commands/compact.go`  
**Function**: `Execute`  

**Current Logic**:
```go
strategy := compaction.StrategyStandard

if len(args) > 0 {
    arg := strings.ToLower(args[0])
    switch arg {
    case "minimal", "min", "quick":
        strategy = compaction.StrategyMinimal
    case "full", "comprehensive", "verbose", "all":
        strategy = compaction.StrategyComprehensive
    case "standard", "default", "normal":
        strategy = compaction.StrategyStandard
    }
}
```

**New Logic** (Simplified):
```go
// Always use standard strategy (ignore user input)
// Strategy is now effectively unused since file recovery is disabled
strategy := compaction.StrategyStandard
```

**Alternative**: Remove strategy parsing entirely and pass empty string

**Validation**:
- Run `/compact minimal` → same result as `/compact`
- Run `/compact full` → same result as `/compact`
- Strategy has no effect since files aren't recovered

---

#### Change 4: Adjust Message Structure

**File**: `sdk/compaction/compaction.go`  
**Function**: `BuildCompactedMessagesWithContext`  
**Lines**: 688-735  

**Current Behavior**:
- Creates 1 user message
- Content = Summary + Recovered Files

**Desired Behavior** (SwarmCode):
- Creates 2 messages:
  1. User message: "Context has been compressed..."
  2. Assistant message: The summary

**Current Code**:
```go
func (s *Service) BuildCompactedMessagesWithContext(result *CompactionResult, compCtx *CompactionContext) []*conversation.Message {
    var messages []*conversation.Message
    now := time.Now()
    
    var content strings.Builder
    
    // Summary from LLM
    content.WriteString(result.Summary)
    
    // Add recovered files (WILL BE EMPTY NOW)
    if len(result.RecoveredFiles) > 0 {
        content.WriteString("\n\n---\n\n")
        content.WriteString("## Files for Context\n\n")
        for _, file := range result.RecoveredFiles {
            // ... file formatting ...
        }
    }
    
    // Create the user message
    combinedText := content.String()
    userTokens := EstimateTokens(combinedText)
    
    userMessage := &conversation.Message{
        ID:        uuid.New().String(),
        Timestamp: now,
        Role:      conversation.RoleUser,
        Content:   combinedText,
        Tokens: &conversation.TokenUsage{
            Input: userTokens,
            Total: userTokens,
        },
    }
    messages = append(messages, userMessage)
    
    return messages
}
```

**New Code** (SwarmCode structure):
```go
func (s *Service) BuildCompactedMessagesWithContext(result *CompactionResult, compCtx *CompactionContext) []*conversation.Message {
    var messages []*conversation.Message
    now := time.Now()
    
    // Message 1: User notification message
    notificationText := "Context has been compressed using structured 8-section algorithm. All essential information has been preserved for seamless continuation."
    notificationTokens := EstimateTokens(notificationText)
    
    userMessage := &conversation.Message{
        ID:        uuid.New().String(),
        Timestamp: now,
        Role:      conversation.RoleUser,
        Content:   notificationText,
        Tokens: &conversation.TokenUsage{
            Input: notificationTokens,
            Total: notificationTokens,
        },
    }
    messages = append(messages, userMessage)
    
    // Message 2: Assistant summary message
    // Use the summary directly (already has SummaryPrefix from CompactWithContext)
    summaryTokens := EstimateTokens(result.Summary)
    
    assistantMessage := &conversation.Message{
        ID:        uuid.New().String(),
        Timestamp: now.Add(1 * time.Millisecond), // Slightly after user message
        Role:      conversation.RoleAssistant,
        Content:   result.Summary,
        Tokens: &conversation.TokenUsage{
            Output: summaryTokens,
            Total:  summaryTokens,
        },
    }
    messages = append(messages, assistantMessage)
    
    return messages
}
```

**Logic Changes**:
1. Create 2 messages instead of 1
2. First message (user): notification text
3. Second message (assistant): the summary
4. No file content appended

**Validation**:
- After `/compact`, conversation should have 2 messages
- First message role should be "user"
- Second message role should be "assistant"
- No file content blocks

---

#### Change 5: Update SummaryPrefix

**File**: `sdk/compaction/prompt.go`  
**Lines**: 50-57  

**Current**:
```go
const SummaryPrefix = `# Previous Session Summary

The following is a summary of our previous work session. Continue from where we left off.

---

`
```

**Analysis**: This prefix is fine for the assistant message. Keep as is.

**Validation**: Check that summary starts with "# Previous Session Summary"

---

#### Change 6: Adjust Token Estimation

**File**: `sdk/compaction/compaction.go`  
**Function**: `CompactWithContext`  
**Lines**: 291-298  

**Current**:
```go
// Estimate compacted token count
summaryTokens := EstimateTokens(result.Summary)
fileTokens := 0
for _, f := range result.RecoveredFiles {
    fileTokens += f.Tokens
}

result.CompactedTokens = summaryTokens + fileTokens + 100 // +100 for formatting overhead
```

**New** (No files):
```go
// Estimate compacted token count
// Message 1: notification text (~25 tokens)
notificationTokens := EstimateTokens("Context has been compressed using structured 8-section algorithm. All essential information has been preserved for seamless continuation.")
// Message 2: summary
summaryTokens := EstimateTokens(result.Summary)

result.CompactedTokens = notificationTokens + summaryTokens + 50 // +50 for message overhead
```

**Validation**:
- CompactedTokens should be accurate
- Should match what's actually in the new conversation

---

#### Change 7: Optional Terminal Clear

This is a nice-to-have, not required for SwarmCode parity.

**File**: `internal/chat/app.go`  
**Location**: After switching to new conversation  

**Add** (in CompactCompletedMsg handler, around line 2786):
```go
case commands.CompactCompletedMsg:
    // ... existing code ...
    
    // Optional: Clear terminal for "clean slate" feeling
    // TODO: Make this configurable via settings
    // clearTerminal()
    
    // ... rest of handler ...
```

**Separate Function** (terminal utility):
**File**: Create `internal/chat/terminal.go`  
```go
package chat

import (
    "fmt"
    "os"
)

// clearTerminal clears the terminal screen and scrollback
func clearTerminal() {
    // ANSI escape sequences:
    // \x1b[2J - Clear entire screen
    // \x1b[3J - Clear scrollback buffer
    // \x1b[H  - Move cursor to home (0,0)
    fmt.Print("\x1b[2J\x1b[3J\x1b[H")
    os.Stdout.Sync()
}
```

**Validation**:
- If enabled, terminal should clear after compact
- If disabled, smooth transition (current behavior)

---

### Part 6: Implementation Sequence

Let me order these changes logically:

```
PHASE 1: PROMPT CHANGES (Zero Risk, High Impact)
├── 1.1 Replace CompressionPrompt with 8-section version
│   ├── File: sdk/compaction/prompt.go
│   ├── Change: Replace constant value
│   ├── Test: Run /compact, check summary structure
│   └── Impact: Better summaries immediately
│
└── 1.2 Update SummarizationSystemPrompt if needed
    ├── File: sdk/compaction/prompt.go
    ├── Current: "You create clear, actionable summaries..."
    ├── SwarmCode: "You are a helpful AI assistant tasked with creating comprehensive conversation summaries..."
    └── Decision: Update to match SwarmCode

PHASE 2: FILE RECOVERY CHANGES (Medium Risk, High Impact)
├── 2.1 Disable file recovery
│   ├── File: sdk/compaction/compaction.go
│   ├── Function: CompactWithContext
│   ├── Change: Set result.RecoveredFiles = nil
│   ├── Test: Run /compact, verify no files in output
│   └── Impact: Token savings, simpler output
│
└── 2.2 Update token estimation
    ├── File: sdk/compaction/compaction.go
    ├── Function: CompactWithContext
    ├── Change: Remove fileTokens calculation
    └── Test: Verify CompactedTokens is accurate

PHASE 3: MESSAGE STRUCTURE CHANGES (Medium Risk, High Impact)
├── 3.1 Update BuildCompactedMessagesWithContext
│   ├── File: sdk/compaction/compaction.go
│   ├── Change: Create 2 messages (user + assistant) instead of 1
│   ├── Test: Verify conversation has 2 messages after compact
│   └── Impact: Matches SwarmCode structure
│
└── 3.2 Verify message rendering
    ├── Check: UI displays both messages correctly
    ├── Check: User message shows notification
    └── Check: Assistant message shows summary

PHASE 4: SIMPLIFICATION (Low Risk, Low Impact)
├── 4.1 Simplify strategy handling
│   ├── File: internal/chat/commands/compact.go
│   ├── Change: Always use StrategyStandard
│   ├── Test: Verify /compact minimal works same as /compact
│   └── Impact: Less cognitive load
│
└── 4.2 Remove strategy parsing logic
    ├── Option: Keep parsing but ignore
    ├── Option: Remove parsing entirely
    └── Decision: Keep parsing, ignore for now (minimal change)

PHASE 5: OPTIONAL ENHANCEMENTS (Low Priority)
├── 5.1 Add terminal clear option
│   ├── File: internal/chat/terminal.go (new)
│   ├── File: internal/chat/app.go
│   ├── Change: Add clearTerminal() call
│   └── Impact: "Clean slate" feeling (optional)
│
└── 5.2 Add configuration flag
    ├── Settings: Add "ClearTerminalOnCompact" boolean
    └── Use: if settings.ClearTerminalOnCompact { clearTerminal() }
```

---

### Part 7: Testing & Validation Plan

For each change, we need to validate:

```
TEST 1: Basic Compaction
├── Action: Run /compact in long conversation
├── Check: Summary is generated
├── Check: Summary has 8 section headers
├── Check: No file content blocks
└── Check: Conversation now has 2 messages

TEST 2: Token Accuracy
├── Before: Note original token count (e.g., 150,000)
├── After: Note compacted token count
├── Check: CompactedTokens matches actual conversation tokens
└── Expected: ~2,500-5,000 tokens (summary only)

TEST 3: Summary Quality
├── Check: All 8 sections are present
├── Check: Technical Context section exists
├── Check: User Preferences section exists
├── Check: Key Decisions section exists
└── Compare: To SwarmCode's output quality

TEST 4: Continuation
├── After /compact, ask: "Continue where we left off"
├── Check: LLM references summary correctly
├── Check: LLM knows technical context
└── Check: LLM knows user preferences

TEST 5: Message Structure
├── Check: Message 1 is role "user"
├── Check: Message 1 content is notification
├── Check: Message 2 is role "assistant"
└── Check: Message 2 content is summary

TEST 6: Conversation Persistence
├── Run: /compact
├── Action: Restart app
├── Check: Conversation still exists
└── Check: Summary is still there

TEST 7: Strategy Ignored
├── Run: /compact minimal
├── Run: /compact standard
├── Run: /compact full
└── Check: All produce identical results

TEST 8: Error Handling
├── Simulate: LLM error during summary generation
├── Check: Error message displayed
├── Check: Old conversation preserved
└── Check: UI state recovered

TEST 9: Token Reduction
├── Before: 150,000 tokens
├── After: ~3,000 tokens
└── Reduction: ~98% (vs SwarmOS current ~93% with files)
```

---

### Part 8: Risk Analysis

| Change | Risk Level | Rollback Strategy |
|--------|-----------|-------------------|
| Prompt replacement | Low | Revert constant value |
| Disable file recovery | Low | Re-enable file recovery logic |
| Message structure | Medium | Revert to 1-message structure |
| Strategy simplification | Low | Re-enable strategy switching |
| Terminal clear | Low | Remove clearTerminal() call |

**Overall Risk**: Low  
**Why**: Changes are mostly in prompt and configuration, not core logic

---

### Part 9: Success Criteria

The implementation is successful when:

1. ✅ `/compact` produces 8-section summaries
2. ✅ No file content in compacted conversations
3. ✅ 2 messages in compacted conversation (user + assistant)
4. ✅ Token reduction is ~98% (vs current ~93%)
5. ✅ Summary quality matches SwarmCode
6. ✅ Continuation works as well as SwarmCode
7. ✅ User preferences are captured in summary
8. ✅ Key decisions are captured in summary
9. ✅ Code is simpler (file recovery disabled)
10. ✅ All existing tests pass

---

## Summary: What We're Doing & Why

**GOAL**: Make SwarmOS `/compact` work like SwarmCode's proven approach

**KEY CHANGES**:
1. **Better Prompt** - 8 sections instead of 4 (captures user preferences, decisions)
2. **No File Recovery** - Summary only (98% token reduction vs 93%)
3. **2-Message Structure** - User notification + Assistant summary (matches SwarmCode)
4. **Simpler Code** - Remove file recovery complexity

**WHY**:
- SwarmCode's approach produces better summaries in practice
- File recovery wastes tokens and dilutes summary
- 8-section prompt captures critical information (preferences, decisions)
- Simpler is better for maintainability

**IMPACT**:
- Better continuation quality
- Higher token reduction
- Simpler codebase
- Proven approach from SwarmCode

