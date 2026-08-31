package deepwiki

// debug_log.go — optional JSONL trace of every LLM call and JSON parse attempt.
//
// Two independent modes (combinable):
//
// 1. DEEPWIKI_VERBOSE=1 — prints truncated prompt/response to stderr so they
//    appear in the IPC terminal window alongside the normal log lines.
//    Useful for live debugging without digging into a separate file.
//
// 2. DEEPWIKI_DEBUG_LOG=1  → writes full JSONL to /tmp/deepwiki-debug-<ts>.jsonl
//    DEEPWIKI_DEBUG_LOG=/path → writes full JSONL to that path
//    Inspect with: cat /tmp/deepwiki-debug-*.jsonl | jq .
//
// Both can be active at once.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// LLMCallLog is one entry written to the debug log file.
type LLMCallLog struct {
	Timestamp   string `json:"timestamp"`
	CallType    string `json:"call_type"`    // e.g. "synthesis_planner", "skeleton_pass"
	System      string `json:"system"`       // full system prompt sent to the LLM
	User        string `json:"user"`         // full user prompt sent to the LLM
	RawResponse string `json:"raw_response"` // exactly what the LLM returned
	Cleaned     string `json:"cleaned"`      // after stripJSONFences + repairJSON
	ParseOK     bool   `json:"parse_ok"`
	ParseError  string `json:"parse_error,omitempty"` // non-empty when parse_ok == false
	DurationMs  int64  `json:"duration_ms,omitempty"`
}

// verboseLog enables truncated prompt/response logging to stderr (IPC terminal).
// Activate with DEEPWIKI_VERBOSE=1 or DEEPWIKI_VERBOSE=true.
var verboseLog = func() bool {
	v := os.Getenv("DEEPWIKI_VERBOSE")
	return v == "1" || v == "true"
}()

// dlog is the package-level debug logger, initialised once from the env var.
var dlog = initDebugLog()

type debugLog struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

func initDebugLog() *debugLog {
	path := os.Getenv("DEEPWIKI_DEBUG_LOG")
	if path == "" {
		return &debugLog{} // disabled — zero overhead in production
	}
	if path == "1" || path == "true" {
		path = fmt.Sprintf("/tmp/deepwiki-debug-%s.jsonl",
			time.Now().Format("20060102-150405"))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[deepwiki debug] cannot open log %q: %v\n", path, err)
		return &debugLog{}
	}
	fmt.Fprintf(os.Stderr, "[deepwiki debug] LLM call log → %s\n", path)
	return &debugLog{f: f, path: path}
}

func (d *debugLog) enabled() bool { return d != nil && d.f != nil }

func (d *debugLog) write(entry LLMCallLog) {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format(time.RFC3339Nano)
	}

	// ── Verbose mode: emit truncated view to stderr (IPC terminal) ────────────
	if verboseLog {
		sys := truncate(entry.System, 300)
		usr := truncate(entry.User, 600)
		resp := truncate(entry.RawResponse, 1000)
		parseInfo := "ok"
		if !entry.ParseOK {
			parseInfo = "FAIL: " + entry.ParseError
		}
		log.Printf("[deepwiki/call] %-32s %5dms | parse=%s\n  SYS: %s\n  USR: %s\n  RSP: %s",
			entry.CallType, entry.DurationMs, parseInfo, sys, usr, resp)
	}

	// ── File mode: write full JSONL ───────────────────────────────────────────
	if !d.enabled() {
		return
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	d.mu.Lock()
	d.f.Write(b)
	d.f.Write([]byte("\n"))
	d.mu.Unlock()
}

// logLLMCall writes one LLMCallLog entry.  A no-op when debug logging is off.
func logLLMCall(entry LLMCallLog) { dlog.write(entry) }

// llmErrStr converts an error to its string representation (empty string for nil).
func llmErrStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
