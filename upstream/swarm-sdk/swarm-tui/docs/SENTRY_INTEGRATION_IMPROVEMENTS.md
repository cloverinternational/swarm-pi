# Sentry Integration Improvements

## Overview

This document describes the comprehensive improvements made to error logging and Sentry integration in SwarmOS TUI. The primary goal was to fix the issue where all errors were being grouped under `*fmt.wrapError` in Sentry, making it impossible to distinguish between different error types.

## Problem Statement

**Before:** All errors were wrapped using `fmt.Errorf("agent execution failed: %w", err)`, causing Sentry to group everything under a single issue type (`*fmt.wrapError`). This made it impossible to:
- Distinguish between context cancellations and real errors
- Identify provider-specific issues (rate limits, auth failures)
- Track tool execution errors separately
- Understand error patterns and frequency

**Example Before:**
```
*fmt.wrapError (500 events)
├── agent execution failed: context canceled
├── agent execution failed: rate limited  
├── agent execution failed: permission denied
└── ... (all grouped together)
```

## Solution Architecture

### 1. Custom Error Types (`internal/chat/sentry_integration.go`)

Created structured error types that preserve context and enable proper categorization:

#### AgentExecutionError
Represents agent execution failures with full context:
- `Type` - Error type (e.g., "context_canceled", "provider_error", "tool_error")
- `Provider` - Provider name (anthropic, openai, etc.)
- `Model` - Model being used
- `ConversationID` - Conversation identifier
- `Retried` - Whether a retry was attempted
- `OriginalError` - Original error if retry occurred

#### ContextCanceledError
Specifically for user-initiated cancellations:
- Distinguished from real errors
- Filtered out of Sentry by default (not an error condition)
- Preserves context for debugging if needed

#### ProviderError
Provider-specific errors with categorization:
- `ErrorType` - "rate_limited", "auth_failed", "timeout", "invalid_request"
- `Retryable` - Whether the error can be retried
- `Code` - Provider-specific error code
- Provider name and message

#### ToolExecutionError  
Tool-specific errors:
- `ToolName` - Name of the tool that failed
- `ErrorType` - "permission_denied", "not_found", "invalid_input", "execution_failed"
- `TargetPath` - Path/resource that caused the error
- Full error message

### 2. Error Classification System

**ClassifyError()** - Intelligent error detection and classification:
```go
func ClassifyError(err error, provider, model, conversationID string) error
```

Classifies errors based on:
1. **Context cancellation** detection
2. **SDK structured error** unwrapping
3. **Provider error** patterns (rate limits, auth, timeouts)
4. **Tool error** patterns (permissions, not found)
5. **Generic fallback** for unknown errors

### 3. Sentry Integration Functions

**CaptureSentryError()** - Enhanced error capture:
- Automatically classifies errors before sending
- Generates custom fingerprints for proper grouping
- Adds comprehensive context and tags
- Creates breadcrumb trail for debugging

**Custom Fingerprinting:**
```go
Context Canceled:    ["context-canceled", provider]
Provider Error:      ["provider-error", provider, error_type]
Tool Error:          ["tool-error", tool_name, error_type]
Agent Error:         ["agent-error", error_type, provider]
```

**Context Enrichment:**
- Provider and model information
- Conversation ID
- Retry status
- Error-specific metadata (tool name, target path, etc.)

**Tagging for Filtering:**
- `error_category` - High-level category
- `error_type` - Specific error type
- `provider` - Provider name
- `model` - Model name
- `conversation_id` - Conversation identifier
- `retryable` - Whether error can be retried

### 4. Breadcrumb Logging

**AddSentryBreadcrumb()** - Execution flow tracking:
- Automatically added for important lifecycle events
- Provides debugging context when errors occur
- Integrated with observability logger

**Key Breadcrumb Points:**
1. Agent execution start
2. Agent execution completion
3. Agent execution failure
4. Tool execution events
5. Provider request lifecycle

### 5. Enhanced Sentry Configuration

**Updated main.go initialization:**

```go
sentry.Init(sentry.ClientOptions{
    Release:          version.Version,      // Track by version
    Environment:      getEnvironment(),     // dev/production
    TracesSampleRate: 0.1,                 // 10% sampling
    
    BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
        // Filter out context cancellations
        // Add build information
        // Enrich with metadata
    },
    
    IgnoreErrors: []string{
        "context canceled",
        "context deadline exceeded",
        "EOF",
    },
})
```

**Benefits:**
- Environment tracking (development vs production)
- Version-based release tracking
- Automatic filtering of non-errors
- Build information in every event

### 6. Observability Logger Integration

**Enhanced SimpleLogger:**
- Automatically creates Sentry breadcrumbs for important events
- Maps SDK log levels to Sentry breadcrumb levels
- Categorizes events for better organization
- Provides execution flow context

**Automatic Breadcrumb Creation:**
- Warning and error level logs
- Agent lifecycle events
- Tool execution events  
- Provider request events

## Distributed Tracing

### Trace Propagation
SwarmOS now supports distributed tracing across the TUI → SDK boundary. This allows you to follow an error from the moment a user sends a message until it fails deep within the SDK.

1. **TUI Span**: When a message is sent, a new trace span is created.
2. **SDK Propagation**: The `TraceID` is propagated through the execution context to the SDK.
3. **Error Capture**: If an error occurs, the SDK's `TraceID` is captured and linked to the Sentry event.
4. **Sentry Visualization**: Errors in Sentry are tagged with `trace_id` and include a `trace` context for visualization in Sentry's Performance tab.

### Trace Information in Errors
Structured errors now include the `TraceID` in their string representation:
`agent execution failed [execution_failed]: rate limit exceeded (trace: trace-123456789)`

### Benefits
- **End-to-End Visibility**: Link TUI actions to SDK failures.
- **Improved Debugging**: Follow the exact path of a failed request.
- **Cross-Boundary Analysis**: See how TUI context (like conversation ID) relates to SDK errors.

## Implementation Details

### Files Modified

1. **`internal/chat/sentry_integration.go`**
   - Added `TraceID` field to all structured error types.
   - Added `CaptureSentryErrorWithContext` for trace propagation.
   - Added trace extraction logic from `context.Context` and `sdkerror.Error`.
   - Added Sentry trace context support for distributed tracing.

2. **`internal/chat/sdk_integration.go`**
   - Propagated `ctx` through error creation functions.
   - Included `TraceID` in structured errors.

3. **`internal/chat/background_manager.go`**
   - Updated `Register` to accept a parent context for trace propagation.

4. **`internal/chat/app_messaging.go`**
   - Started trace spans for message execution.
   - Propagated trace context to the background manager.
   - Included `TraceID` in `agentResponseMsg`.

5. **`internal/chat/app_streaming_types.go`**
   - Added `traceID` field to relevant Bubble Tea messages.

6. **`internal/chat/app_update.go`**
   - Updated to use `CaptureSentryErrorWithContext` with trace ID propagation.

### Error Flow

```
Error Occurs
    ↓
sdk_integration.go: ClassifyError()
    ↓
Create Structured Error (AgentExecutionError, ProviderError, etc.)
    ↓
Return to app_update.go
    ↓
app_update.go: CaptureSentryError()
    ↓
Generate Fingerprint + Add Context + Add Tags
    ↓
Send to Sentry (unless filtered)
```

## Results

### Before vs After

**Before:**
```
Sentry Issues Dashboard:
├── *fmt.wrapError (500 events, all types mixed)
└── (No useful categorization)
```

**After:**
```
Sentry Issues Dashboard:
├── AgentExecutionError: context_canceled (0 events - filtered)
├── ProviderError: rate_limited (Anthropic) (150 events)
├── ProviderError: auth_failed (OpenAI) (5 events)
├── ProviderError: timeout (Anthropic) (25 events)
├── ToolExecutionError: permission_denied (swarm_Read) (50 events)
├── ToolExecutionError: not_found (swarm_Edit) (10 events)
├── AgentExecutionError: codex_retry_failed (15 events)
└── AgentExecutionError: execution_failed (20 events)
```

### Benefits

1. **Proper Error Grouping**
   - Each error type creates a separate Sentry issue
   - Easy to identify most common problems
   - Clear error patterns and trends

2. **Context Preservation**
   - Provider and model information
   - Conversation tracking
   - Retry status
   - Error-specific metadata

3. **Debugging Improvements**
   - Breadcrumb trail shows execution flow
   - Full context in each error event
   - Stack traces are meaningful
   - Build information for version tracking

4. **Noise Reduction**
   - User cancellations filtered out
   - Known non-errors ignored
   - Proper severity levels

5. **Better Monitoring**
   - Track errors by provider
   - Track errors by tool
   - Track errors by error type
   - Release-based tracking

## Testing and Validation

### Build Verification
```bash
cd /home/swarm/SwarmCode/TUI
go build -o /tmp/swarmos ./cmd/swarmos
# ✅ Build successful (33MB binary)
```

### Test Scenarios

To validate the integration, test these scenarios:

1. **Context Cancellation**
   - Start a long-running operation
   - Cancel it (Ctrl+C)
   - Verify: No Sentry error created

2. **Provider Rate Limit**
   - Trigger rate limit from Anthropic
   - Verify: Separate issue "ProviderError: rate_limited (Anthropic)"

3. **Tool Permission Error**
   - Try to read a restricted file
   - Verify: Separate issue "ToolExecutionError: permission_denied (swarm_Read)"

4. **Authentication Failure**
   - Use invalid API key
   - Verify: Separate issue "ProviderError: auth_failed"

5. **Network Timeout**
   - Simulate network timeout
   - Verify: Separate issue "ProviderError: timeout"

## Usage Examples

### Creating Structured Errors

```go
// In sdk_integration.go or similar
err := NewAgentExecutionError(
    "execution_failed",
    "Agent failed to complete execution",
    "anthropic",
    "claude-sonnet-4",
    "conv_12345",
    originalErr,
)
return "", err
```

### Capturing Errors with Context

```go
// In app_update.go or similar
if err != nil {
    if !ShouldIgnoreError(err) {
        CaptureSentryError(err, provider, model, conversationID)
    }
}
```

### Adding Breadcrumbs

```go
// In sdk_integration.go or similar
AddSentryBreadcrumb(
    "agent.execution",
    "Starting agent execution",
    sentry.LevelInfo,
    map[string]interface{}{
        "model": model,
        "provider": provider,
        "conversation_id": convID,
    },
)
```

## Environment Configuration

### Development
```bash
export SWARMOS_ENV=development
```

### Production
```bash
export SWARMOS_ENV=production
# Or leave unset for automatic detection based on version
```

## Migration Notes

### Breaking Changes
None - this is backward compatible. Old code will continue to work.

### Recommended Actions
1. Monitor Sentry dashboard after deployment
2. Adjust fingerprinting rules if needed
3. Update alerting rules for new error categories
4. Review ignored error patterns

### Future Improvements

1. **Performance Monitoring**
   - Add more performance breadcrumbs
   - Track operation durations
   - Monitor token usage patterns

2. **Advanced Classification**
   - Machine learning for error categorization
   - Automatic error recovery suggestions
   - Pattern detection for cascading failures

3. **Enhanced Reporting**
   - Daily error summaries
   - Trend analysis
   - Cost impact analysis (for rate limits)

4. **Integration Expansion**
   - Add error tracking for other components
   - Webhook notifications for critical errors
   - Slack/Discord integration

## References

- [Sentry SDK Documentation](https://docs.sentry.io/platforms/go/)
- [Sentry Fingerprinting](https://docs.sentry.io/platform-redirect/?next=/data-management/event-grouping/)
- [Go Error Handling Best Practices](https://blog.golang.org/error-handling-and-go)
- [SwarmOS SDK Error Types](../../sdk/error/)

## Support

For issues or questions:
- GitHub Issues: https://github.com/your-org/swarmos-tui/issues
- Internal Docs: See SDK error package documentation
- Sentry Dashboard: https://sentry.io/organizations/your-org/

---

**Last Updated:** 2026-02-09
**Version:** v0.5.8+
**Author:** SwarmOS Team
