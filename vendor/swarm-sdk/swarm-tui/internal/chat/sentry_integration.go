package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerror "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/getsentry/sentry-go"
)

// ============================================================================
// CUSTOM ERROR TYPES FOR SENTRY CATEGORIZATION
// ============================================================================

// AgentExecutionError represents an error during agent execution
type AgentExecutionError struct {
	Type           string // e.g., "context_canceled", "provider_error", "tool_error"
	Message        string
	Provider       string
	Model          string
	ConversationID string
	TraceID        string
	Retried        bool
	OriginalError  error
	Wrapped        error
}

func (e *AgentExecutionError) Error() string {
	if e.Retried && e.OriginalError != nil {
		return fmt.Sprintf("agent execution failed after retry [%s]: %s (original: %v) (trace: %s)", e.Type, e.Message, e.OriginalError, e.TraceID)
	}
	if e.TraceID != "" {
		return fmt.Sprintf("agent execution failed [%s]: %s (trace: %s)", e.Type, e.Message, e.TraceID)
	}
	return fmt.Sprintf("agent execution failed [%s]: %s", e.Type, e.Message)
}

func (e *AgentExecutionError) Unwrap() error {
	return e.Wrapped
}

// ContextCanceledError represents a user-initiated cancellation
type ContextCanceledError struct {
	ConversationID string
	TraceID        string
	Provider       string
	Model          string
	Wrapped        error
}

func (e *ContextCanceledError) Error() string {
	if e.TraceID != "" {
		return fmt.Sprintf("execution canceled by user (trace: %s)", e.TraceID)
	}
	return "execution canceled by user"
}

func (e *ContextCanceledError) Unwrap() error {
	return e.Wrapped
}

// ProviderError represents a provider-specific error
type ProviderError struct {
	Provider    string
	ErrorType   string // "rate_limited", "auth_failed", "timeout", "invalid_request"
	Message     string
	Code        string
	TraceID     string
	Retryable   bool
	StatusCode  int
	RawResponse string
	URL         string
	Wrapped     error
}

func (e *ProviderError) Error() string {
	if e.TraceID != "" {
		return fmt.Sprintf("provider error [%s/%s]: %s (trace: %s)", e.Provider, e.ErrorType, e.Message, e.TraceID)
	}
	return fmt.Sprintf("provider error [%s/%s]: %s", e.Provider, e.ErrorType, e.Message)
}

func (e *ProviderError) Unwrap() error {
	return e.Wrapped
}

// ToolExecutionError represents a tool execution error
type ToolExecutionError struct {
	ToolName   string
	ErrorType  string // "permission_denied", "not_found", "invalid_input", "execution_failed"
	Message    string
	TargetPath string
	TraceID    string
	Wrapped    error
}

func (e *ToolExecutionError) Error() string {
	traceSuffix := ""
	if e.TraceID != "" {
		traceSuffix = fmt.Sprintf(" (trace: %s)", e.TraceID)
	}
	if e.TargetPath != "" {
		return fmt.Sprintf("tool error [%s/%s]: %s (target: %s)%s", e.ToolName, e.ErrorType, e.Message, e.TargetPath, traceSuffix)
	}
	return fmt.Sprintf("tool error [%s/%s]: %s%s", e.ToolName, e.ErrorType, e.Message, traceSuffix)
}

func (e *ToolExecutionError) Unwrap() error {
	return e.Wrapped
}

// ============================================================================
// ERROR CLASSIFICATION AND DETECTION
// ============================================================================

// ClassifyError analyzes an error and returns a structured error type
func ClassifyError(ctx context.Context, err error, provider, model, conversationID string) error {
	if err == nil {
		return nil
	}

	traceID := GetTraceID(ctx, err)

	// Check for context cancellation
	if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
		return &ContextCanceledError{
			ConversationID: conversationID,
			TraceID:        traceID,
			Provider:       provider,
			Model:          model,
			Wrapped:        err,
		}
	}

	// Check for SDK structured error
	var sdkErr *sdkerror.Error
	if errors.As(err, &sdkErr) {
		return classifySDKError(sdkErr, provider, model, conversationID, traceID)
	}

	// Check for provider-specific errors
	errStr := err.Error()
	if strings.Contains(errStr, "all providers exhausted") {
		return &ProviderError{
			Provider:  provider,
			ErrorType: "all_providers_exhausted",
			Message:   errStr,
			Code:      "exhausted",
			TraceID:   traceID,
			Retryable: false,
			Wrapped:   err,
		}
	}

	if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "rate_limit") {
		return &ProviderError{
			Provider:  provider,
			ErrorType: "rate_limited",
			Message:   errStr,
			TraceID:   traceID,
			Retryable: true,
			Wrapped:   err,
		}
	}

	if strings.Contains(errStr, "authentication") || strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "invalid api key") || strings.Contains(errStr, "invalid_api_key") {
		return &ProviderError{
			Provider:  provider,
			ErrorType: "auth_failed",
			Message:   errStr,
			TraceID:   traceID,
			Retryable: false,
			Wrapped:   err,
		}
	}

	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
		return &ProviderError{
			Provider:  provider,
			ErrorType: "timeout",
			Message:   errStr,
			TraceID:   traceID,
			Retryable: true,
			Wrapped:   err,
		}
	}

	if strings.Contains(errStr, "invalid request") || strings.Contains(errStr, "bad request") {
		return &ProviderError{
			Provider:  provider,
			ErrorType: "invalid_request",
			Message:   errStr,
			TraceID:   traceID,
			Retryable: false,
			Wrapped:   err,
		}
	}

	// Check for tool errors
	if strings.Contains(errStr, "permission denied") || strings.Contains(errStr, "access denied") {
		toolName := extractToolName(errStr)
		return &ToolExecutionError{
			ToolName:  toolName,
			ErrorType: "permission_denied",
			Message:   errStr,
			TraceID:   traceID,
			Wrapped:   err,
		}
	}

	// Default to generic agent execution error
	return &AgentExecutionError{
		Type:           "unknown",
		Message:        errStr,
		Provider:       provider,
		Model:          model,
		ConversationID: conversationID,
		TraceID:        traceID,
		Wrapped:        err,
	}
}

func classifySDKError(sdkErr *sdkerror.Error, provider, model, conversationID, traceID string) error {
	// Convert SDK error type to our error types based on error type string
	errorType := strings.ToLower(sdkErr.Code)

	// Use TraceID from SDK error if present
	if sdkErr.TraceID != "" {
		traceID = sdkErr.TraceID
	}

	// Check if it's a provider error
	if strings.Contains(errorType, "provider") || strings.Contains(errorType, "rate_limit") ||
		strings.Contains(errorType, "auth") || strings.Contains(errorType, "timeout") {
		providerErrorType := "unknown"
		if strings.Contains(errorType, "rate_limit") {
			providerErrorType = "rate_limited"
		} else if strings.Contains(errorType, "auth") {
			providerErrorType = "auth_failed"
		} else if strings.Contains(errorType, "timeout") {
			providerErrorType = "timeout"
		} else if strings.Contains(errorType, "bad_request") || strings.Contains(errorType, "invalid_request") {
			providerErrorType = "invalid_request"
		}

		pe := &ProviderError{
			Provider:  provider,
			ErrorType: providerErrorType,
			Message:   sdkErr.Message,
			TraceID:   traceID,
			Retryable: sdkErr.Retryable,
			Wrapped:   sdkErr.Cause,
		}

		// Extract additional details from context
		if sdkErr.Attributes != nil {
			if val, ok := sdkErr.Attributes["status_code"].(int); ok {
				pe.StatusCode = val
			}
			if val, ok := sdkErr.Attributes["raw_response"].(string); ok {
				pe.RawResponse = val
			}
			if val, ok := sdkErr.Attributes["url"].(string); ok {
				pe.URL = val
			}
			if pe.Code == "" {
				if val, ok := sdkErr.Attributes["code"].(string); ok {
					pe.Code = val
				}
			}
		}

		return pe
	}

	// Check if it's a tool error
	if strings.Contains(errorType, "tool") {
		toolErrorType := "execution_failed"
		if strings.Contains(errorType, "permission") {
			toolErrorType = "permission_denied"
		} else if strings.Contains(errorType, "not_found") {
			toolErrorType = "not_found"
		}
		return &ToolExecutionError{
			ToolName:  extractToolName(sdkErr.Message),
			ErrorType: toolErrorType,
			Message:   sdkErr.Message,
			TraceID:   traceID,
			Wrapped:   sdkErr.Cause,
		}
	}

	// Default to agent execution error
	return &AgentExecutionError{
		Type:           sdkErr.Code,
		Message:        sdkErr.Message,
		Provider:       provider,
		Model:          model,
		ConversationID: conversationID,
		TraceID:        traceID,
		Wrapped:        sdkErr.Cause,
	}
}

func extractToolName(errStr string) string {
	// Try to extract tool name from error message
	// Common patterns: "tool: name", "[name]", "name error"
	if _, after, ok := strings.Cut(errStr, "tool:"); ok {
		parts := strings.Fields(after)
		if len(parts) > 0 {
			return parts[0]
		}
	}
	if idx := strings.Index(errStr, "["); idx != -1 {
		if endIdx := strings.Index(errStr[idx:], "]"); endIdx != -1 {
			return errStr[idx+1 : idx+endIdx]
		}
	}
	return "unknown"
}

// ============================================================================
// SENTRY INTEGRATION FUNCTIONS
// ============================================================================

// CaptureSentryError sends an error to Sentry with proper fingerprinting and context
func CaptureSentryError(err error, provider, model, conversationID string) *sentry.EventID {
	return CaptureSentryErrorWithContext(context.Background(), err, provider, model, conversationID)
}

// CaptureSentryErrorWithContext sends an error to Sentry with trace context propagation
func CaptureSentryErrorWithContext(ctx context.Context, err error, provider, model, conversationID string) *sentry.EventID {
	if err == nil {
		return nil
	}

	// Classify the error first
	classifiedErr := ClassifyError(ctx, err, provider, model, conversationID)

	// Extract trace information from context and error
	traceInfo := extractTraceContext(ctx, err)

	// Create Sentry event
	hub := sentry.CurrentHub()

	// Create a custom event to ensure we control the Exception structure
	// This prevents Sentry from unwrapping our custom errors and grouping by *fmt.wrapError
	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Message = classifiedErr.Error()

	// Strip the pointer info for cleaner type names
	errType := fmt.Sprintf("%T", classifiedErr)
	if after, ok := strings.CutPrefix(errType, "*chat."); ok {
		errType = after
	}

	// Enrich the type name with the error type if available
	if pe, ok := classifiedErr.(*ProviderError); ok {
		errType = fmt.Sprintf("ProviderError:%s", pe.ErrorType)
	} else if te, ok := classifiedErr.(*ToolExecutionError); ok {
		errType = fmt.Sprintf("ToolError:%s:%s", te.ToolName, te.ErrorType)
	} else if ae, ok := classifiedErr.(*AgentExecutionError); ok {
		errType = fmt.Sprintf("AgentError:%s", ae.Type)
	}

	// Manually construct the exception
	event.Exception = []sentry.Exception{{
		Type:       errType,
		Value:      classifiedErr.Error(),
		Module:     "internal/chat",
		Stacktrace: sentry.ExtractStacktrace(err),
	}}

	// Set fingerprint directly on the event to ensure it's honored
	event.Fingerprint = generateFingerprint(classifiedErr)

	// Set trace context for distributed tracing
	if traceInfo.TraceID != "" {
		hub.ConfigureScope(func(scope *sentry.Scope) {
			// Set Sentry trace context for distributed tracing visualization
			scope.SetContext("trace", map[string]any{
				"trace_id":        traceInfo.TraceID,
				"span_id":         traceInfo.SpanID,
				"parent_span_id":  traceInfo.ParentSpanID,
				"conversation_id": conversationID,
				"origin":          traceInfo.Origin,
			})

			// Also set as tags for filtering
			if traceInfo.TraceID != "" {
				scope.SetTag("trace_id", traceInfo.TraceID)
			}
			if traceInfo.SpanID != "" {
				scope.SetTag("span_id", traceInfo.SpanID)
			}
		})
	}

	// Add breadcrumb for this error with trace context
	hub.AddBreadcrumb(&sentry.Breadcrumb{
		Type:     "error",
		Category: "agent.execution",
		Message:  fmt.Sprintf("Error occurred: %v", err),
		Level:    sentry.LevelError,
		Data: map[string]any{
			"trace_id":   traceInfo.TraceID,
			"span_id":    traceInfo.SpanID,
			"error_type": fmt.Sprintf("%T", classifiedErr),
		},
	}, nil)

	// Configure scope with custom context
	hub.ConfigureScope(func(scope *sentry.Scope) {
		// Set custom fingerprint based on error type
		fingerprint := generateFingerprint(classifiedErr)
		scope.SetFingerprint(fingerprint)

		// Add context based on error type
		addErrorContext(scope, classifiedErr, provider, model, conversationID)

		// Add tags for filtering
		scope.SetTag("error_category", getErrorCategory(classifiedErr))
		if provider != "" {
			scope.SetTag("provider", provider)
		}
		if model != "" {
			scope.SetTag("model", model)
		}
		if conversationID != "" {
			scope.SetTag("conversation_id", conversationID)
		}

		// Add trace origin tag
		if traceInfo.Origin != "" {
			scope.SetTag("trace_origin", traceInfo.Origin)
		}

		// Add extra context
		scope.SetContext("error_details", map[string]any{
			"original_error":  err.Error(),
			"classified_as":   fmt.Sprintf("%T", classifiedErr),
			"stack_trace":     captureStackTrace(),
			"trace_id":        traceInfo.TraceID,
			"span_id":         traceInfo.SpanID,
			"parent_span_id":  traceInfo.ParentSpanID,
			"sdk_error_trace": traceInfo.SDKTraceID,
		})

		// Add SDK error metadata if present
		if sdkErr := extractSDKError(err); sdkErr != nil {
			sdkCtx := map[string]any{
				"type":            sdkErr.Code,
				"category":        sdkErr.Category.String(),
				"message":         sdkErr.Message,
				"retryable":       sdkErr.Retryable,
				"trace_id":        sdkErr.TraceID,
				"conversation_id": sdkErr.ConversationID,
				"agent_id":        sdkErr.AgentID,
				"timestamp":       sdkErr.OccurredAt,
			}
			// Include full context map
			if sdkErr.Attributes != nil {
				for k, v := range sdkErr.Attributes {
					sdkCtx["ctx_"+k] = v
				}
			}
			scope.SetContext("sdk_error", sdkCtx)

			// Also add to Extra for redundancy
			scope.SetExtra("sdk_error_full", sdkErr)
		}
	})

	// Add global extra information to the event directly
	if event.Extra == nil {
		event.Extra = make(map[string]any)
	}
	event.Extra["provider"] = provider
	event.Extra["model"] = model
	event.Extra["conversation_id"] = conversationID

	if sdkErr := extractSDKError(err); sdkErr != nil {
		if sdkErr.Attributes != nil {
			event.Extra["sdk_context"] = sdkErr.Attributes
		}
	}

	// Capture the event instead of the exception to enforce our structure
	eventID := hub.CaptureEvent(event)

	return eventID
}

// generateFingerprint creates a custom fingerprint for Sentry grouping
func generateFingerprint(err error) []string {
	switch e := err.(type) {
	case *ContextCanceledError:
		return []string{"context-canceled", e.Provider}

	case *ProviderError:
		return []string{"provider-error", e.Provider, e.ErrorType}

	case *ToolExecutionError:
		return []string{"tool-error", e.ToolName, e.ErrorType}

	case *AgentExecutionError:
		return []string{"agent-error", e.Type, e.Provider}

	default:
		// Fallback to default grouping
		return []string{"{{ default }}"}
	}
}

// getErrorCategory returns a human-readable category for tagging
func getErrorCategory(err error) string {
	switch err.(type) {
	case *ContextCanceledError:
		return "user_cancellation"
	case *ProviderError:
		return "provider_error"
	case *ToolExecutionError:
		return "tool_error"
	case *AgentExecutionError:
		return "agent_error"
	default:
		return "unknown"
	}
}

// addErrorContext adds type-specific context to the Sentry scope
func addErrorContext(scope *sentry.Scope, err error, provider, model, conversationID string) {
	switch e := err.(type) {
	case *ContextCanceledError:
		scope.SetContext("cancellation", map[string]any{
			"conversation_id": e.ConversationID,
			"provider":        e.Provider,
			"model":           e.Model,
		})
		scope.SetLevel(sentry.LevelInfo) // Cancellation is not an error

	case *ProviderError:
		providerCtx := map[string]any{
			"provider":   e.Provider,
			"error_type": e.ErrorType,
			"message":    e.Message,
			"code":       e.Code,
			"retryable":  e.Retryable,
		}
		if e.StatusCode != 0 {
			providerCtx["status_code"] = e.StatusCode
		}
		if e.URL != "" {
			providerCtx["url"] = e.URL
		}
		scope.SetContext("provider_error", providerCtx)

		if e.RawResponse != "" {
			// If it's JSON, try to unmarshal it for better display
			var jsonResp any
			if err := json.Unmarshal([]byte(e.RawResponse), &jsonResp); err == nil {
				scope.SetContext("raw_response", map[string]any{
					"json": jsonResp,
				})
			} else {
				scope.SetContext("raw_response", map[string]any{
					"text": e.RawResponse,
				})
			}
		}

		scope.SetTag("error_type", e.ErrorType)
		scope.SetTag("retryable", fmt.Sprintf("%v", e.Retryable))
		if e.StatusCode != 0 {
			scope.SetTag("status_code", fmt.Sprintf("%d", e.StatusCode))
		}

	case *ToolExecutionError:
		scope.SetContext("tool_error", map[string]any{
			"tool_name":   e.ToolName,
			"error_type":  e.ErrorType,
			"message":     e.Message,
			"target_path": e.TargetPath,
		})
		scope.SetTag("tool_name", e.ToolName)
		scope.SetTag("error_type", e.ErrorType)

	case *AgentExecutionError:
		scope.SetContext("agent_error", map[string]any{
			"type":            e.Type,
			"message":         e.Message,
			"provider":        e.Provider,
			"model":           e.Model,
			"conversation_id": e.ConversationID,
			"retried":         e.Retried,
		})
		scope.SetTag("error_type", e.Type)
		scope.SetTag("retried", fmt.Sprintf("%v", e.Retried))
	}
}

// captureStackTrace captures the current stack trace
func captureStackTrace() string {
	stackBuf := make([]byte, 4096)
	n := runtime.Stack(stackBuf, false)
	return string(stackBuf[:n])
}

// AddSentryBreadcrumb adds a breadcrumb to the current Sentry scope
func AddSentryBreadcrumb(category, message string, level sentry.Level, data map[string]any) {
	sentry.AddBreadcrumb(&sentry.Breadcrumb{
		Type:     "default",
		Category: category,
		Message:  message,
		Level:    level,
		Data:     data,
	})
}

// ============================================================================
// PANIC CAPTURE FOR SENTRY
// ============================================================================

// CapturePanicEvent sends a recovered panic to Sentry with the full goroutine
// stack trace attached. Call this inside a defer/recover block, passing the
// value returned by recover() and the stack from debug.Stack().
//
// location identifies the call site (e.g. "agent_execution", "wake_runtime").
// extra is an optional map of additional key/value pairs to attach.
func CapturePanicEvent(panicVal any, stack []byte, location string, extra map[string]any) *sentry.EventID {
	hub := sentry.CurrentHub()
	if hub == nil {
		return nil
	}
	hub = hub.Clone()

	panicMsg := fmt.Sprintf("%v", panicVal)
	event := sentry.NewEvent()
	event.Level = sentry.LevelFatal
	event.Message = fmt.Sprintf("panic in %s: %s", location, panicMsg)

	event.Exception = []sentry.Exception{{
		Type:       fmt.Sprintf("Panic:%s", location),
		Value:      panicMsg,
		Module:     "internal/chat",
		Stacktrace: sentry.ExtractStacktrace(fmt.Errorf("%v", panicVal)),
	}}

	event.Fingerprint = []string{"panic", location, panicMsg}

	event.Tags = map[string]string{
		"error_category": "panic",
		"panic_location": location,
	}

	event.Contexts = map[string]map[string]any{
		"panic": {
			"value":    panicMsg,
			"location": location,
			"stack":    string(stack),
		},
		"runtime": collectRuntimeStats(),
	}

	event.Extra = map[string]any{
		"panic_value":     panicMsg,
		"panic_location":  location,
		"goroutine_stack": string(stack),
	}
	maps.Copy(event.Extra, extra)

	eventID := hub.CaptureEvent(event)
	hub.Flush(2 * time.Second)
	return eventID
}

// ============================================================================
// HELPER FUNCTIONS FOR ERROR CREATION
// ============================================================================

// NewAgentExecutionError creates a new AgentExecutionError
func NewAgentExecutionError(ctx context.Context, errType, message, provider, model, conversationID string, wrapped error) *AgentExecutionError {
	return &AgentExecutionError{
		Type:           errType,
		Message:        message,
		Provider:       provider,
		Model:          model,
		ConversationID: conversationID,
		TraceID:        GetTraceID(ctx, wrapped),
		Wrapped:        wrapped,
	}
}

// NewAgentExecutionErrorWithRetry creates a new AgentExecutionError with retry information
func NewAgentExecutionErrorWithRetry(ctx context.Context, errType, message, provider, model, conversationID string, originalErr, retryErr error) *AgentExecutionError {
	return &AgentExecutionError{
		Type:           errType,
		Message:        message,
		Provider:       provider,
		Model:          model,
		ConversationID: conversationID,
		TraceID:        GetTraceID(ctx, retryErr),
		Retried:        true,
		OriginalError:  originalErr,
		Wrapped:        retryErr,
	}
}

// ShouldIgnoreError determines if an error should be ignored in Sentry
func ShouldIgnoreError(err error) bool {
	if err == nil {
		return true
	}

	// Ignore context cancellations (user-initiated, not actual errors)
	if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
		return true
	}

	// Check for classified errors
	if _, ok := err.(*ContextCanceledError); ok {
		return true
	}

	return false
}

// ============================================================================
// TRACE CONTEXT EXTRACTION FOR DISTRIBUTED TRACING
// ============================================================================

// TraceContext contains distributed trace information
type TraceContext struct {
	TraceID      string // Trace ID from context or SDK error
	SpanID       string // Span ID from context
	ParentSpanID string // Parent span ID for nesting
	SDKTraceID   string // SDK error TraceID if different
	Origin       string // Where the trace originated (TUI, SDK, etc.)
}

// extractTraceContext extracts trace information from context and error
func extractTraceContext(ctx context.Context, err error) TraceContext {
	trace := TraceContext{
		Origin: "TUI",
	}

	// Extract from context (TUI tracer)
	if span := extractSpanFromContext(ctx); span != nil {
		trace.TraceID = span.TraceID()
		trace.SpanID = span.SpanID()
		trace.Origin = "TUI"
	}
	if trace.TraceID == "" {
		if traceID, ok := ctx.Value("trace_id").(string); ok && traceID != "" {
			trace.TraceID = traceID
			trace.Origin = "Context"
		}
	}

	// Extract from SDK error if present
	if sdkErr := extractSDKError(err); sdkErr != nil {
		if sdkErr.TraceID != "" {
			// If we have a TUI trace, keep it and store SDK trace separately
			if trace.TraceID == "" {
				trace.TraceID = sdkErr.TraceID
				trace.Origin = "SDK"
			} else {
				trace.SDKTraceID = sdkErr.TraceID
				trace.Origin = "TUI→SDK" // Cross-boundary trace
			}
		}
	}

	// Generate trace ID if none exists
	if trace.TraceID == "" {
		trace.TraceID = fmt.Sprintf("trace-%d", time.Now().UnixNano())
		trace.Origin = "Generated"
	}

	return trace
}

// extractSpanFromContext extracts span from context if available
func extractSpanFromContext(ctx context.Context) observability.Span {
	// Try to get TUITracer from SDK integration
	// This is a type assertion that may fail, which is fine
	type spanContextKey string
	const tuiSpanKey spanContextKey = "tui_span"

	if span, ok := ctx.Value(tuiSpanKey).(observability.Span); ok {
		return span
	}

	return nil
}

// extractSDKError extracts SDK error from error chain
func extractSDKError(err error) *sdkerror.Error {
	var sdkErr *sdkerror.Error
	if errors.As(err, &sdkErr) {
		return sdkErr
	}
	return nil
}

// GetTraceID extracts trace ID from error or context
func GetTraceID(ctx context.Context, err error) string {
	trace := extractTraceContext(ctx, err)
	return trace.TraceID
}

// GetSpanID extracts span ID from context
func GetSpanID(ctx context.Context) string {
	if span := extractSpanFromContext(ctx); span != nil {
		return span.SpanID()
	}
	return ""
}

// FormatTraceInfo returns a human-readable trace information string
func FormatTraceInfo(ctx context.Context, err error) string {
	trace := extractTraceContext(ctx, err)

	if trace.SDKTraceID != "" {
		return fmt.Sprintf("Trace: %s (TUI) → %s (SDK) | Span: %s | Origin: %s",
			trace.TraceID, trace.SDKTraceID, trace.SpanID, trace.Origin)
	}

	return fmt.Sprintf("Trace: %s | Span: %s | Origin: %s",
		trace.TraceID, trace.SpanID, trace.Origin)
}

// ============================================================================
// USER-INITIATED BUG REPORT
// ============================================================================

// MessageSummary captures non-sensitive metadata about a single chat message.
// Content is intentionally excluded for user privacy.
type MessageSummary struct {
	Role         string `json:"role"`
	ContentLen   int    `json:"content_len"`
	HasToolCalls bool   `json:"has_tool_calls,omitempty"`
	HasThinking  bool   `json:"has_thinking,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	Model        string `json:"model,omitempty"`
}

// DebugRequestSummary is a sanitized, compact version of a DebugRequest.
type DebugRequestSummary struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Method       string    `json:"method"`
	URL          string    `json:"url"`
	ResponseCode int       `json:"response_code"`
	DurationMS   int64     `json:"duration_ms"`
	TokensIn     int       `json:"tokens_in,omitempty"`
	TokensOut    int       `json:"tokens_out,omitempty"`
	Model        string    `json:"model,omitempty"`
	HasError     bool      `json:"has_error,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// BugReportState is the complete snapshot of client state collected at the
// moment the user runs /bug.
type BugReportState struct {
	// User-supplied description (may be empty)
	Description string

	// Timestamp when the report was initiated
	CapturedAt time.Time

	// Build / version info (version.LogFields())
	Version map[string]any

	// Last N log lines from the in-memory ring buffer
	Logs []string

	// Sanitized summaries of the last N API request/response cycles
	DebugRequests []DebugRequestSummary

	// Per-message metadata for the current conversation (no content)
	MessageSummaries []MessageSummary

	// Sanitized subset of config.json (no API keys / tokens)
	ConfigSummary map[string]any

	// Current screen / layout state
	ScreenState map[string]any

	// Go runtime statistics
	RuntimeStats map[string]any
}

// CaptureBugReportEvent sends a user-initiated bug report to Sentry.
// It creates an informational event (not an error) tagged as "user_bug_report"
// and attaches all collected state as contexts + breadcrumbs.
//
// Returns the Sentry event ID, or nil if Sentry is not initialised.
func CaptureBugReportEvent(state BugReportState) *sentry.EventID {
	hub := sentry.CurrentHub()
	if hub == nil {
		return nil
	}

	event := sentry.NewEvent()
	event.Level = sentry.LevelInfo

	msg := "User-submitted bug report"
	if state.Description != "" {
		msg = "User-submitted bug report: " + state.Description
	}
	event.Message = msg

	// Unique-ish fingerprint so each report becomes its own issue in Sentry.
	ts := state.CapturedAt.UTC().Format("2006-01-02T15:04")
	event.Fingerprint = []string{"user-bug-report", ts}

	// Tags (indexed, filterable)
	event.Tags = map[string]string{
		"report_type": "user_bug_report",
	}
	if v, ok := state.Version["version"].(string); ok && v != "" {
		event.Tags["app_version"] = v
	}
	if v, ok := state.Version["git_commit"].(string); ok && v != "" {
		event.Tags["git_commit"] = v
	}
	if screen, ok := state.ScreenState["screen"].(string); ok && screen != "" {
		event.Tags["screen"] = screen
	}
	if provider, ok := state.ScreenState["provider"].(string); ok && provider != "" {
		event.Tags["provider"] = provider
	}
	if model, ok := state.ScreenState["model"].(string); ok && model != "" {
		event.Tags["model"] = model
	}

	// Contexts (non-indexed, rich structured data)
	event.Contexts = map[string]map[string]any{
		"version":              state.Version,
		"screen":               state.ScreenState,
		"runtime":              state.RuntimeStats,
		"config_summary":       state.ConfigSummary,
		"conversation_summary": buildConversationSummary(state.MessageSummaries),
	}

	// Extra top-level metadata
	event.Extra = map[string]any{
		"description":       state.Description,
		"captured_at":       state.CapturedAt.UTC().Format(time.RFC3339),
		"message_count":     len(state.MessageSummaries),
		"log_line_count":    len(state.Logs),
		"api_request_count": len(state.DebugRequests),
	}

	// Breadcrumbs: log lines (most recent 50)
	logLines := state.Logs
	if len(logLines) > 50 {
		logLines = logLines[len(logLines)-50:]
	}
	for _, line := range logLines {
		level := classifyLogLineSentryLevel(line)
		hub.AddBreadcrumb(&sentry.Breadcrumb{
			Type:     "default",
			Category: "app.log",
			Message:  line,
			Level:    level,
		}, nil)
	}

	// Breadcrumbs: API request summary (most recent 10)
	reqs := state.DebugRequests
	if len(reqs) > 10 {
		reqs = reqs[len(reqs)-10:]
	}
	for _, req := range reqs {
		data := map[string]any{
			"url":         req.URL,
			"method":      req.Method,
			"response":    req.ResponseCode,
			"duration_ms": req.DurationMS,
			"tokens_in":   req.TokensIn,
			"tokens_out":  req.TokensOut,
			"model":       req.Model,
		}
		if req.HasError {
			data["error"] = req.Error
		}
		lvl := sentry.LevelInfo
		if req.HasError {
			lvl = sentry.LevelError
		}
		hub.AddBreadcrumb(&sentry.Breadcrumb{
			Type:      "http",
			Category:  "api.request",
			Message:   fmt.Sprintf("%s %s -> %d", req.Method, req.URL, req.ResponseCode),
			Level:     lvl,
			Timestamp: req.Timestamp,
			Data:      data,
		}, nil)
	}

	// Attach full logs as event extra (up to 200 lines)
	fullLogs := state.Logs
	if len(fullLogs) > 200 {
		fullLogs = fullLogs[len(fullLogs)-200:]
	}
	event.Extra["logs"] = strings.Join(fullLogs, "\n")

	// Attach debug requests JSON
	if len(state.DebugRequests) > 0 {
		if reqJSON, err := json.Marshal(state.DebugRequests); err == nil {
			event.Extra["api_requests"] = string(reqJSON)
		}
	}

	// Attach message summaries
	if len(state.MessageSummaries) > 0 {
		if msJSON, err := json.Marshal(state.MessageSummaries); err == nil {
			event.Extra["message_summaries"] = string(msJSON)
		}
	}

	return hub.CaptureEvent(event)
}

// buildConversationSummary produces a rolled-up summary from per-message metadata.
func buildConversationSummary(summaries []MessageSummary) map[string]any {
	if len(summaries) == 0 {
		return map[string]any{"message_count": 0}
	}

	roles := map[string]int{}
	totalInputTokens := 0
	totalOutputTokens := 0
	hasToolCalls := false
	hasThinking := false

	for _, m := range summaries {
		roles[m.Role]++
		totalInputTokens += m.InputTokens
		totalOutputTokens += m.OutputTokens
		if m.HasToolCalls {
			hasToolCalls = true
		}
		if m.HasThinking {
			hasThinking = true
		}
	}

	return map[string]any{
		"message_count":       len(summaries),
		"roles":               roles,
		"total_input_tokens":  totalInputTokens,
		"total_output_tokens": totalOutputTokens,
		"has_tool_calls":      hasToolCalls,
		"has_thinking":        hasThinking,
	}
}

// classifyLogLineSentryLevel maps log line text to a Sentry breadcrumb level.
func classifyLogLineSentryLevel(line string) sentry.Level {
	upper := strings.ToUpper(line)
	switch {
	case strings.Contains(upper, "FATAL") || strings.Contains(upper, "PANIC"):
		return sentry.LevelFatal
	case strings.Contains(upper, "ERROR"):
		return sentry.LevelError
	case strings.Contains(upper, "WARN"):
		return sentry.LevelWarning
	case strings.Contains(upper, "DEBUG") || strings.Contains(upper, "TRACE"):
		return sentry.LevelDebug
	default:
		return sentry.LevelInfo
	}
}

// sanitizeConfigMap removes sensitive fields (api_key, token, password, secret)
// from a config map before sending to Sentry.
func sanitizeConfigMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}
	out := make(map[string]any, len(cfg))
	sensitiveSubstrings := []string{"api_key", "apikey", "token", "password", "secret", "auth", "credential"}
	for k, v := range cfg {
		kLower := strings.ToLower(k)
		isSensitive := false
		for _, sk := range sensitiveSubstrings {
			if strings.Contains(kLower, sk) {
				isSensitive = true
				break
			}
		}
		if isSensitive {
			out[k] = "[REDACTED]"
			continue
		}
		// Recurse into nested maps
		if nested, ok := v.(map[string]any); ok {
			out[k] = sanitizeConfigMap(nested)
		} else {
			out[k] = v
		}
	}
	return out
}

// collectRuntimeStats gathers current Go runtime statistics.
func collectRuntimeStats() map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return map[string]any{
		"goroutines":       runtime.NumGoroutine(),
		"heap_alloc_mb":    float64(ms.HeapAlloc) / 1024 / 1024,
		"heap_sys_mb":      float64(ms.HeapSys) / 1024 / 1024,
		"heap_in_use_mb":   float64(ms.HeapInuse) / 1024 / 1024,
		"gc_runs":          ms.NumGC,
		"last_gc_pause_ms": float64(ms.PauseNs[(ms.NumGC+255)%256]) / 1e6,
	}
}

// ============================================================================
// INVALID REQUEST CAPTURE — tool_use / tool_result pairing errors
// ============================================================================

// ConvMessageDiag is a diagnostic snapshot of a single canonical conversation
// message, recording only the structural metadata needed to reproduce pairing
// errors (no user content is included).
type ConvMessageDiag struct {
	Index         int      `json:"index"`
	ID            string   `json:"id"`
	Role          string   `json:"role"`
	ContentLen    int      `json:"content_len"`
	ToolUseIDs    []string `json:"tool_use_ids,omitempty"`
	ToolResultIDs []string `json:"tool_result_ids,omitempty"`
	IsHookContext bool     `json:"is_hook_context,omitempty"`
}

// CaptureInvalidRequestError captures an Anthropic invalid_request error to Sentry
// with extreme diagnostic context: the full canonical conversation structure
// (roles, tool_use IDs, tool_result IDs, hook_context markers) so the pairing
// mismatch can be reproduced from the Sentry event alone.
//
// Call this whenever an error contains "invalid_request" or
// "unexpected tool_use_id" from the Anthropic API.
func CaptureInvalidRequestError(
	ctx context.Context,
	err error,
	provider, model, convID string,
	history []*conversation.Message,
) *sentry.EventID {
	if err == nil {
		return nil
	}

	// ── Build structural digest of the conversation history ───────────────
	diag := make([]ConvMessageDiag, 0, len(history))
	for i, msg := range history {
		d := ConvMessageDiag{
			Index:      i,
			ID:         msg.ID,
			Role:       string(msg.Role),
			ContentLen: len(msg.Content),
		}
		for _, tc := range msg.ToolCalls {
			d.ToolUseIDs = append(d.ToolUseIDs, tc.ID)
		}
		for _, tr := range msg.ToolResults {
			d.ToolResultIDs = append(d.ToolResultIDs, tr.CallID)
		}
		if msg.Metadata != nil {
			if t, ok := msg.Metadata["type"].(string); ok && t == "hook_context" {
				d.IsHookContext = true
			}
		}
		diag = append(diag, d)
	}

	// ── Build human-readable structure string (like Claude Code's messageTypes) ──
	lines := make([]string, 0, len(diag))
	for _, d := range diag {
		switch {
		case len(d.ToolUseIDs) > 0:
			lines = append(lines, fmt.Sprintf("[%d] %s(tool_use=[%s])", d.Index, d.Role, strings.Join(d.ToolUseIDs, ",")))
		case len(d.ToolResultIDs) > 0:
			lines = append(lines, fmt.Sprintf("[%d] %s(tool_results=[%s])", d.Index, d.Role, strings.Join(d.ToolResultIDs, ",")))
		case d.IsHookContext:
			lines = append(lines, fmt.Sprintf("[%d] %s(hook_context)", d.Index, d.Role))
		default:
			lines = append(lines, fmt.Sprintf("[%d] %s", d.Index, d.Role))
		}
	}
	structureStr := strings.Join(lines, " 	 ")

	traceInfo := extractTraceContext(ctx, err)
	hub := sentry.CurrentHub()
	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Message = fmt.Sprintf("invalid_request: %s", err.Error())
	event.Exception = []sentry.Exception{{
		Type:       "InvalidRequestError:tool_use_pairing",
		Value:      err.Error(),
		Module:     "sdk/provider/anthropic",
		Stacktrace: sentry.ExtractStacktrace(err),
	}}
	event.Fingerprint = []string{
		"invalid_request_error",
		"tool_use_pairing",
		provider,
	}

	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("error_category", "invalid_request")
		scope.SetTag("provider", provider)
		scope.SetTag("model", model)
		scope.SetTag("conversation_id", convID)
		if traceInfo.TraceID != "" {
			scope.SetTag("trace_id", traceInfo.TraceID)
		}

		// Full conversation structure — the PRIMARY diagnostic payload
		scope.SetContext("conversation_structure", map[string]any{
			"message_count": len(history),
			"structure":     structureStr,
			"messages":      diag,
		})
		scope.SetContext("error_details", map[string]any{
			"error":           err.Error(),
			"provider":        provider,
			"model":           model,
			"conversation_id": convID,
			"trace_id":        traceInfo.TraceID,
		})
	})

	// Attach structure as Extra so it survives any scope isolation
	event.Extra = map[string]any{
		"provider":               provider,
		"model":                  model,
		"conversation_id":        convID,
		"message_count":          len(history),
		"conversation_structure": structureStr,
		"conversation_detail":    diag,
	}

	diagJSON, _ := json.Marshal(diag)
	logDebug("[SENTRY] Capturing invalid_request error. Conversation structure: %s", structureStr)
	logDebug("[SENTRY] Conversation detail JSON: %s", string(diagJSON))

	eventID := hub.CaptureEvent(event)
	return eventID
}
