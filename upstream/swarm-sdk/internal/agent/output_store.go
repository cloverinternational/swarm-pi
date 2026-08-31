// Package agent provides OutputStore — an append-only file-based output sink
// for background agents. Mirrors Claude Code's file-IPC pattern where every
// output chunk is persisted to ~/.cache/swarm/tasks/{agentID}.output so that:
//
//   - Parent agents and tools can read partial output while the agent is running
//   - Output survives process crashes (no loss on TUI restart)
//   - Resume / transcript replay becomes possible
//   - Output can be truncated at read time rather than ballooning in memory
package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// outputStoreBaseDir returns the base directory for task output files.
// Resolves to: $XDG_CACHE_HOME/swarm/tasks  or  ~/.cache/swarm/tasks
func outputStoreBaseDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(base, "swarm", "tasks")
}

// OutputStorePath returns the canonical file path for a given agent ID.
func OutputStorePath(agentID string) string {
	return filepath.Join(outputStoreBaseDir(), agentID+".output")
}

// OutputRecord is one line in the NDJSON output file.
// Each IntermediateUpdate is serialised as one record so the file can be
// streamed (read line-by-line) and re-parsed for resume / summarisation.
type OutputRecord struct {
	Type    string          `json:"type"` // "thinking"|"content"|"tool_call"|"tool_result"|"tool_chunk"|"final"
	TS      int64           `json:"ts"`   // unix milliseconds
	Content string          `json:"content,omitempty"`
	ID      string          `json:"id,omitempty"`     // tool call / result ID
	Name    string          `json:"name,omitempty"`   // tool name
	Params  json.RawMessage `json:"params,omitempty"` // tool call parameters
	Output  string          `json:"output,omitempty"` // tool result output
	Error   string          `json:"error,omitempty"`  // tool result error
	Append  bool            `json:"append,omitempty"` // true for streaming content/thinking
}

// OutputStore is a thread-safe, append-only file writer for background agent output.
type OutputStore struct {
	path string

	mu  sync.Mutex
	f   *os.File
	buf *bufio.Writer
}

// NewOutputStore opens (or creates) the output file at path and returns a store
// ready for writing. Parent directories are created automatically.
func NewOutputStore(agentID string) (*OutputStore, error) {
	path := OutputStorePath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("output_store: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("output_store: open: %w", err)
	}
	return &OutputStore{
		path: path,
		f:    f,
		buf:  bufio.NewWriterSize(f, 64*1024),
	}, nil
}

// Path returns the absolute path of the output file.
func (s *OutputStore) Path() string { return s.path }

// WriteRecord serialises rec as a single NDJSON line and appends it to the file.
// Thread-safe.
func (s *OutputStore) WriteRecord(rec OutputRecord) error {
	rec.TS = time.Now().UnixMilli()
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("output_store: marshal: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.buf.Write(line); err != nil {
		return fmt.Errorf("output_store: write: %w", err)
	}
	if err := s.buf.WriteByte('\n'); err != nil {
		return fmt.Errorf("output_store: write newline: %w", err)
	}
	return nil
}

// Flush flushes the write buffer to the OS. Call after each record if you want
// low-latency reads from a concurrent reader (e.g. TaskOutput mid-flight).
func (s *OutputStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.buf.Flush(); err != nil {
		return fmt.Errorf("output_store: flush: %w", err)
	}
	return nil
}

// Close flushes and closes the file. Safe to call multiple times.
func (s *OutputStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	if err := s.buf.Flush(); err != nil {
		_ = s.f.Close()
		s.f = nil
		return err
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// ---------------------------------------------------------------------------
// Read-side helpers (used by TaskOutput tool and resume)
// ---------------------------------------------------------------------------

// ReadOutputStoreResult is returned by ReadOutputStore.
type ReadOutputStoreResult struct {
	// Content is the raw text read from the file starting at Offset.
	Content string
	// BytesRead is the number of bytes actually returned in Content.
	BytesRead int64
	// TotalBytes is the total size of the file at read time.
	TotalBytes int64
	// NewOffset is Offset + BytesRead — pass this on the next call for
	// incremental (streaming) reads.
	NewOffset int64
	// Truncated is true when TotalBytes > Offset + MaxBytes, meaning there
	// is more content in the file beyond what was returned.
	Truncated bool
}

const defaultMaxReadBytes = 8 * 1024 * 1024 // 8 MB

// ReadOutputStore reads up to maxBytes of content from the file starting at
// byteOffset. Pass maxBytes=0 to use the 8 MB default. Pass byteOffset=0 to
// read from the beginning.
//
// If the total file size exceeds byteOffset+maxBytes, Truncated is set and
// callers should prepend a human-readable note to Content.
func ReadOutputStore(agentID string, byteOffset, maxBytes int64) (*ReadOutputStoreResult, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxReadBytes
	}
	path := OutputStorePath(agentID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ReadOutputStoreResult{}, nil // Agent hasn't written anything yet
		}
		return nil, fmt.Errorf("output_store: open for read: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("output_store: stat: %w", err)
	}
	totalBytes := stat.Size()

	if byteOffset > totalBytes {
		byteOffset = totalBytes
	}
	if byteOffset > 0 {
		if _, err := f.Seek(byteOffset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("output_store: seek: %w", err)
		}
	}

	available := totalBytes - byteOffset
	readSize := available
	truncated := false
	if readSize > maxBytes {
		readSize = maxBytes
		truncated = true
	}

	buf := make([]byte, readSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("output_store: read: %w", err)
	}
	buf = buf[:n]

	return &ReadOutputStoreResult{
		Content:    string(buf),
		BytesRead:  int64(n),
		TotalBytes: totalBytes,
		NewOffset:  byteOffset + int64(n),
		Truncated:  truncated,
	}, nil
}

// ReadOutputStoreBestEffort is a tolerant, last-resort fallback for
// ReadOutputStore: a plain whole-file read with no seek/stat/ReadFull error
// path to trip over. It exists for issue #284, where a background agent
// wrote 172KB of real NDJSON output and then failed during finalization; a
// transient error from ReadOutputStore's structured read (a concurrent
// Close/rename, a stat race, a truncated final write, etc.) made the caller
// fall back to reporting only the in-memory failure marker, discarding every
// byte the agent actually produced. This function trades ReadOutputStore's
// precision (exact byte offsets, a distinguishable "does not exist yet" nil
// vs error) for robustness: any content it can read at all is better than
// silently losing a substantial, already-persisted report. Returns ("", nil)
// if the file does not exist or is empty; returns a non-nil error only if
// the file exists but genuinely cannot be read (e.g. permissions).
//
// Capped at defaultMaxReadBytes for the same reason ReadOutputStore caps its
// read — an unbounded read of a runaway output file must never be handed
// back to the caller in one shot.
func ReadOutputStoreBestEffort(agentID string) (string, error) {
	path := OutputStorePath(agentID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("output_store: best-effort read: %w", err)
	}
	if int64(len(data)) > defaultMaxReadBytes {
		data = data[:defaultMaxReadBytes]
	}
	return string(data), nil
}

// ParseOutputStoreRecords parses raw NDJSON content (as returned by
// ReadOutputStore) into a slice of OutputRecord. Malformed lines are skipped.
func ParseOutputStoreRecords(content string) []OutputRecord {
	var records []OutputRecord
	for len(content) > 0 {
		// Find next newline
		end := 0
		for end < len(content) && content[end] != '\n' {
			end++
		}
		line := content[:end]
		if end < len(content) {
			content = content[end+1:]
		} else {
			content = ""
		}

		line = trimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec OutputRecord
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			records = append(records, rec)
		}
	}
	return records
}

// FormatRecordsAsTranscript reconstructs a clean, human-readable transcript from
// parsed OutputStore records. It coalesces streaming "append" chunks back into
// whole content/thinking blocks, condenses tool calls to a single line, and
// surfaces errors and the final message. This is what TaskOutput/SubagentOutput
// should return — NOT the raw NDJSON, which balloons to hundreds of KB of
// {"type":"content",...} noise under token streaming.
//
// includeThinking controls whether thinking blocks are included (usually off —
// they are verbose and rarely needed by the parent).
func FormatRecordsAsTranscript(records []OutputRecord, includeThinking bool) string {
	if len(records) == 0 {
		return ""
	}

	var sb strings.Builder
	var contentBuf strings.Builder // coalesces consecutive streaming content
	var thinkingBuf strings.Builder

	flushContent := func() {
		if contentBuf.Len() > 0 {
			sb.WriteString(strings.TrimRight(contentBuf.String(), "\n"))
			sb.WriteString("\n")
			contentBuf.Reset()
		}
	}
	flushThinking := func() {
		if thinkingBuf.Len() > 0 {
			if includeThinking {
				sb.WriteString("[thinking] ")
				sb.WriteString(strings.TrimRight(thinkingBuf.String(), "\n"))
				sb.WriteString("\n")
			}
			thinkingBuf.Reset()
		}
	}

	var finalContent, finalError string

	for _, rec := range records {
		switch rec.Type {
		case "content":
			flushThinking()
			// A non-append record marks the START of a new content block. Separate
			// it from any prior block with a newline, but never split streaming
			// chunks (append==true) which must concatenate seamlessly.
			if !rec.Append && contentBuf.Len() > 0 {
				contentBuf.WriteString("\n")
			}
			contentBuf.WriteString(rec.Content)
		case "thinking":
			flushContent()
			thinkingBuf.WriteString(rec.Content)
		case "tool_call":
			flushThinking()
			flushContent()
			if rec.Name != "" {
				sb.WriteString(fmt.Sprintf("→ %s\n", rec.Name))
			}
		case "tool_result":
			if rec.Error != "" {
				flushThinking()
				flushContent()
				sb.WriteString(fmt.Sprintf("  ✗ %s\n", rec.Error))
			}
		case "final":
			finalContent = rec.Content
			finalError = rec.Error
		}
	}
	flushThinking()
	flushContent()

	if finalContent != "" {
		sb.WriteString("\n")
		sb.WriteString(strings.TrimRight(finalContent, "\n"))
		sb.WriteString("\n")
	} else if finalError != "" {
		sb.WriteString(fmt.Sprintf("\n[failed: %s]\n", finalError))
	}

	return sb.String()
}

// FormatFinalOrTranscript returns the sub-agent's FINAL message only when the
// run produced one (terminal "final" record), matching Claude Code's
// finalizeAgentTool behavior — the parent sees the agent's answer, not its
// intermediate reasoning/tool noise. When there is no final record yet (agent
// still running, or killed before finishing), it falls back to the coalesced
// transcript so partial progress is still visible.
//
// This is the preferred formatter for returning a COMPLETED background agent's
// result to the parent. Use FormatRecordsAsTranscript directly only when you
// explicitly want the full play-by-play (e.g. a verbose/progress view).
func FormatFinalOrTranscript(records []OutputRecord, includeThinking bool) string {
	if len(records) == 0 {
		return ""
	}
	// Find the last terminal "final" record.
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.Type != "final" {
			continue
		}
		if rec.Content != "" {
			return strings.TrimRight(rec.Content, "\n") + "\n"
		}
		if rec.Error != "" {
			return fmt.Sprintf("[failed: %s]\n", rec.Error)
		}
		// "final" with neither content nor error (e.g. "cancelled" sentinel
		// handled elsewhere) — fall through to the transcript.
		break
	}
	return FormatRecordsAsTranscript(records, includeThinking)
}

// trimSpace trims leading/trailing ASCII whitespace without allocating.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// BuildResumeContext reads the prior output file for agentID and returns a
// formatted context block suitable for prepending to a new task message.
// This enables the Task tool to resume a prior agent run.
//
// Returns ("", nil) if no prior output file exists.
func BuildResumeContext(agentID string) (string, error) {
	r, err := ReadOutputStore(agentID, 0, 512*1024) // cap at 512 KB for context injection
	if err != nil {
		return "", fmt.Errorf("output_store: resume read: %w", err)
	}
	if r.BytesRead == 0 {
		return "", nil // No prior output
	}

	records := ParseOutputStoreRecords(r.Content)
	if len(records) == 0 {
		return "", nil
	}

	// Reconstruct a human-readable transcript of the prior run.
	// We include: content blocks, tool calls (name + brief params), and the final message.
	var sb strings.Builder
	sb.WriteString("[PRIOR RUN CONTEXT - agent_id: ")
	sb.WriteString(agentID)
	sb.WriteString("]\n")
	if r.Truncated {
		sb.WriteString(fmt.Sprintf("[Note: prior output was truncated — showing first %dKB]\n", r.BytesRead/1024))
	}
	sb.WriteString("\n")

	for _, rec := range records {
		switch rec.Type {
		case "content":
			if rec.Content != "" && !rec.Append {
				sb.WriteString(rec.Content)
				sb.WriteByte('\n')
			}
		case "tool_call":
			sb.WriteString(fmt.Sprintf("[Tool: %s]\n", rec.Name))
		case "tool_result":
			if rec.Error != "" {
				sb.WriteString(fmt.Sprintf("[Tool result error: %s]\n", rec.Error))
			}
		case "final":
			if rec.Content != "" {
				sb.WriteString("\n[Final output from prior run:]\n")
				sb.WriteString(rec.Content)
				sb.WriteString("\n")
			} else if rec.Error != "" {
				sb.WriteString(fmt.Sprintf("\n[Prior run failed with: %s]\n", rec.Error))
			}
		}
	}

	sb.WriteString("\n[END PRIOR RUN CONTEXT]\n\n")
	sb.WriteString("Please continue the following task, building on the above prior work:\n")
	return sb.String(), nil
}
