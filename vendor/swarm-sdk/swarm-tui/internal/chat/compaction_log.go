// Package chat — compaction_log.go
//
// CompactionLog writes structured JSONL diagnostics to
// ~/.swarmos/logs/compaction.log so developers can tail/grep the file to
// correlate request/response sizes and hashes, and understand why a compaction
// request failed without persisting conversation or summary content.
//
// Usage:
//
//	cl := openCompactionLog()          // call once per compaction attempt
//	ctx  = cl.AttachToContext(ctx)     // providers call RawLogFromCtx(ctx)
//	cl.LogStart(provider, model, ...)
//	cl.LogRequest(chatReqJSON)
//	// … provider call …
//	cl.LogResponse(summary)            // or cl.LogError(err, rawBody)
package chat

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// compactionLogEntry is one JSONL line in the log file.
type compactionLogEntry struct {
	Timestamp     string         `json:"ts"`
	Event         string         `json:"event"`
	Provider      string         `json:"provider,omitempty"`
	Model         string         `json:"model,omitempty"`
	Method        string         `json:"method,omitempty"`
	MessageCount  int            `json:"message_count,omitempty"`
	ContentLen    int            `json:"content_len,omitempty"`
	StatusCode    int            `json:"status_code,omitempty"`
	BodyBytes     int            `json:"body_bytes,omitempty"`
	BodySHA256    string         `json:"body_sha256,omitempty"`
	SummaryLen    int            `json:"summary_len,omitempty"`
	SummarySHA256 string         `json:"summary_sha256,omitempty"`
	Error         string         `json:"error,omitempty"`
	ErrorSHA256   string         `json:"error_sha256,omitempty"`
	Extra         map[string]any `json:"extra,omitempty"`
}

// compactionLogFile is the package-level singleton that owns the open file
// handle.  It is lazily initialised the first time openCompactionLog() is
// called and intentionally never closed (process lifetime).
var (
	clOnce   sync.Once
	clWriter *bufio.Writer
	clMu     sync.Mutex
)

func initCompactionLogFile() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".swarmos", "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.Chmod(dir, 0o700)
	path := filepath.Join(dir, "compaction.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_ = os.Chmod(path, 0o600)
	clWriter = bufio.NewWriter(f)
}

func writeCompactionEntry(e compactionLogEntry) {
	clOnce.Do(initCompactionLogFile)
	if clWriter == nil {
		return
	}
	e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	clMu.Lock()
	defer clMu.Unlock()
	clWriter.Write(b)
	clWriter.WriteByte('\n')
	clWriter.Flush()
}

func compactionPayloadSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func compactionErrorMetadata(message string) (category, digest string) {
	if message == "" {
		return "", ""
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timeout"):
		category = "timeout"
	case strings.Contains(lower, "quota"), strings.Contains(lower, "usage limit"), strings.Contains(lower, "capacity"):
		category = "capacity"
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"):
		category = "rate_limit"
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"),
		strings.Contains(lower, "401"), strings.Contains(lower, "403"):
		category = "auth"
	case strings.Contains(lower, "context limit"), strings.Contains(lower, "too many tokens"),
		strings.Contains(lower, "prompt too long"):
		category = "context_limit"
	case strings.Contains(lower, "503"), strings.Contains(lower, "service unavailable"),
		strings.Contains(lower, "circuit open"):
		category = "service_unavailable"
	default:
		category = "other"
	}
	return category, compactionPayloadSHA256([]byte(message))
}

// ── CompactionLog ─────────────────────────────────────────────────────────────

// CompactionLog is a per-attempt helper that writes structured events and
// also implements provider.RawLogger so it can be attached to a context.
type CompactionLog struct {
	provider string
	model    string
}

// openCompactionLog returns a new CompactionLog for one compaction attempt.
// The returned value can be attached to a context and passed to provider calls.
func openCompactionLog(providerName, model string) *CompactionLog {
	return &CompactionLog{provider: providerName, model: model}
}

// AttachToContext attaches this CompactionLog as the RawLogger for all
// provider calls made with the returned context.
func (cl *CompactionLog) AttachToContext(ctx context.Context) context.Context {
	return provider.WithRawLogger(ctx, cl)
}

// LogStart records the beginning of a compaction attempt.
func (cl *CompactionLog) LogStart(messageCount, contentLen int) {
	writeCompactionEntry(compactionLogEntry{
		Event:        "compaction_start",
		Provider:     cl.provider,
		Model:        cl.model,
		MessageCount: messageCount,
		ContentLen:   contentLen,
	})
}

// LogRequest records the marshaled ChatRequest sent to the provider.
func (cl *CompactionLog) LogRequest(reqJSON []byte) {
	writeCompactionEntry(compactionLogEntry{
		Event:      "compaction_request",
		Provider:   cl.provider,
		Model:      cl.model,
		BodyBytes:  len(reqJSON),
		BodySHA256: compactionPayloadSHA256(reqJSON),
	})
}

// LogResponse records a successful compaction response.
func (cl *CompactionLog) LogResponse(summary string) {
	writeCompactionEntry(compactionLogEntry{
		Event:         "compaction_response",
		Provider:      cl.provider,
		Model:         cl.model,
		SummaryLen:    len(summary),
		SummarySHA256: compactionPayloadSHA256([]byte(summary)),
	})
}

// LogError records a failed compaction attempt.
func (cl *CompactionLog) LogError(err error, rawBody []byte) {
	e := compactionLogEntry{
		Event:    "compaction_error",
		Provider: cl.provider,
		Model:    cl.model,
	}
	if err != nil {
		e.Error, e.ErrorSHA256 = compactionErrorMetadata(err.Error())
	}
	if len(rawBody) > 0 {
		e.BodyBytes = len(rawBody)
		e.BodySHA256 = compactionPayloadSHA256(rawBody)
	}
	writeCompactionEntry(e)
}

// ── provider.RawLogger implementation ────────────────────────────────────────

func (cl *CompactionLog) LogHTTPRequest(prov, method string, body []byte) {
	writeCompactionEntry(compactionLogEntry{
		Event:      "http_request",
		Provider:   prov,
		Method:     method,
		BodyBytes:  len(body),
		BodySHA256: compactionPayloadSHA256(body),
	})
}

func (cl *CompactionLog) LogHTTPResponse(prov, method string, statusCode int, body []byte) {
	writeCompactionEntry(compactionLogEntry{
		Event:      "http_response",
		Provider:   prov,
		Method:     method,
		StatusCode: statusCode,
		BodyBytes:  len(body),
		BodySHA256: compactionPayloadSHA256(body),
	})
}

func (cl *CompactionLog) LogHTTPError(prov, method string, statusCode int, body []byte, err error) {
	e := compactionLogEntry{
		Event:      "http_error",
		Provider:   prov,
		Method:     method,
		StatusCode: statusCode,
		BodyBytes:  len(body),
		BodySHA256: compactionPayloadSHA256(body),
	}
	if err != nil {
		e.Error, e.ErrorSHA256 = compactionErrorMetadata(err.Error())
	}
	writeCompactionEntry(e)
}

func (cl *CompactionLog) LogParseError(prov string, body []byte, err error) {
	e := compactionLogEntry{
		Event:      "parse_error",
		Provider:   prov,
		BodyBytes:  len(body),
		BodySHA256: compactionPayloadSHA256(body),
	}
	if err != nil {
		e.Error, e.ErrorSHA256 = compactionErrorMetadata(err.Error())
	}
	writeCompactionEntry(e)
}
