// Package inbox holds the peer-inbox implementation extracted from
// internal/a2a/discovery.go (Phase 06, task "inbox extraction" — see
// docs/architecture/swarm-attach/adr-003-control-planes.md's Compatibility
// section and this phase's CONTRACT.md). It owns the on-disk message
// format (append-only NDJSON with legacy JSON-array back-compat and
// threshold-triggered compaction) but deliberately does NOT own the
// underlying registry filesystem security primitives (secure directory
// creation/migration, atomic same-directory-rename writes, per-path
// advisory locking, symlink-safe reads). Those primitives are shared,
// registry-wide chokepoints used well beyond inbox messaging (peer
// presence files, LAN registry ingestion) and therefore remain in
// internal/a2a; this package receives them via constructor injection
// (the FileSystem interface below) exactly as CONTRACT.md's "Shared type
// seam" section directs: "if shared, they stay in internal/a2a and
// internal/inbox takes them as constructor parameters/interfaces rather
// than duplicating them."
//
// See INDEX.md in this directory for the SwarmMessage-location decision,
// the exact dependency direction, and this phase's deliberately deferred
// scope (this is extraction/reorganization only — it does not change
// queue semantics or attempt "inbox retirement" in the broader push-
// delivery sense).
package inbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxMessages caps how many messages are retained per peer inbox. The
// inbox file uses an append-only NDJSON format (one JSON object per line);
// when the file grows beyond CompactionThreshold lines, it is rewritten to
// contain just the most recent MaxMessages lines. This bounds both read
// time and disk usage as message volume grows.
//
// These are the exact values (200 / 400) that lived at
// internal/a2a.MaxInboxMessages / internal/a2a.CompactionThreshold before
// this extraction; nothing outside internal/a2a's now-thin wrapper
// functions referenced those constants directly (verified by repo-wide
// grep before the move), so they were not left behind as aliases.
const (
	MaxMessages         = 200
	CompactionThreshold = 400
)

// Message represents a message in a peer's inbox. This is the type that
// used to be named SwarmMessage in internal/a2a/discovery.go. See
// INDEX.md's "SwarmMessage location decision" for why the type itself
// moved here (rather than staying in internal/a2a and being imported back)
// and how internal/a2a.SwarmMessage remains a drop-in type alias so every
// existing external caller (internal/a2a/runtime.go,
// swarm-tui/internal/chat/app_a2a_debug.go) needed zero changes.
type Message struct {
	From      string    `json:"from"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	Read      bool      `json:"read"`
}

// FileSystem abstracts the registry filesystem primitives this package
// depends on but does not own. internal/a2a supplies the concrete
// implementation (see internal/a2a/discovery.go's registryFS adapter),
// backed by its existing secureDir/resolveRegistryPath/withRegistryHandle/
// safeReadFile/registryReadFile/registryRemove/writeFileAtomic(Locked)/
// isUnsafeEntryError chokepoints — none of which are duplicated here.
type FileSystem interface {
	// SecureDir ensures dir exists, is owned by the current user, and is
	// migrated/quarantined to the package's required directory
	// permissions (0700) if it already existed unsafely.
	SecureDir(dir string) error
	// ResolvePath validates name and joins it under dir with ext,
	// re-verifying the result both canonicalizes to exactly dir/name+ext
	// and lies beneath the caller's owned registry root.
	ResolvePath(dir, name, ext string) (string, error)
	// WithHandle serializes concurrent access to path (process-local
	// mutex plus a stable cross-process OS lock) for the duration of fn.
	WithHandle(path string, fn func() error) error
	// SafeReadFile reads path only when it is a regular file (never a
	// symlink) and, where determinable, owned by the current user.
	SafeReadFile(path string) ([]byte, error)
	// ReadFile is a plain file read with no extra safety checks, for use
	// only inside a WithHandle callback where the caller has already
	// established a safe, locked context.
	ReadFile(path string) ([]byte, error)
	// RemoveFile removes path.
	RemoveFile(path string) error
	// WriteFileLocked writes data to path atomically (temp file + same-
	// directory rename) WITHOUT acquiring WithHandle's lock itself — for
	// use only inside a WithHandle callback that already holds it.
	WriteFileLocked(path string, data []byte) error
	// WriteFileAtomic writes data to path atomically, acquiring its own
	// WithHandle-equivalent locking internally — for use outside an
	// already-locked context.
	WriteFileAtomic(path string, data []byte) error
	// IsUnsafeEntryError reports whether err was produced by SafeReadFile
	// for an existing-but-untrusted entry (symlink, wrong type, wrong
	// owner), which callers must treat as "absent" rather than a hard
	// error.
	IsUnsafeEntryError(err error) bool
}

// Store is the inbox implementation, parameterized over a FileSystem so
// this package never touches the filesystem except through the injected
// interface.
type Store struct {
	fs FileSystem
}

// NewStore constructs a Store backed by fs. fs must not be nil.
func NewStore(fs FileSystem) *Store {
	return &Store{fs: fs}
}

// inboxesDir returns the "inboxes" subdirectory beneath swarmPath (the
// per-swarm root directory, e.g. internal/a2a.SwarmPath's return value).
func inboxesDir(swarmPath string) string {
	return filepath.Join(swarmPath, "inboxes")
}

// SendMessage appends a message to a peer's inbox using append-only NDJSON.
//
// Format: one JSON object per line. This avoids the read-modify-write cost
// of the previous JSON-array layout, which had to deserialize and
// re-serialize the entire inbox on every send — a real performance cliff
// when peers exchange thousands of messages.
//
// Truncation: the in-memory message slice is capped at MaxMessages entries
// (oldest dropped first) before every write, so the file never grows
// unbounded even without a separate compaction pass.
//
// Back-compat: ReadInbox transparently detects the old JSON-array format
// and parses it. The first SendMessage on an old-format inbox upgrades it
// to NDJSON (this call reads the existing content, appends the new
// message, and rewrites the whole file as NDJSON regardless of the
// previous format).
func (s *Store) SendMessage(swarmPath, toHandle, fromHandle, text string) error {
	dir := inboxesDir(swarmPath)
	if err := s.fs.SecureDir(dir); err != nil {
		return fmt.Errorf("secure inboxes directory: %w", err)
	}
	inboxPath, err := s.fs.ResolvePath(dir, toHandle, ".json")
	if err != nil {
		return fmt.Errorf("invalid recipient handle: %w", err)
	}

	msg := Message{
		From:      fromHandle,
		Text:      text,
		Timestamp: time.Now().UTC(),
		Read:      false,
	}

	return s.fs.WithHandle(inboxPath, func() error {
		existing, err := s.fs.ReadFile(inboxPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read inbox file: %w", err)
		}
		var messages []Message
		if len(existing) > 0 && existing[0] == '[' {
			if err := json.Unmarshal(existing, &messages); err != nil {
				return fmt.Errorf("unmarshal legacy inbox: %w", err)
			}
		} else {
			messages = parseNDJSONLines(existing)
		}
		messages = append(messages, msg)
		if len(messages) > MaxMessages {
			messages = messages[len(messages)-MaxMessages:]
		}
		var buf bytes.Buffer
		for _, item := range messages {
			data, err := json.Marshal(item)
			if err != nil {
				return err
			}
			buf.Write(data)
			buf.WriteByte('\n')
		}
		return s.fs.WriteFileLocked(inboxPath, buf.Bytes())
	})
}

// ReadInbox returns all messages in a peer's inbox, transparently handling
// both NDJSON and legacy JSON-array formats. Returns the last MaxMessages
// entries when the inbox is large.
func (s *Store) ReadInbox(swarmPath, handle string) ([]Message, error) {
	inboxPath, err := s.fs.ResolvePath(inboxesDir(swarmPath), handle, ".json")
	if err != nil {
		return nil, err
	}

	data, err := s.fs.SafeReadFile(inboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Message{}, nil
		}
		if s.fs.IsUnsafeEntryError(err) {
			// Fail closed: never trust an unsafe legacy/hostile inbox file.
			return []Message{}, nil
		}
		return nil, fmt.Errorf("read inbox: %w", err)
	}

	// Detect legacy JSON-array format from the bytes already in memory.
	if len(data) > 0 && data[0] == '[' {
		var msgs []Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			return nil, fmt.Errorf("unmarshal legacy inbox: %w", err)
		}
		return msgs, nil
	}

	msgs := parseNDJSONLines(data)
	// Trim to the most recent MaxMessages. Compaction normally keeps the
	// file under this cap, but readers should tolerate temporarily
	// oversized inboxes.
	if len(msgs) > MaxMessages {
		msgs = msgs[len(msgs)-MaxMessages:]
	}
	return msgs, nil
}

// ClearInbox removes all messages from a peer's inbox.
func (s *Store) ClearInbox(swarmPath, handle string) error {
	inboxPath, err := s.fs.ResolvePath(inboxesDir(swarmPath), handle, ".json")
	if err != nil {
		return err
	}
	return s.fs.WithHandle(inboxPath, func() error {
		return s.fs.RemoveFile(inboxPath)
	})
}

// readLegacyArrayInbox reads an inbox that was written in the old
// JSON-array format. Used to keep existing on-disk inboxes accessible
// after the format switch. Ported verbatim from
// internal/a2a/discovery.go; unused by SendMessage/ReadInbox's own inline
// legacy detection (both already handle the legacy-array case directly)
// but preserved here — exactly as unused in the original file — for
// callers/tests that want a standalone legacy-array reader.
func (s *Store) readLegacyArrayInbox(inboxPath string) ([]Message, error) {
	data, err := s.fs.SafeReadFile(inboxPath)
	if err != nil {
		if os.IsNotExist(err) || s.fs.IsUnsafeEntryError(err) {
			return []Message{}, nil
		}
		return nil, fmt.Errorf("read legacy inbox: %w", err)
	}
	var msgs []Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("unmarshal legacy inbox: %w", err)
	}
	return msgs, nil
}

// parseNDJSONLines parses an NDJSON byte slice into Messages, skipping
// blank lines and malformed entries.
func parseNDJSONLines(data []byte) []Message {
	msgs := make([]Message, 0, 16)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// writeNDJSONInbox rewrites the inbox file as NDJSON, capping at the most
// recent MaxMessages entries. Delegates to fs.WriteFileAtomic (temp file
// in the same directory + rename, per FileSystem's contract) rather than a
// hand-rolled deterministic "<path>.tmp" name: a deterministic temp name
// is symlink-attackable (an attacker who can pre-create that exact path as
// a symlink would have their target opened and truncated by
// O_CREATE|O_TRUNC), whereas the injected WriteFileAtomic's randomized
// temp name cannot be pre-positioned by an attacker, and the final publish
// is a same-directory rename that replaces the destination directory entry
// atomically regardless of what (if anything) was there before.
func (s *Store) writeNDJSONInbox(inboxPath string, msgs []Message) error {
	if len(msgs) > MaxMessages {
		msgs = msgs[len(msgs)-MaxMessages:]
	}
	var buf bytes.Buffer
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal inbox message: %w", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := s.fs.WriteFileAtomic(inboxPath, buf.Bytes()); err != nil {
		return fmt.Errorf("rename inbox: %w", err)
	}
	return nil
}

// maybeCompactInbox rewrites the inbox to keep only the most recent
// MaxMessages entries when its line count exceeds CompactionThreshold.
// Ported verbatim from internal/a2a/discovery.go; like the original, it is
// not called from SendMessage's own inline truncation path (SendMessage
// truncates directly on every write instead) — preserved here, still
// unused in the production call path, for exact behavioral parity plus
// availability to future callers/tests that want threshold-triggered
// compaction as a standalone operation.
func (s *Store) maybeCompactInbox(inboxPath string) error {
	data, err := s.fs.SafeReadFile(inboxPath)
	if err != nil {
		if os.IsNotExist(err) || s.fs.IsUnsafeEntryError(err) {
			return nil
		}
		return err
	}
	if bytes.Count(data, []byte{'\n'}) <= CompactionThreshold {
		return nil
	}
	return s.writeNDJSONInbox(inboxPath, parseNDJSONLines(data))
}
