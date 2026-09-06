package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// ComprehensiveTrace captures the complete transformation flow like Claude Code does
// This logs every event at every stage so you can recreate the JSON 1:1
type ComprehensiveTrace struct {
	mu          sync.Mutex
	events      []TraceEvent
	traceDir    string
	enabled     bool
	sessionID   string
	messageID   string
	sequenceNum int
}

// TraceEvent represents a single event in the complete flow
type TraceEvent struct {
	// When & where
	Timestamp   string `json:"timestamp"`
	SequenceNum int    `json:"sequence"`
	Stage       string `json:"stage"` // "raw_sse", "stream_chunk", "canonical", "final_message"
	EventType   string `json:"type"`  // "message_start", "content_block_delta", "message_delta", etc

	// Raw data from each stage
	RawSSE            any `json:"raw_sse,omitempty"`            // Direct from Anthropic
	StreamChunk       any `json:"stream_chunk,omitempty"`       // Provider stream format
	CanonicalResponse any `json:"canonical_response,omitempty"` // ChatResponse
	FinalMessage      any `json:"final_message,omitempty"`      // conversation.Message
	AccumulatedState  any `json:"accumulated_state,omitempty"`  // Full state at this point

	// Cache-specific
	CacheMetrics any `json:"cache_metrics,omitempty"`
	TokenUsage   any `json:"token_usage,omitempty"`

	// For reconstruction
	ParentUUID string `json:"parent_uuid,omitempty"`
	MessageID  string `json:"message_id,omitempty"`
}

// NewComprehensiveTrace creates a new tracer
func NewComprehensiveTrace(sessionID, messageID string) *ComprehensiveTrace {
	traceDir := filepath.Join(os.TempDir(), "tui-trace", sessionID)
	os.MkdirAll(traceDir, 0755)

	enabled := os.Getenv("TRACE_ENABLED") != "" || os.Getenv("CACHE_DEBUG") != ""

	return &ComprehensiveTrace{
		traceDir:    traceDir,
		enabled:     enabled,
		sessionID:   sessionID,
		messageID:   messageID,
		sequenceNum: 0,
	}
}

// LogRawSSEEvent logs raw data from Anthropic API
func (ct *ComprehensiveTrace) LogRawSSEEvent(eventType, rawData string) {
	if !ct.enabled {
		return
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sequenceNum++

	var data any
	json.Unmarshal([]byte(rawData), &data)

	event := TraceEvent{
		Timestamp:   time.Now().Format(time.RFC3339Nano),
		SequenceNum: ct.sequenceNum,
		Stage:       "raw_sse",
		EventType:   eventType,
		RawSSE:      data,
		MessageID:   ct.messageID,
	}

	ct.events = append(ct.events, event)
	ct.writeEventFile(event)
}

// LogStreamChunk logs the stream chunk after initial processing
func (ct *ComprehensiveTrace) LogStreamChunk(chunkData any, state any) {
	if !ct.enabled {
		return
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sequenceNum++

	event := TraceEvent{
		Timestamp:        time.Now().Format(time.RFC3339Nano),
		SequenceNum:      ct.sequenceNum,
		Stage:            "stream_chunk",
		StreamChunk:      chunkData,
		AccumulatedState: state,
		MessageID:        ct.messageID,
	}

	ct.events = append(ct.events, event)
	ct.writeEventFile(event)
}

// LogCanonical logs the canonical provider format
func (ct *ComprehensiveTrace) LogCanonical(resp any, accumulatedState any) {
	if !ct.enabled {
		return
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sequenceNum++

	event := TraceEvent{
		Timestamp:         time.Now().Format(time.RFC3339Nano),
		SequenceNum:       ct.sequenceNum,
		Stage:             "canonical",
		CanonicalResponse: resp,
		AccumulatedState:  accumulatedState,
		MessageID:         ct.messageID,
	}

	ct.events = append(ct.events, event)
	ct.writeEventFile(event)
}

// LogFinalMessage logs the final conversation.Message
func (ct *ComprehensiveTrace) LogFinalMessage(msg *conversation.Message) {
	if !ct.enabled {
		return
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sequenceNum++

	event := TraceEvent{
		Timestamp:    time.Now().Format(time.RFC3339Nano),
		SequenceNum:  ct.sequenceNum,
		Stage:        "final_message",
		FinalMessage: msg,
		MessageID:    ct.messageID,
	}

	ct.events = append(ct.events, event)
	ct.writeEventFile(event)
}

// LogCacheMetrics logs cache-specific data at any point
func (ct *ComprehensiveTrace) LogCacheMetrics(metrics any, stage string) {
	if !ct.enabled {
		return
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sequenceNum++

	event := TraceEvent{
		Timestamp:    time.Now().Format(time.RFC3339Nano),
		SequenceNum:  ct.sequenceNum,
		Stage:        stage,
		CacheMetrics: metrics,
		MessageID:    ct.messageID,
	}

	ct.events = append(ct.events, event)
	ct.writeEventFile(event)
}

// writeEventFile writes individual event to file
func (ct *ComprehensiveTrace) writeEventFile(event TraceEvent) {
	filename := filepath.Join(ct.traceDir, fmt.Sprintf("%03d_%s_%s.json", event.SequenceNum, event.Stage, event.EventType))
	data, _ := json.MarshalIndent(event, "", "  ")
	os.WriteFile(filename, data, 0644)
}

// WriteCompleteTrace writes the full trace as NDJSON (like Claude Code)
func (ct *ComprehensiveTrace) WriteCompleteTrace() string {
	if !ct.enabled || len(ct.events) == 0 {
		return ""
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	filename := filepath.Join(ct.traceDir, fmt.Sprintf("complete_trace_%s.ndjson", time.Now().Format("20060102_150405")))

	file, _ := os.Create(filename)
	defer file.Close()

	// Write as newline-delimited JSON
	for _, event := range ct.events {
		data, _ := json.Marshal(event)
		file.WriteString(string(data) + "\n")
	}

	return filename
}

// GetTraceDir returns the trace directory
func (ct *ComprehensiveTrace) GetTraceDir() string {
	return ct.traceDir
}

// GetEventCount returns number of events logged
func (ct *ComprehensiveTrace) GetEventCount() int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return len(ct.events)
}

// GetAllEvents returns all events (for analysis)
func (ct *ComprehensiveTrace) GetAllEvents() []TraceEvent {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	result := make([]TraceEvent, len(ct.events))
	copy(result, ct.events)
	return result
}

// Reconstruction describes how to recreate the JSON
func (ct *ComprehensiveTrace) Reconstruction() string {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	var report strings.Builder
	report.WriteString(fmt.Sprintf(`
=== COMPLETE TRACE RECONSTRUCTION GUIDE ===
Session ID: %s
Message ID: %s
Total Events: %d
Trace Directory: %s

TO RECREATE THE JSON 1:1:
1. Read events in sequence order (sequence field)
2. Each event shows the transformation stage
3. Stage order: raw_sse → stream_chunk → canonical → final_message
4. Use 'parent_uuid' field to link events together
5. Accumulate state across events to rebuild full message

EVENTS RECORDED:
`, ct.sessionID, ct.messageID, len(ct.events), ct.traceDir))

	for i, evt := range ct.events {
		report.WriteString(fmt.Sprintf("%d. Seq %d: %s/%s @ %s\n",
			i+1, evt.SequenceNum, evt.Stage, evt.EventType, evt.Timestamp))
	}

	report.WriteString(fmt.Sprintf(`
FILE LOCATIONS:
- Individual events: %s/[seq]_[stage]_[type].json
- Complete NDJSON: %s/complete_trace_*.ndjson

ANALYSIS:
- View individual transformation: cat %s/001_*.json
- Compare all raw SSE events: grep raw_sse %s/complete_trace_*.ndjson
- Follow cache metrics: grep cache_metrics %s/complete_trace_*.ndjson
- Trace final message: cat %s/*_final_message_*.json

`, ct.traceDir, ct.traceDir, ct.traceDir, ct.traceDir, ct.traceDir, ct.traceDir))

	return report.String()
}
