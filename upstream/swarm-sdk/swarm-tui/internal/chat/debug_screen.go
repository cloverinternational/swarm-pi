package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/envelope"
)

// debugScreenActive tracks whether the debug screen is visible. When false,
// marshalForDebug is a no-op, avoiding expensive json.MarshalIndent on every
// API call when nobody is looking at the debug screen.
var debugScreenActive atomic.Bool

// SetDebugScreenActive updates whether the debug screen is visible.
func SetDebugScreenActive(active bool) {
	debugScreenActive.Store(active)
}

// hashContent returns first 8 chars of SHA256 hash for quick comparison
func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])[:8]
}

// DebugRequest captures a complete API request/response cycle for inspection
type DebugRequest struct {
	ID           string            // Unique ID for this request
	MessageIndex int               // Which message this relates to
	Timestamp    time.Time         // When the request was made
	Method       string            // HTTP method (POST, GET, etc)
	URL          string            // Full request URL
	Headers      map[string]string // Request headers (sanitized API keys)
	Body         string            // Request body (pretty-printed JSON)
	Response     string            // Response body (pretty-printed JSON)

	// Universal Envelope data
	Envelopes []*envelope.Envelope // Sequence of events for this request

	// Store both canonical and provider JSONs for diffing
	CanonicalJSON *string `json:"canonical_json,omitempty"` // Original Go conversation model
	ProviderJSON  *string `json:"provider_json,omitempty"`  // Transformed API request JSON

	ResponseCode int            // HTTP response code
	Error        string         // Error message if request failed
	Duration     time.Duration  // How long the request took
	TokensInput  int            // Tokens used in input
	TokensOutput int            // Tokens generated in output
	Model        string         // Model used for this request
	Metadata     map[string]any // Additional metadata

	// Conversation state tracking
	ConversationVersion int                     // Increments with each change
	MessageCount        int                     // Number of messages in this request
	MessageHashes       []string                // Hash of each message for diff detection
	MessagesAdded       []string                // IDs of messages added since last request
	MessagesRemoved     []string                // IDs of messages removed since last request (trimming)
	ConversationState   *DebugConversationState // Full snapshot for comparison
}

// DebugConversationState captures the state of a conversation at a point in time for debug tracking
type DebugConversationState struct {
	MessageCount       int                 `json:"message_count"`
	TotalContentLength int                 `json:"total_content_length"`
	Messages           []DebugMessageState `json:"messages"`
	SystemPromptLength int                 `json:"system_prompt_length"`
	ToolCount          int                 `json:"tool_count"`
}

// DebugMessageState captures essential info about a message for debug tracking
type DebugMessageState struct {
	Index          int    `json:"index"`
	ID             string `json:"id"`
	Role           string `json:"role"`
	ContentLength  int    `json:"content_length"`
	ContentHash    string `json:"content_hash"` // First 8 chars of hash for quick comparison
	HasToolCalls   bool   `json:"has_tool_calls,omitempty"`
	HasToolResults bool   `json:"has_tool_results,omitempty"`
	HasThinking    bool   `json:"has_thinking,omitempty"`
	TokensTotal    int    `json:"tokens_total,omitempty"`
}

// DebugTab represents a tab in the debug screen
type DebugTab int

const (
	DebugTabRequests DebugTab = iota // Request inspector (list + per-request detail lenses)
	DebugTabLogs                     // Application log viewer
	DebugTabUsage                    // Provider usage / quota / token consumption
	DebugTabContext                  // Context composition audit (what's in the prompt, incl. hidden/ephemeral)
)

// debugTabCount is the number of tabs in the debug screen (used for tab wrap-around).
const debugTabCount = int(DebugTabContext) + 1

// debugLens selects which detail view is shown for the selected request inside
// the Requests inspector. Lenses that have no data for the current request are
// not offered (see availableLenses), so the user never lands on an empty panel.
type debugLens int

const (
	lensOverview debugLens = iota // request metadata + conversation state
	lensRequest                   // request body + response JSON
	lensProvider                  // provider-format JSON (only when captured)
	lensEvents                    // raw API events for this request (only when captured)
)

// marshalForDebug safely marshals a value to pretty-printed JSON for debugging
func marshalForDebug(v any) *string {
	// Skip expensive marshaling when the debug screen is not visible.
	// This avoids json.MarshalIndent on the full conversation on every
	// API call — a major source of heap allocation (4.5MB) and CPU.
	if !debugScreenActive.Load() {
		return nil
	}
	jsonBytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		errStr := fmt.Sprintf(`{"error": "failed to marshal", "details": "%s"}`, err.Error())
		return &errStr
	}
	result := string(jsonBytes)
	return &result
}

// DebugScreen shows detailed debugging information for messages
type DebugScreen struct {
	visible               bool
	width                 int
	height                int
	theme                 Theme
	requests              []DebugRequest         // All captured requests
	selectedReq           int                    // Which request is selected
	contextCalls          []DebugContextCall     // Paired provider-call context snapshots
	selectedContextCall   int                    // Which provider call the Context tab shows
	contextMu             sync.RWMutex           // Protects contextCalls and selectedContextCall
	activeTab             DebugTab               // Which tab is active
	detailOpen            bool                   // Requests tab: showing a request's detail lenses vs the list
	lens                  debugLens              // Active detail lens while detailOpen
	tabScrollOffsets      map[DebugTab]int       // Per-tab scroll positions
	enhancedLogsView      *EnhancedLogsView      // Enhanced log viewer with filtering
	enhancedRawEventsView *EnhancedRawEventsView // Enhanced raw events viewer
	jsonPretty            bool                   // Toggle pretty-print JSON
	showSanitized         bool                   // Show sanitized API keys
	autoScroll            bool                   // Auto-scroll to latest request
	envelopeHandler       *envelope.Handler      // Unified transformation handler

	// Usage tab bridge - the usage renderer lives on *App, so we bridge via callbacks.
	usageRenderer   func(width, height int) []string // Returns rendered usage lines
	usageActivate   func() tea.Cmd                   // Triggers async usage data load
	usageSubTabPrev func()                           // Switch to previous usage sub-tab
	usageSubTabNext func()                           // Switch to next usage sub-tab
}

// NewDebugScreen creates a new debug screen
func NewDebugScreen() *DebugScreen {
	// Log enum values for debugging
	logDebug("[DebugScreen] Enum values: DebugTabRequests=%d, DebugTabLogs=%d, DebugTabUsage=%d",
		DebugTabRequests, DebugTabLogs, DebugTabUsage)

	ds := &DebugScreen{
		visible:               false,
		requests:              make([]DebugRequest, 0),
		contextCalls:          make([]DebugContextCall, 0),
		enhancedLogsView:      NewEnhancedLogsView(1000),      // Start with 1000 log capacity
		enhancedRawEventsView: NewEnhancedRawEventsView(1000), // Start with 1000 event capacity
		selectedReq:           0,
		selectedContextCall:   -1,
		activeTab:             DebugTabRequests,
		jsonPretty:            true,
		showSanitized:         true,
		autoScroll:            true,
		tabScrollOffsets:      make(map[DebugTab]int),
		envelopeHandler:       envelope.NewHandler("tui-debug"),
	}
	// Set global reference for debug logging
	return ds
}

// maybeActivateUsage triggers the async usage load when the Usage tab becomes active.
// It is a no-op (returns nil) when the callback is unset or the active tab is not Usage.
// The returned tea.Cmd drives the loading spinner animation and should be propagated
// by the caller when it returns a command to the bubbletea runtime.
func (d *DebugScreen) maybeActivateUsage() tea.Cmd {
	if d.activeTab == DebugTabUsage && d.usageActivate != nil {
		return d.usageActivate()
	}
	return nil
}

// switchTab moves the active tab by delta (with wrap-around) and leaves the
// Requests inspector's detail view, returning the user to the request list.
func (d *DebugScreen) switchTab(delta int) {
	n := debugTabCount
	d.activeTab = DebugTab((int(d.activeTab) + delta%n + n) % n)
	d.detailOpen = false
	d.resetScrollOffset()
}

// availableLenses returns the detail lenses that actually have data for the
// currently selected request. Overview + Request always exist; Provider and
// Events are only offered when captured, so we never render an empty panel.
func (d *DebugScreen) availableLenses() []debugLens {
	lenses := []debugLens{lensOverview, lensRequest}
	if d.selectedReq >= 0 && d.selectedReq < len(d.requests) {
		req := d.requests[d.selectedReq]
		if req.ProviderJSON != nil {
			lenses = append(lenses, lensProvider)
		}
		if len(req.Envelopes) > 0 {
			lenses = append(lenses, lensEvents)
		}
	}
	return lenses
}

// setLens opens the Requests inspector detail view on the requested lens, but
// only if that lens is available for the selected request. Unavailable lenses
// are a no-op, so pressing e/p on a request without events/provider data does
// nothing rather than showing a blank pane.
func (d *DebugScreen) setLens(l debugLens) {
	if d.activeTab != DebugTabRequests {
		return
	}
	for _, avail := range d.availableLenses() {
		if avail == l {
			d.detailOpen = true
			d.lens = l
			d.resetScrollOffset()
			return
		}
	}
}

// scrollOffset returns the scroll offset for the current tab
func (d *DebugScreen) scrollOffset() int {
	return d.tabScrollOffsets[d.activeTab]
}

// setScrollOffset sets the scroll offset for the current tab
func (d *DebugScreen) setScrollOffset(offset int) {
	if offset < 0 {
		offset = 0
	}
	d.tabScrollOffsets[d.activeTab] = offset
}

// resetScrollOffset resets the scroll offset for the current tab to 0
func (d *DebugScreen) resetScrollOffset() {
	d.tabScrollOffsets[d.activeTab] = 0
}

// Show displays the debug screen
func (d *DebugScreen) Show() {
	d.visible = true
	SetDebugScreenActive(true)
	// Auto-select last request if available
	if len(d.requests) > 0 && d.autoScroll {
		d.selectedReq = len(d.requests) - 1
	}
}

// Hide closes the debug screen
func (d *DebugScreen) Hide() {
	d.visible = false
	SetDebugScreenActive(false)
}

// ShowContextTab opens the debug screen directly on the Context tab (the live
// context-composition audit). It backs the /context (/audit) slash command so
// users can jump straight to "where the tokens go — incl. hidden/ephemeral"
// without opening the debug screen and pressing 4.
func (d *DebugScreen) ShowContextTab() {
	d.Show()
	d.activeTab = DebugTabContext
	d.contextMu.Lock()
	if len(d.contextCalls) > 0 && d.autoScroll {
		d.selectedContextCall = len(d.contextCalls) - 1
	}
	d.contextMu.Unlock()
	// Reset this tab's scroll so the headline (grand total + hidden summary) is
	// visible from the top.
	if d.tabScrollOffsets != nil {
		d.tabScrollOffsets[DebugTabContext] = 0
	}
}

// contextAuditRequestAvailable reports whether there is a paired provider-call
// snapshot the Context tab can analyze yet.
func (d *DebugScreen) contextAuditRequestAvailable() bool {
	d.contextMu.RLock()
	defer d.contextMu.RUnlock()
	return len(d.contextCalls) > 0
}

// Toggle shows/hides the debug screen
func (d *DebugScreen) Toggle() {
	if d.visible {
		d.Hide()
	} else {
		d.Show()
	}
}

// IsVisible returns whether the debug screen is shown
func (d *DebugScreen) IsVisible() bool {
	return d.visible
}

// SetSize updates the debug screen dimensions
func (d *DebugScreen) SetSize(width, height int) {
	d.width = width
	d.height = height
	// Update enhanced views size
	d.enhancedLogsView.SetSize(width-4, height-6)
	d.enhancedLogsView.SetTheme(d.theme)
	d.enhancedRawEventsView.SetSize(width-4, height-6)
	d.enhancedRawEventsView.SetTheme(d.theme)
}

// AddRequest captures a new API request for debugging.
// It returns the index of the newly added request in d.requests so callers can
// later attach the response/provider JSON to the exact same entry, even if other
// requests are appended in between (e.g. nested hooks/agents/goal-continuation
// calls). Use requestPtr(index) to safely fetch the entry afterwards.
func (d *DebugScreen) AddRequest(req DebugRequest) int {
	req.ID = fmt.Sprintf("req_%d_%d", time.Now().Unix(), len(d.requests))
	d.requests = append(d.requests, req)
	index := len(d.requests) - 1

	if d.autoScroll {
		d.selectedReq = index
		d.resetScrollOffset()
	}

	logDebug("[Debug] Captured request: %s (msg %d, %dms)", req.ID, req.MessageIndex, req.Duration.Milliseconds())
	return index
}

// AddContextCalls appends completed provider-call captures without mixing them
// into the ordinary request inspector's synthetic HTTP/debug records.
func (d *DebugScreen) AddContextCalls(calls []DebugContextCall) {
	if len(calls) == 0 {
		return
	}
	d.contextMu.Lock()
	defer d.contextMu.Unlock()
	d.contextCalls = append(d.contextCalls, calls...)
	// AddContextCalls may run off the Bubble Tea update goroutine. Touch only
	// context-owned state here; tab/scroll state remains on the UI goroutine.
	d.selectedContextCall = len(d.contextCalls) - 1
}

// requestPtr returns a pointer to the request at the given index, or nil if the
// index is out of range. Use this to attach response data to the exact request
// captured by AddRequest rather than blindly targeting the last entry.
func (d *DebugScreen) requestPtr(index int) *DebugRequest {
	if index < 0 || index >= len(d.requests) {
		return nil
	}
	return &d.requests[index]
}

// AddLog adds a raw log message
func (d *DebugScreen) AddLog(message string) {
	timestamp := time.Now().Format("15:04:05.000")
	d.enhancedLogsView.AddLog(fmt.Sprintf("[%s] %s", timestamp, message))
}

// AddRawEvent adds a raw API event to the debug screen (for Raw Events tab)
func (d *DebugScreen) AddRawEvent(eventType string, rawData string) {
	// Process through Universal Envelope system
	var env *envelope.Envelope
	if d.envelopeHandler != nil {
		// Use a background context for debug processing
		ctx := context.Background()

		// Map TUI event types to Envelope event types if needed
		envType := envelope.EventTypeSSE
		if eventType == "request" {
			envType = envelope.EventTypeMessage
		}

		// Process the event
		var err error
		env, err = d.envelopeHandler.Process(ctx, envelope.ProviderAnthropic, envType, []byte(rawData))
		if err == nil && len(d.requests) > 0 {
			// Associate with the most recent request
			lastReq := &d.requests[len(d.requests)-1]
			lastReq.Envelopes = append(lastReq.Envelopes, env)
		}
	}

	// Add to enhanced raw events view (handles auto-scroll internally)
	d.enhancedRawEventsView.AddEvent(eventType, rawData, env)

	// Log to regular debug log too for cross-reference
	d.AddLog(fmt.Sprintf("[RAW_API_EVENT] Type=%s, Data=%s", eventType, rawData))
}

// copyCurrentTabContent copies the current tab's content to ALL clipboards
func (d *DebugScreen) copyCurrentTabContent() {
	var content strings.Builder

	switch d.activeTab {
	case DebugTabRequests:
		// Copy all request summaries
		for i, req := range d.requests {
			statusText := "OK"
			if req.Error != "" {
				statusText = "ERROR"
			}
			content.WriteString(fmt.Sprintf("[REQ#%d] %s | %s %s | HTTP %d | %dms | IN:%dtok OUT:%dtok | %s\n",
				i, statusText, req.Method, req.URL, req.ResponseCode,
				req.Duration.Milliseconds(), req.TokensInput, req.TokensOutput, req.Model))
			if req.Error != "" {
				content.WriteString(fmt.Sprintf("       ERROR: %s\n", req.Error))
			}
		}
		// When a request is selected, append its full detail (metadata,
		// request/response bodies, provider JSON) so a single copy captures
		// everything the inspector lenses can show for that request.
		if d.selectedReq >= 0 && d.selectedReq < len(d.requests) {
			req := d.requests[d.selectedReq]
			content.WriteString(fmt.Sprintf("\n========== REQUEST #%d DETAIL ==========\n\n", d.selectedReq))
			content.WriteString(d.formatRequestDetails(req))
			content.WriteString("\n--- REQUEST BODY ---\n\n")
			if req.Body != "" {
				if d.jsonPretty {
					content.WriteString(prettyJSON(req.Body))
				} else {
					content.WriteString(req.Body)
				}
			}
			content.WriteString("\n\n--- RESPONSE BODY ---\n\n")
			if req.Response != "" {
				if d.jsonPretty {
					content.WriteString(prettyJSON(req.Response))
				} else {
					content.WriteString(req.Response)
				}
			} else if req.Error != "" {
				content.WriteString(fmt.Sprintf("ERROR: %s", req.Error))
			}
			if req.ProviderJSON != nil {
				content.WriteString("\n\n--- PROVIDER JSON (API Format) ---\n\n")
				content.WriteString(*req.ProviderJSON)
			}
			content.WriteString("\n")
		}

	case DebugTabLogs:
		// Copy all logs (filtered)
		allLogs := d.enhancedLogsView.buffer.GetAll()
		for _, entry := range allLogs {
			if d.enhancedLogsView.filter.Matches(entry) {
				content.WriteString(entry.Raw + "\n")
			}
		}
	}

	// Copy to ALL clipboards
	if content.String() != "" {
		successCount := writeClipboard(content.String())

		// Also write to file as backup
		homeDir, _ := os.UserHomeDir()
		filePath := filepath.Join(homeDir, "swarmos_debug_copy.txt")
		if err := os.WriteFile(filePath, []byte(content.String()), 0644); err == nil {
			logDebug("✓ Copied to %d clipboard(s) + file: %s", successCount, filePath)
		} else {
			logDebug("✓ Copied to %d clipboard(s)", successCount)
		}
	}
}

// exportAllLogs exports all logs to a timestamped file AND all clipboards
func (d *DebugScreen) exportAllLogs() {
	homeDir, _ := os.UserHomeDir()
	timestamp := time.Now().Format("20060102_150405")
	filePath := filepath.Join(homeDir, fmt.Sprintf("swarmos_debug_export_%s.txt", timestamp))
	jsonFilePath := filepath.Join(homeDir, fmt.Sprintf("swarmos_debug_export_%s.json", timestamp))

	var content strings.Builder
	content.WriteString("========== SWARMOS DEBUG EXPORT ==========\n")
	content.WriteString(fmt.Sprintf("Exported: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// Token growth analysis section
	content.WriteString("========== TOKEN GROWTH ANALYSIS ==========\n\n")
	if len(d.requests) > 0 {
		content.WriteString(fmt.Sprintf("Total Requests: %d\n\n", len(d.requests)))
		content.WriteString("Request # | Version | Msgs | Input Tokens | Growth  | Removed\n")
		content.WriteString("----------|---------|------|--------------|---------|--------\n")

		prevInput := 0
		totalRemoved := 0
		for i, req := range d.requests {
			growth := req.TokensInput - prevInput
			growthStr := fmt.Sprintf("%+d", growth)
			if i == 0 {
				growthStr = "(base)"
			}
			removedStr := ""
			if len(req.MessagesRemoved) > 0 {
				removedStr = fmt.Sprintf("%d", len(req.MessagesRemoved))
				totalRemoved += len(req.MessagesRemoved)
			}
			content.WriteString(fmt.Sprintf("%-9d | %-7d | %-4d | %-12d | %-7s | %s\n",
				i, req.ConversationVersion, req.MessageCount, req.TokensInput, growthStr, removedStr))
			prevInput = req.TokensInput
		}
		content.WriteString("\n")

		// Summary
		if len(d.requests) > 1 {
			first := d.requests[0]
			last := d.requests[len(d.requests)-1]
			totalGrowth := last.TokensInput - first.TokensInput
			avgGrowth := totalGrowth / (len(d.requests) - 1)
			content.WriteString(fmt.Sprintf("First request input:  %d tokens\n", first.TokensInput))
			content.WriteString(fmt.Sprintf("Last request input:   %d tokens\n", last.TokensInput))
			content.WriteString(fmt.Sprintf("Total growth:         %d tokens\n", totalGrowth))
			content.WriteString(fmt.Sprintf("Avg growth/request:   %d tokens\n", avgGrowth))
			content.WriteString(fmt.Sprintf("Final message count:  %d\n", last.MessageCount))
			content.WriteString(fmt.Sprintf("Final version:        %d\n", last.ConversationVersion))
			if totalRemoved > 0 {
				content.WriteString(fmt.Sprintf("Total msgs trimmed:   %d\n", totalRemoved))
			}
			if avgGrowth > 1000 {
				content.WriteString("\n⚠️ High average growth! Possible causes:\n")
				content.WriteString("   - Extended thinking enabled (stores thinking blocks in history)\n")
				content.WriteString("   - Large tool outputs (file contents, grep results)\n")
				content.WriteString("   - Verbose assistant responses\n")
			}
		}
	}

	// Export all requests with full details
	content.WriteString("\n\n========== REQUESTS ==========\n\n")
	for i, req := range d.requests {
		content.WriteString(fmt.Sprintf("\n--- REQUEST #%d ---\n", i))
		content.WriteString(d.formatRequestDetails(req))
		content.WriteString("\n")
	}

	// Export all logs
	content.WriteString("\n========== LOGS ==========\n\n")
	allLogs := d.enhancedLogsView.buffer.GetAll()
	for _, entry := range allLogs {
		content.WriteString(entry.Raw + "\n")
	}

	// Copy to ALL clipboards
	successCount := writeClipboard(content.String())

	// Write text file
	if err := os.WriteFile(filePath, []byte(content.String()), 0644); err == nil {
		logDebug("✓ Full export: %d clipboard(s) + file: %s", successCount, filePath)
	} else {
		logDebug("✓ Full export: %d clipboard(s)", successCount)
	}

	// Also write a JSON file for easier parsing
	jsonExport := d.buildJSONExport()
	if jsonData, err := json.MarshalIndent(jsonExport, "", "  "); err == nil {
		if err := os.WriteFile(jsonFilePath, jsonData, 0644); err == nil {
			logDebug("✓ JSON export: %s", jsonFilePath)
		}
	}
}

// buildJSONExport creates a structured JSON export for analysis
func (d *DebugScreen) buildJSONExport() map[string]any {
	requests := make([]map[string]any, 0, len(d.requests))
	prevInput := 0

	for i, req := range d.requests {
		growth := req.TokensInput - prevInput
		if i == 0 {
			growth = 0
		}

		reqMap := map[string]any{
			"request_num":   i,
			"timestamp":     req.Timestamp.Format(time.RFC3339Nano),
			"model":         req.Model,
			"duration_ms":   req.Duration.Milliseconds(),
			"input_tokens":  req.TokensInput,
			"output_tokens": req.TokensOutput,
			"token_growth":  growth,
			"status_code":   req.ResponseCode,
		}

		if req.Error != "" {
			reqMap["error"] = req.Error
		}

		// Include request body (parsed JSON if possible)
		if req.Body != "" {
			var bodyObj any
			if err := json.Unmarshal([]byte(req.Body), &bodyObj); err == nil {
				reqMap["request_body"] = bodyObj
			} else {
				reqMap["request_body_raw"] = req.Body
			}
		}

		// Include response body (parsed JSON if possible)
		if req.Response != "" {
			var respObj any
			if err := json.Unmarshal([]byte(req.Response), &respObj); err == nil {
				reqMap["response_body"] = respObj
			} else {
				reqMap["response_body_raw"] = req.Response
			}
		}

		if len(req.Metadata) > 0 {
			reqMap["metadata"] = req.Metadata
		}

		// Include conversation versioning and state tracking
		reqMap["conversation_version"] = req.ConversationVersion
		reqMap["message_count"] = req.MessageCount

		if len(req.MessageHashes) > 0 {
			reqMap["message_hashes"] = req.MessageHashes
		}

		if len(req.MessagesAdded) > 0 {
			reqMap["messages_added"] = req.MessagesAdded
		}

		if len(req.MessagesRemoved) > 0 {
			reqMap["messages_removed"] = req.MessagesRemoved
		}

		if req.ConversationState != nil {
			reqMap["conversation_state"] = req.ConversationState
		}

		// Include canonical and provider JSONs if available
		if req.CanonicalJSON != nil {
			var canonicalObj any
			if err := json.Unmarshal([]byte(*req.CanonicalJSON), &canonicalObj); err == nil {
				reqMap["canonical_json"] = canonicalObj
			} else {
				reqMap["canonical_json_raw"] = *req.CanonicalJSON
			}
		}

		if req.ProviderJSON != nil {
			var providerObj any
			if err := json.Unmarshal([]byte(*req.ProviderJSON), &providerObj); err == nil {
				reqMap["provider_json"] = providerObj
			} else {
				reqMap["provider_json_raw"] = *req.ProviderJSON
			}
		}

		requests = append(requests, reqMap)
		prevInput = req.TokensInput
	}

	// Build summary
	summary := map[string]any{
		"total_requests": len(d.requests),
		"exported_at":    time.Now().Format(time.RFC3339),
	}

	if len(d.requests) > 1 {
		first := d.requests[0]
		last := d.requests[len(d.requests)-1]
		summary["first_input_tokens"] = first.TokensInput
		summary["last_input_tokens"] = last.TokensInput
		summary["total_token_growth"] = last.TokensInput - first.TokensInput
		summary["avg_growth_per_request"] = (last.TokensInput - first.TokensInput) / (len(d.requests) - 1)

		// Conversation version tracking
		summary["final_conversation_version"] = last.ConversationVersion
		summary["final_message_count"] = last.MessageCount

		// Count total messages removed (trimmed) across all requests
		totalRemoved := 0
		for _, req := range d.requests {
			totalRemoved += len(req.MessagesRemoved)
		}
		if totalRemoved > 0 {
			summary["total_messages_trimmed"] = totalRemoved
		}
	}

	return map[string]any{
		"summary":  summary,
		"requests": requests,
	}
}

// formatRequestDetails formats a request's full details
func (d *DebugScreen) formatRequestDetails(req DebugRequest) string {
	var b strings.Builder

	b.WriteString("========== REQUEST OVERVIEW ==========\n\n")
	b.WriteString(fmt.Sprintf("ID:            %s\n", req.ID))
	b.WriteString(fmt.Sprintf("Message Index: %d\n", req.MessageIndex))
	b.WriteString(fmt.Sprintf("Timestamp:     %s\n", req.Timestamp.Format("2006-01-02 15:04:05.000")))
	b.WriteString(fmt.Sprintf("Method:        %s\n", req.Method))
	b.WriteString(fmt.Sprintf("URL:           %s\n", req.URL))
	b.WriteString(fmt.Sprintf("Duration:      %dms\n", req.Duration.Milliseconds()))
	b.WriteString(fmt.Sprintf("Status Code:   %d\n", req.ResponseCode))
	b.WriteString(fmt.Sprintf("Model:         %s\n", req.Model))
	b.WriteString(fmt.Sprintf("Tokens In:     %d\n", req.TokensInput))
	b.WriteString(fmt.Sprintf("Tokens Out:    %d\n", req.TokensOutput))

	// Calculate token growth from previous request
	reqIndex := -1
	for i, r := range d.requests {
		if r.ID == req.ID {
			reqIndex = i
			break
		}
	}
	if reqIndex > 0 {
		prevReq := d.requests[reqIndex-1]
		inputGrowth := req.TokensInput - prevReq.TokensInput
		b.WriteString("\n--- Token Growth from Previous Request ---\n")
		b.WriteString(fmt.Sprintf("Prev Input:    %d tokens\n", prevReq.TokensInput))
		b.WriteString(fmt.Sprintf("Curr Input:    %d tokens\n", req.TokensInput))
		b.WriteString(fmt.Sprintf("Growth:        %+d tokens\n", inputGrowth))
		if inputGrowth > 1000 {
			b.WriteString("⚠️ Large growth! Check for thinking content or large tool outputs.\n")
		}
	}

	if req.Error != "" {
		b.WriteString(fmt.Sprintf("Error:         %s\n", req.Error))
	}

	b.WriteString("\n========== REQUEST HEADERS ==========\n\n")
	for key, value := range req.Headers {
		displayValue := value
		if d.showSanitized && (strings.Contains(strings.ToLower(key), "key") || strings.Contains(strings.ToLower(key), "auth")) {
			if len(value) > 12 {
				displayValue = value[:4] + "..." + value[len(value)-4:]
			}
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", key, displayValue))
	}

	if len(req.Metadata) > 0 {
		b.WriteString("\n========== METADATA ==========\n\n")
		for key, value := range req.Metadata {
			b.WriteString(fmt.Sprintf("%s: %v\n", key, value))
		}
	}

	// Conversation version and state tracking
	b.WriteString("\n========== CONVERSATION STATE ==========\n\n")
	b.WriteString(fmt.Sprintf("Version:       %d\n", req.ConversationVersion))
	b.WriteString(fmt.Sprintf("Message Count: %d\n", req.MessageCount))

	if len(req.MessagesAdded) > 0 {
		b.WriteString(fmt.Sprintf("Added:         %v\n", req.MessagesAdded))
	}

	if len(req.MessagesRemoved) > 0 {
		b.WriteString(fmt.Sprintf("⚠️ Removed:    %v\n", req.MessagesRemoved))
	}

	if req.ConversationState != nil {
		b.WriteString("\nSnapshot:\n")
		b.WriteString(fmt.Sprintf("  System Prompt: %d chars\n", req.ConversationState.SystemPromptLength))
		b.WriteString(fmt.Sprintf("  Tools:         %d\n", req.ConversationState.ToolCount))
		b.WriteString(fmt.Sprintf("  Content Total: %d chars\n", req.ConversationState.TotalContentLength))
		b.WriteString("  Messages:\n")
		for _, msg := range req.ConversationState.Messages {
			flags := ""
			if msg.HasToolCalls {
				flags += "[tools] "
			}
			if msg.HasToolResults {
				flags += "[results] "
			}
			if msg.HasThinking {
				flags += "[thinking] "
			}
			b.WriteString(fmt.Sprintf("    [%d] %s: %d chars (hash:%s) %s\n",
				msg.Index, msg.Role, msg.ContentLength, msg.ContentHash, flags))
		}
	}

	b.WriteString("\n========== REQUEST BODY ==========\n\n")
	if req.Body != "" {
		bodySize := len(req.Body)
		b.WriteString(fmt.Sprintf("Size: %d bytes\n\n", bodySize))
		if d.jsonPretty {
			b.WriteString(prettyJSON(req.Body))
		} else {
			b.WriteString(req.Body)
		}
	} else {
		b.WriteString("(no request body)\n")
	}

	b.WriteString("\n\n========== RESPONSE BODY ==========\n\n")
	if req.Response != "" {
		respSize := len(req.Response)
		b.WriteString(fmt.Sprintf("Size: %d bytes\n\n", respSize))
		if d.jsonPretty {
			b.WriteString(prettyJSON(req.Response))
		} else {
			b.WriteString(req.Response)
		}
	} else if req.Error != "" {
		b.WriteString(fmt.Sprintf("ERROR: %s\n", req.Error))
	} else {
		b.WriteString("(no response body)\n")
	}

	return b.String()
}

// Update handles keyboard input for the debug screen
func (d *DebugScreen) Update(msg tea.Msg) tea.Cmd {
	if !d.visible {
		return nil
	}

	// If in Logs tab, delegate to specialized handler
	if d.activeTab == DebugTabLogs {
		return d.updateLogsTab(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// In the Requests inspector, esc backs out of the detail view first.
			if d.activeTab == DebugTabRequests && d.detailOpen {
				d.detailOpen = false
				d.resetScrollOffset()
				return nil
			}
			d.Hide()
			return nil
		case "q":
			d.Hide()
			return nil

		case "tab":
			d.switchTab(1)
			return d.maybeActivateUsage()
		case "shift+tab":
			d.switchTab(-1)
			return d.maybeActivateUsage()

		case "1":
			d.activeTab = DebugTabRequests
			d.detailOpen = false
			d.resetScrollOffset()
		case "2":
			d.activeTab = DebugTabLogs
			d.detailOpen = false
			d.resetScrollOffset()
		case "3":
			d.activeTab = DebugTabUsage
			d.detailOpen = false
			d.resetScrollOffset()
			return d.maybeActivateUsage()
		case "4":
			d.activeTab = DebugTabContext
			d.detailOpen = false
			d.resetScrollOffset()

		// Detail-lens selection inside the Requests inspector. Each is a no-op
		// unless the lens has data for the selected request (see setLens).
		case "o":
			d.setLens(lensOverview)
		case "e":
			d.setLens(lensEvents)

		case "enter", "l", "right":
			if d.activeTab == DebugTabRequests {
				if !d.detailOpen && len(d.requests) > 0 {
					d.detailOpen = true
					d.lens = lensOverview
					d.resetScrollOffset()
				}
			} else if d.activeTab == DebugTabUsage && d.usageSubTabNext != nil {
				d.usageSubTabNext()
				d.resetScrollOffset()
			}

		case "h", "left":
			if d.activeTab == DebugTabRequests && d.detailOpen {
				d.detailOpen = false
				d.resetScrollOffset()
			} else if d.activeTab == DebugTabUsage && d.usageSubTabPrev != nil {
				d.usageSubTabPrev()
				d.resetScrollOffset()
			}

		case "j", "down":
			if d.activeTab == DebugTabRequests && !d.detailOpen {
				if d.selectedReq < len(d.requests)-1 {
					d.selectedReq++
					d.autoScroll = false
					d.resetScrollOffset() // Reset scroll when changing request
				}
			} else {
				d.setScrollOffset(d.scrollOffset() + 3) // Scroll multiple lines for faster navigation
			}

		case "k", "up":
			if d.activeTab == DebugTabRequests && !d.detailOpen {
				if d.selectedReq > 0 {
					d.selectedReq--
					d.autoScroll = false
					d.resetScrollOffset() // Reset scroll when changing request
				}
			} else {
				if d.scrollOffset() > 0 {
					d.setScrollOffset(d.scrollOffset() - 3) // Scroll multiple lines for faster navigation
					if d.scrollOffset() < 0 {
						d.resetScrollOffset()
					}
				}
			}

		case "g":
			if d.activeTab == DebugTabRequests && !d.detailOpen {
				d.selectedReq = 0
			} else {
				d.resetScrollOffset()
			}

		case "G":
			if d.activeTab == DebugTabRequests && !d.detailOpen {
				if len(d.requests) > 0 {
					d.selectedReq = len(d.requests) - 1
				}
			} else {
				// Scroll to bottom of the current detail/log/usage view.
				d.setScrollOffset(999999) // Large number to force to bottom
			}

		case "ctrl+d":
			// Page down
			d.setScrollOffset(d.scrollOffset() + 10)

		case "ctrl+u":
			// Page up
			d.setScrollOffset(d.scrollOffset() - 10)
			if d.scrollOffset() < 0 {
				d.resetScrollOffset()
			}

		case "[":
			// On the Context tab, step to the previous captured turn (request).
			if d.activeTab == DebugTabContext {
				d.stepContextRequest(-1)
			}

		case "]":
			// On the Context tab, step to the next captured turn (request).
			if d.activeTab == DebugTabContext {
				d.stepContextRequest(1)
			}

		case "r":
			// Request lens inside the inspector; otherwise refresh Usage.
			if d.activeTab == DebugTabRequests && d.detailOpen {
				d.setLens(lensRequest)
			} else if d.activeTab == DebugTabUsage && d.usageActivate != nil {
				return d.usageActivate()
			}

		case "p":
			// Provider lens inside the inspector; otherwise toggle pretty JSON.
			if d.activeTab == DebugTabRequests && d.detailOpen {
				d.setLens(lensProvider)
			} else {
				d.jsonPretty = !d.jsonPretty
			}

		case "P":
			d.jsonPretty = !d.jsonPretty

		case "s":
			d.showSanitized = !d.showSanitized

		case "a":
			d.autoScroll = !d.autoScroll

		case "c":
			// Clear all requests
			d.requests = make([]DebugRequest, 0)
			d.contextMu.Lock()
			d.contextCalls = make([]DebugContextCall, 0)
			d.selectedContextCall = -1
			d.contextMu.Unlock()
			d.selectedReq = 0
			d.detailOpen = false
			d.resetScrollOffset()

		case "y":
			// Copy current tab content to file
			d.copyCurrentTabContent()

		case "Y":
			// Export ALL logs to timestamped file
			d.exportAllLogs()
		}
	}

	return nil
}

// updateLogsTab handles input specifically for the Logs tab
func (d *DebugScreen) updateLogsTab(msg tea.Msg) tea.Cmd {
	// If filter panel is visible, it gets priority
	if d.enhancedLogsView.filterPanel.IsVisible() {
		return d.enhancedLogsView.Update(msg)
	}

	// While the inline '/' search prompt is open, every key edits the query
	// (including digits/letters) — route them all to the logs view.
	if d.enhancedLogsView.searching {
		return d.enhancedLogsView.Update(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			d.Hide()
			return nil

		case "tab":
			d.switchTab(1)
			return d.maybeActivateUsage()
		case "shift+tab":
			d.switchTab(-1)
			return d.maybeActivateUsage()

		case "1":
			d.activeTab = DebugTabRequests
			d.detailOpen = false
			d.resetScrollOffset()
		case "2":
			d.activeTab = DebugTabLogs
			d.resetScrollOffset()
		case "3":
			d.activeTab = DebugTabUsage
			d.resetScrollOffset()
			return d.maybeActivateUsage()
		case "4":
			d.activeTab = DebugTabContext
			d.resetScrollOffset()

		// Forward scrolling keys to enhanced logs view
		case "j", "down", "k", "up", "ctrl+d", "ctrl+u", "g", "G":
			return d.enhancedLogsView.Update(msg)

		case "f":
			// Toggle filter panel
			d.enhancedLogsView.ToggleFilterPanel()

		case "/":
			// Open inline search prompt
			d.enhancedLogsView.StartSearch()

		case "n":
			// Jump to next search match
			d.enhancedLogsView.NextMatch()

		case "N":
			// Jump to previous search match
			d.enhancedLogsView.PrevMatch()

		case "e":
			// One-key severity cycle: ALL -> INFO+ -> WARN+ -> ERROR+ -> ALL
			d.enhancedLogsView.CycleSeverity()

		case "r":
			// Reset all filters (levels, categories, search) back to defaults
			d.enhancedLogsView.filter = NewLogFilter()
			d.enhancedLogsView.ResetSeverity()
			d.enhancedLogsView.ClearSearch()

		case "l":
			// Clear logs
			d.enhancedLogsView.ClearLogs()

		case "y":
			// Copy logs
			d.copyCurrentTabContent()

		case "Y":
			// Export all
			d.exportAllLogs()

		case "+", "=":
			// Increase buffer capacity
			currentCap := d.enhancedLogsView.buffer.Capacity()
			d.enhancedLogsView.SetBufferCapacity(currentCap + 1000)
			logDebug("Increased log buffer capacity to %d", currentCap+1000)

		case "-":
			// Decrease buffer capacity
			currentCap := d.enhancedLogsView.buffer.Capacity()
			if currentCap > 1000 {
				d.enhancedLogsView.SetBufferCapacity(currentCap - 1000)
				logDebug("Decreased log buffer capacity to %d", currentCap-1000)
			}
		}
	}

	return nil
}

// View renders the debug screen
func prettyJSON(jsonStr string) string {
	var obj any
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return jsonStr
	}

	pretty, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return jsonStr
	}

	return string(pretty)
}

// truncate truncates a string to maxLen with ellipsis
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
