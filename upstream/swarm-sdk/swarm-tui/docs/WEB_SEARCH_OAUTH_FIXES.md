# Anthropic Web Search OAuth Integration Fixes

## Summary of Changes

All 5 critical issues with the web search tool's OAuth integration have been fixed to match the patterns and best practices used in the main Anthropic provider implementation.

---

## Issue 1: Hard-coded Beta Flags ✅ FIXED

### Problem
- `NewClient()` was hard-coding `BetaClaudeCode` and `BetaInterleavedThinking` in the default betas list
- These flags are not necessary for web search functionality
- The main provider implementation doesn't hard-code unnecessary betas

### Solution
**File: `sdk/tools/anthropic_web_search/web_search.go`**
- Changed `NewClient()` to initialize betas with an empty slice: `make([]BetaFlag, 0)`
- Only add betas as needed via `ClientOption` functions
- This matches the pattern in `sdk/provider/anthropic/provider.go`

```go
// Before
betas: []BetaFlag{
    BetaClaudeCode,
    BetaInterleavedThinking,
},

// After
betas: make([]BetaFlag, 0),  // Start with empty betas, add only what's needed
```

---

## Issue 2: Missing OAuth Beta Constant ✅ FIXED

### Problem
- `WithOAuth()` function was checking for hard-coded string `"oauth-2025-04-20"`
- Beta flag was being created with `BetaFlag("oauth-2025-04-20")` each time
- No named constant existed for the OAuth beta flag
- Inconsistent with the main provider which uses constants

### Solution
**File: `sdk/tools/anthropic_web_search/web_search.go`**
- Added `BetaOAuth` constant: `BetaFlag = "oauth-2025-04-20"`
- Updated `WithOAuth()` to use the constant instead of string literals
- Now matches pattern in `sdk/provider/anthropic/oauth_tools.go`

```go
// Added constant
const (
    BetaOAuth BetaFlag = "oauth-2025-04-20"  // REQUIRED for OAuth tokens
    // ...
)

// Updated WithOAuth function
if b == BetaOAuth {  // Using constant instead of string
    hasBeta = true
    break
}
```

---

## Issue 3: Missing User-Agent Header ✅ FIXED

### Problem
- Web search tool was NOT setting the `User-Agent` header for OAuth requests
- Main provider in `chat.go` L110 explicitly sets: `headers["User-Agent"] = OAuthUserAgent`
- Missing header could cause OAuth routing issues on Anthropic's infrastructure

### Solution
**File: `sdk/tools/anthropic_web_search/web_search.go`**
- Added User-Agent header in `SendMessage()` method when OAuth is enabled
- Uses the standard Claude CLI user agent: `"claude-cli/2.1.2 (external, cli)"`
- Matches pattern in `sdk/provider/anthropic/oauth_tools.go` L18

```go
// In SendMessage() when OAuth is enabled
if c.isOAuth {
    httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
    httpReq.Header.Set("x-app", "cli")
    // User-Agent is required for OAuth requests for proper routing
    httpReq.Header.Set("User-Agent", "claude-cli/2.1.2 (external, cli)")
}
```

---

## Issue 4: Hard-coded Model ✅ FIXED

### Problem
- `tool.go` Execute() method was hard-coding the model to `"claude-sonnet-4-20250514"`
- No way to use a different model for web searches
- Ignores any model configuration from the caller
- Main provider allows model to be specified in the request

### Solution
**File: `sdk/tools/anthropic_web_search/web_search.go` & `tool.go`**

**Step 1: Add model field to Client struct**
```go
type Client struct {
    // ...
    model string // Model to use for web search requests
}
```

**Step 2: Add WithModel() ClientOption**
```go
// WithModel sets the model to use for web search requests
// If not specified, defaults to claude-sonnet-4-20250514
func WithModel(model string) ClientOption {
    return func(c *Client) {
        if model != "" {
            c.model = model
        }
    }
}
```

**Step 3: Update NewClient() to set default model**
```go
model: "claude-sonnet-4-20250514", // Default model for web search
```

**Step 4: Add NewWithModel() constructor to Tool**
```go
// NewWithModel creates a new Anthropic Web Search tool with custom model
func NewWithModel(config WebSearchConfig, model string) *Tool {
    return &Tool{
        config: config,
        model:  model,
    }
}
```

**Step 5: Update Tool.Execute() to pass model to client**
```go
clientOpts := []ClientOption{
    WithOAuth(true),
    WithWebSearch(t.config),
    WithBetas(BetaWebSearch),
}

// Use specified model if provided
if t.model != "" {
    clientOpts = append(clientOpts, WithModel(t.model))
}

client := NewClient(token.AccessToken, clientOpts...)

// Determine the model to use
modelToUse := client.model
if modelToUse == "" {
    modelToUse = "claude-sonnet-4-20250514" // Fallback
}

req := &APIRequest{
    Model: modelToUse,
    // ...
}
```

---

## Issue 5: x-app Header Handling ✅ VERIFIED

### Status: Already Correct
- The `x-app` header is properly set in `SendMessage()` when OAuth is enabled
- This was already correct in the original implementation (L384)
- No changes needed for this issue

```go
if c.isOAuth {
    httpReq.Header.Set("x-app", "cli") // CLI identifier for OAuth
}
```

---

## Constants Added/Updated

**File: `sdk/tools/anthropic_web_search/web_search.go`**

```go
const (
    // OAuth-related betas (CRITICAL for OAuth token authentication)
    BetaOAuth               BetaFlag = "oauth-2025-04-20"       // REQUIRED for OAuth tokens
    BetaInterleavedThinking BetaFlag = "interleaved-thinking-2025-05-14"
    
    // Context and feature betas
    BetaContext1M           BetaFlag = "context-1m-2025-08-07"  // Used in getContextWindow for 1M context detection

    // Tool-related betas
    BetaWebSearch       BetaFlag = "web-search-2025-03-05"      // KEY: Enables server-side web search
    BetaToolExamples    BetaFlag = "tool-examples-2025-10-29"
    BetaAdvancedToolUse BetaFlag = "advanced-tool-use-2025-11-20"
    BetaToolSearch      BetaFlag = "tool-search-tool-2025-10-19"
)
```

---

## Testing Results

✅ All existing tests pass:
- `TestToolName`
- `TestToolDescription`
- `TestToolParameters`
- `TestToolValidate` (all 7 sub-tests)
- `TestToolIsIdempotent`
- `TestToolRequiresPermission`
- `TestToolSupportedContentTypes`
- `TestToolOptimizationHints`
- `TestNewWithConfig`
- `TestDefaultWebSearchConfig`
- `TestGetWebSearchTools`

**Build Status:** ✅ Successful with no errors

---

## API Compatibility

The changes maintain 100% backward compatibility:

- `New()` - Still works as before
- `NewWithConfig()` - Still works as before
- New: `NewWithModel()` - Optional new constructor
- `NewClient()` - Same signature, improved internals
- All ClientOption functions work as before

---

## OAuth Header Configuration Summary

The web search tool now correctly configures OAuth requests with:

| Header | Value | Required | Source |
|--------|-------|----------|--------|
| Authorization | Bearer {oauth_token} | Yes (OAuth only) | L388 |
| x-app | "cli" | Yes (OAuth only) | L389 |
| User-Agent | "claude-cli/2.1.2 (external, cli)" | Yes (OAuth only) | L391 |
| anthropic-beta | "oauth-2025-04-20,web-search-2025-03-05" | Yes | L396-397 |
| Content-Type | "application/json" | Yes | L386 |
| anthropic-version | "2023-06-01" | Yes | L395 |

This matches the pattern in `sdk/provider/anthropic/chat.go` L40-114 exactly.

---

## Files Modified

1. `sdk/tools/anthropic_web_search/web_search.go`
   - Simplified beta constants (removed unused ones, added BetaOAuth)
   - Added `model` field to Client struct
   - Added `WithModel()` ClientOption
   - Updated `WithOAuth()` to use BetaOAuth constant
   - Updated `NewClient()` to use empty betas slice
   - Added User-Agent header in SendMessage()

2. `sdk/tools/anthropic_web_search/tool.go`
   - Added `model` field to Tool struct
   - Updated `New()` and `NewWithConfig()` to initialize model field
   - Added `NewWithModel()` constructor
   - Updated Execute() to pass model to client via WithModel option
   - Added logic to use client's model in APIRequest

---

## Verification Commands

```bash
# Build the web search command
go build -o /tmp/test-build ./cmd/anthropic-web-search

# Run tests
go test github.com/Swarm-Code/mono/swarm-sdk/tools/anthropic_web_search -v
```

All commands complete successfully with no errors.
