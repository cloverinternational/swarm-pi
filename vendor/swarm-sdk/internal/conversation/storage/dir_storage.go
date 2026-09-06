package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// WorkspaceStorage extends Storage with a workspace-scoped listing method.
// Implementations that support efficient workspace-based queries should implement
// this interface in addition to Storage.
type WorkspaceStorage interface {
	Storage
	// ListByWorkspace returns all conversations belonging to a specific workspace
	// path without scanning unrelated workspace directories.
	ListByWorkspace(ctx context.Context, workspacePath string) ([]*conversation.Conversation, error)
}

// DirectoryFileStorage implements Storage using a directory layout. New writes
// are persisted in the conversation-owned directory used by task/journal
// metadata, while the workspace-partitioned copy remains during migration:
//
//	<baseDir>/<id>/conversation.json          – canonical transcript
//	<baseDir>/<encoded-workspace>/<id>.json   – compatibility/workspace index
//	<baseDir>/_idx/<id>                       – tiny index: workspace dir name
//
// Benefits over the old flat FileStorage:
//   - O(1) listing for a specific workspace (just read one directory)
//   - Zero redundant JSON parsing when filtering by workspace
//   - Index enables O(1) Load / Save / Delete / Exists by ID without scanning
//
// Migration: on the first instantiation the constructor transparently migrates
// any legacy flat files found in <baseDir>/*.json into the new layout. The
// original flat files are left untouched (safe, non-destructive).
type DirectoryFileStorage struct {
	mu      sync.RWMutex
	baseDir string
	closed  bool

	// In-memory LRU cache (same design as FileStorage)
	cache     map[string]*cachedConversation
	cacheSize int
	cacheLRU  []string

	// Metadata-only cache for List/Query. Separate from the LRU above: that
	// one holds FULL conversations and is capped at cacheSize (default 100),
	// which against thousands of files means a ~100% miss rate on every
	// listing. Metadata entries are ~1 KB, so every file can be cached, and
	// each is validated against the file's (modTime, size) before reuse.
	// Guarded by its own mutex to keep listings off the main storage lock.
	metaMu    sync.RWMutex
	metaCache map[string]*metaCacheEntry

	// Compaction settings (same as FileStorage, preserved for API compatibility)
	compactionThresholdTokens int
	maxContextWindow          int
	minRetainPercent          float64
	autoCompact               bool

	metrics *FileStorageMetrics

	readConversationFile func(string) ([]byte, error)
}

// DirectoryFileStorageConfig holds configuration for DirectoryFileStorage.
type DirectoryFileStorageConfig struct {
	// BaseDir is the root directory. Sub-directories are created automatically.
	// Defaults to paths.ConversationsDir() (~/.swarm/conversations) if empty.
	BaseDir string

	// CacheSize is the in-memory LRU cache capacity (default: 100).
	CacheSize int

	// CompactionThresholdPercent (0–1) of MaxContextWindow at which to auto-compact.
	// Default: 0.80.
	CompactionThresholdPercent float64

	// MaxContextWindow in tokens. Default: 200 000.
	MaxContextWindow int

	// MinRetainPercent (0–1) of messages to keep after compaction. Default: 0.30.
	MinRetainPercent float64

	// AutoCompact enables automatic compaction on Save when threshold is exceeded.
	AutoCompact bool
}

// NewDirectoryFileStorage creates a DirectoryFileStorage and (if necessary) migrates
// legacy flat conversation files from baseDir into the new nested layout.
func NewDirectoryFileStorage(config DirectoryFileStorageConfig) (*DirectoryFileStorage, error) {
	if config.BaseDir == "" {
		config.BaseDir = paths.ConversationsDir()
	}

	if config.CacheSize <= 0 {
		config.CacheSize = 100
	}
	if config.MaxContextWindow <= 0 {
		config.MaxContextWindow = 200_000 // Unknown-model fallback; resolved model limits should be supplied.
	}
	if config.CompactionThresholdPercent <= 0 {
		config.CompactionThresholdPercent = 0.80
	}
	if config.MinRetainPercent <= 0 {
		config.MinRetainPercent = 0.30
	}

	compactionThresholdTokens := int(float64(config.MaxContextWindow) * config.CompactionThresholdPercent)

	// Ensure base + index dirs exist.
	for _, dir := range []string{config.BaseDir, filepath.Join(config.BaseDir, IndexDir)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	d := &DirectoryFileStorage{
		baseDir:                   config.BaseDir,
		cache:                     make(map[string]*cachedConversation),
		cacheSize:                 config.CacheSize,
		cacheLRU:                  make([]string, 0, config.CacheSize),
		metaCache:                 make(map[string]*metaCacheEntry),
		compactionThresholdTokens: compactionThresholdTokens,
		maxContextWindow:          config.MaxContextWindow,
		minRetainPercent:          config.MinRetainPercent,
		autoCompact:               config.AutoCompact,
		metrics:                   &FileStorageMetrics{},
		readConversationFile:      os.ReadFile,
	}

	// Transparent migration of legacy flat files (non-destructive, idempotent).
	if err := d.migrateFlatFiles(); err != nil {
		// Log but don't fail – the old flat files remain accessible via the
		// fallback path in Load.
		fmt.Fprintf(os.Stderr, "[dir-storage] migration warning: %v\n", err)
	}

	return d, nil
}

// ---- Storage interface -------------------------------------------------------

// Save persists a conversation. The destination subdirectory is determined from
// conv.WorkspacePath (falling back to Metadata.Custom["workspace_path"]).
func (d *DirectoryFileStorage) Save(ctx context.Context, conv *conversation.Conversation) error {
	if conv == nil || conv.ID == "" {
		return ErrInvalidID
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrStorageClosed
	}

	// Establish the first-class invariant before either persistence or caching.
	// A warm read immediately after Save must behave like a cold disk read.
	normalizeWorkspacePath(conv, "")

	currentSize := conv.CurrentContextSize
	if currentSize == 0 {
		currentSize = conv.TotalTokens
	}
	if d.autoCompact && currentSize > d.compactionThresholdTokens {
		conv = d.compactUnsafe(conv)
		d.metrics.mu.Lock()
		d.metrics.Compactions++
		d.metrics.mu.Unlock()
	}

	if err := d.writeToDiskUnsafe(conv); err != nil {
		return err
	}

	d.updateCacheUnsafe(conv)

	d.metrics.mu.Lock()
	d.metrics.DiskWrites++
	d.metrics.mu.Unlock()

	return nil
}

// Load retrieves a conversation by ID, checking the index first, then falling
// back to the old flat-file location for unmigrated legacy entries.
func (d *DirectoryFileStorage) Load(ctx context.Context, id string) (*conversation.Conversation, error) {
	if id == "" {
		return nil, ErrInvalidID
	}

	for {
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return nil, ErrStorageClosed
		}

		// Cache hit.
		if cached, ok := d.cache[id]; ok {
			d.updateLRUUnsafe(id)
			cached.lastAccessed = time.Now()
			conv := cloneConversation(cached.conv)
			d.mu.Unlock()
			d.metrics.mu.Lock()
			d.metrics.CacheHits++
			d.metrics.mu.Unlock()
			return conv, nil
		}

		// Resolve file path from index or legacy location while mutations are
		// excluded, then release the lock for the expensive read and decode.
		filePath, err := d.resolvePathUnsafe(id)
		d.mu.Unlock()
		d.metrics.mu.Lock()
		d.metrics.CacheMisses++
		d.metrics.mu.Unlock()
		if err != nil {
			return nil, err
		}

		d.metrics.mu.Lock()
		d.metrics.DiskReads++
		d.metrics.mu.Unlock()

		data, err := d.readConversationFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrConversationNotFound
			}
			return nil, fmt.Errorf("failed to read file: %w", err)
		}

		var conv conversation.Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			return nil, fmt.Errorf("failed to unmarshal conversation: %w", err)
		}
		normalizeWorkspacePath(&conv, workspacePathHintForFile(filePath))

		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return nil, ErrStorageClosed
		}

		// A concurrent Load or Save may have installed a newer value while this
		// goroutine was decoding. Prefer that value rather than overwriting it.
		if cached, ok := d.cache[id]; ok {
			d.updateLRUUnsafe(id)
			cached.lastAccessed = time.Now()
			loaded := cloneConversation(cached.conv)
			d.mu.Unlock()
			return loaded, nil
		}

		// Delete may have removed the file, or Save may have moved it to another
		// workspace, while the lock was released. Never resurrect stale bytes.
		currentPath, err := d.resolvePathUnsafe(id)
		if err != nil {
			d.mu.Unlock()
			return nil, err
		}
		if currentPath != filePath {
			d.mu.Unlock()
			continue
		}

		d.updateCacheUnsafe(&conv)
		loaded := cloneConversation(&conv)
		d.mu.Unlock()
		return loaded, nil
	}
}

// Delete removes a conversation from cache, disk, and the index.
func (d *DirectoryFileStorage) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidID
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrStorageClosed
	}

	delete(d.cache, id)
	d.removeLRUUnsafe(id)

	filePath, err := d.resolvePathUnsafe(id)
	if err == nil {
		if removeErr := os.Remove(filePath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("failed to delete file: %w", removeErr)
		}
	}
	if removeErr := os.RemoveAll(filepath.Join(d.baseDir, id)); removeErr != nil {
		return fmt.Errorf("failed to delete conversation directory: %w", removeErr)
	}

	// Remove index entry.
	idxPath := filepath.Join(d.baseDir, IndexDir, id)
	_ = os.Remove(idxPath)

	return nil
}

// Exists checks whether a conversation ID is present in the index or as a legacy file.
func (d *DirectoryFileStorage) Exists(ctx context.Context, id string) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return false, ErrStorageClosed
	}

	if _, ok := d.cache[id]; ok {
		return true, nil
	}

	_, err := d.resolvePathUnsafe(id)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// List returns all conversations across all workspace directories.
func (d *DirectoryFileStorage) List(ctx context.Context) ([]*conversation.Conversation, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return nil, ErrStorageClosed
	}

	return d.listFromDirsUnsafe("")
}

// ListByWorkspace returns all conversations for the given workspace path.
// This implements WorkspaceStorage and is O(|workspace|) rather than O(N-total).
func (d *DirectoryFileStorage) ListByWorkspace(ctx context.Context, workspacePath string) ([]*conversation.Conversation, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return nil, ErrStorageClosed
	}

	return d.listFromDirsUnsafe(workspacePath)
}

// Query retrieves conversations matching a filter. When the filter contains a
// WorkspacePath it scans only the corresponding subdirectory.
// NOTE: Filter does not currently have a WorkspacePath field; callers should
// use ListByWorkspace for efficient workspace-scoped queries.
func (d *DirectoryFileStorage) Query(ctx context.Context, filter Filter) ([]*conversation.Conversation, error) {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	d.mu.RUnlock()

	all, err := d.pooledListFromDirs(filter.WorkspacePath, filter)
	if err != nil {
		return nil, err
	}

	// Deduplicate by ID — the same conversation can appear in multiple
	// directories when legacy flat files were migrated into workspace dirs
	// but the originals were not removed. First occurrence wins (workspace-
	// partitioned entries are scanned before __default__).
	seen := make(map[string]struct{}, len(all))
	results := make([]*conversation.Conversation, 0, len(all))
	for _, conv := range all {
		if _, dup := seen[conv.ID]; dup {
			continue
		}
		seen[conv.ID] = struct{}{}
		if matchesFilter(conv, filter) {
			results = append(results, conv)
		}
	}

	// Sort BEFORE applying Limit/Offset so pagination respects SortBy/SortOrder.
	// Previously Limit was applied mid-scan (arbitrary directory-read order),
	// which caused "page 1" to return a random subset instead of the N newest.
	sortResults(results, filter)

	if filter.Offset > 0 {
		if filter.Offset >= len(results) {
			return nil, nil
		}
		results = results[filter.Offset:]
	}
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// Stream is unimplemented for file storage. Returns a closed channel.
func (d *DirectoryFileStorage) Stream(ctx context.Context, id string) (<-chan *conversation.Message, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, ErrStorageClosed
	}
	ch := make(chan *conversation.Message)
	close(ch)
	return ch, nil
}

// Close flushes dirty cache entries and marks the storage as closed.
func (d *DirectoryFileStorage) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}

	for id, cached := range d.cache {
		if cached.dirty {
			if err := d.writeToDiskUnsafe(cached.conv); err != nil {
				return fmt.Errorf("failed to flush conversation %s: %w", id, err)
			}
		}
	}

	d.closed = true
	d.cache = nil
	d.cacheLRU = nil
	return nil
}

// Metrics implements MetricsProvider.
func (d *DirectoryFileStorage) Metrics(ctx context.Context) (*StorageMetrics, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count, size int64
	entries, _ := os.ReadDir(d.baseDir)
	for _, e := range entries {
		if !e.IsDir() || !IsWorkspaceDir(e.Name()) {
			continue
		}
		wsEntries, _ := os.ReadDir(filepath.Join(d.baseDir, e.Name()))
		for _, we := range wsEntries {
			if filepath.Ext(we.Name()) != ".json" {
				continue
			}
			count++
			if info, err := we.Info(); err == nil {
				size += info.Size()
			}
		}
	}
	return &StorageMetrics{
		TotalConversations: count,
		TotalSizeBytes:     size,
		BackendType:        "dir_file",
	}, nil
}

// BaseDir returns the root storage directory.
func (d *DirectoryFileStorage) BaseDir() string { return d.baseDir }

// CacheMetrics returns cache hit/miss statistics.
func (d *DirectoryFileStorage) CacheMetrics() *FileStorageMetrics {
	d.metrics.mu.RLock()
	defer d.metrics.mu.RUnlock()
	d.mu.RLock()
	cached := len(d.cache)
	d.mu.RUnlock()
	return &FileStorageMetrics{
		CacheHits:     d.metrics.CacheHits,
		CacheMisses:   d.metrics.CacheMisses,
		DiskReads:     d.metrics.DiskReads,
		DiskWrites:    d.metrics.DiskWrites,
		Compactions:   d.metrics.Compactions,
		CachedEntries: cached,
	}
}

// ---- Internal helpers (caller must hold lock unless noted) -------------------

// workspaceDirForConv resolves the encoded workspace subdirectory for a conversation,
// reading from the first-class WorkspacePath field then falling back to Metadata.Custom.
func workspaceDirForConv(conv *conversation.Conversation) string {
	wp := conv.WorkspacePath
	if wp == "" {
		if conv.Metadata.Custom != nil {
			if v, ok := conv.Metadata.Custom["workspace_path"].(string); ok {
				wp = v
			}
		}
	}
	return EncodeWorkspacePath(wp)
}

// normalizeWorkspacePath establishes the first-class WorkspacePath field using,
// in order, its existing value, legacy Metadata.Custom["workspace_path"], and a
// decoded workspace-directory hint.
//
// Conversations are physically filed into the correct workspace subdirectory by
// workspaceDirForConv (which already consults Metadata.Custom), but the loaded
// struct's top-level WorkspacePath stayed empty for these records. Workspace-scoped
// history tooling (HistorySearch's workspaceMatches filter, HistoryGet's workspace
// guard) reads the top-level field, so without this backfill every legacy record is
// silently dropped from scope=current searches and is unreadable via HistoryGet.
// Call this at every save and load path so the top-level field is authoritative
// everywhere. workspacePathHint must already be decoded.
func normalizeWorkspacePath(conv *conversation.Conversation, workspacePathHint string) {
	if conv == nil || conv.WorkspacePath != "" {
		return
	}
	if conv.Metadata.Custom != nil {
		if v, ok := conv.Metadata.Custom["workspace_path"].(string); ok && v != "" {
			conv.WorkspacePath = v
			return
		}
	}
	conv.WorkspacePath = workspacePathHint
}

// writeToDiskUnsafe atomically writes conv to its workspace subdirectory and
// updates the ID index. Caller must hold d.mu (write).
func (d *DirectoryFileStorage) writeToDiskUnsafe(conv *conversation.Conversation) error {
	wsDir := workspaceDirForConv(conv)
	idxPath := filepath.Join(d.baseDir, IndexDir, conv.ID)
	var previousWSDir string
	if previous, err := os.ReadFile(idxPath); err == nil {
		previousWSDir = strings.TrimSpace(string(previous))
	}
	subDir := filepath.Join(d.baseDir, wsDir)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace dir: %w", err)
	}

	filePath := filepath.Join(subDir, conv.ID+".json")
	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation: %w", err)
	}

	// Persist beside metadata/task.json and metadata/journal. Save runs after
	// every message, so durability does not depend on graceful shutdown.
	conversationDir := filepath.Join(d.baseDir, conv.ID)
	if err := os.MkdirAll(conversationDir, 0o700); err != nil {
		return fmt.Errorf("failed to create conversation dir: %w", err)
	}
	canonicalPath := filepath.Join(conversationDir, "conversation.json")
	if err := atomicfile.Write(canonicalPath, data, atomicfile.WithPerm(0o600)); err != nil {
		return fmt.Errorf("failed to write canonical conversation transcript: %w", err)
	}

	// Keep the workspace-partitioned copy while older readers migrate.
	if err := atomicfile.Write(filePath, data, atomicfile.WithPerm(0o644)); err != nil {
		return fmt.Errorf("failed to write conversation file: %w", err)
	}

	// Write index entry: "<id>" → "<wsDir>"
	if err := atomicfile.Write(idxPath, []byte(wsDir), atomicfile.WithPerm(0o644)); err != nil {
		if previousWSDir != "" && previousWSDir != wsDir && IsWorkspaceDir(previousWSDir) {
			_ = os.Remove(filePath)
			rollbackErr := atomicfile.Write(idxPath, []byte(previousWSDir), atomicfile.WithPerm(0o644))
			return fmt.Errorf("failed to move conversation index: %w (index rollback: %v)", err, rollbackErr)
		}
		// Initial saves remain recoverable through directory scanning.
		fmt.Fprintf(os.Stderr, "[dir-storage] failed to write index for %s: %v\n", conv.ID, err)
	} else if previousWSDir != "" && previousWSDir != wsDir && IsWorkspaceDir(previousWSDir) {
		// The new copy and index are durable; remove the stale old workspace copy
		// so restart-time directory scans cannot surface the conversation twice.
		oldPath := filepath.Join(d.baseDir, previousWSDir, conv.ID+".json")
		if removeErr := os.Remove(oldPath); removeErr != nil && !os.IsNotExist(removeErr) {
			// Restore the old canonical location. Best-effort cleanup errors are
			// included in the returned failure rather than silently duplicating.
			rollbackErr := atomicfile.Write(idxPath, []byte(previousWSDir), atomicfile.WithPerm(0o644))
			cleanupErr := os.Remove(filePath)
			return fmt.Errorf("failed to remove old workspace copy: %w (index rollback: %v, new-copy cleanup: %v)", removeErr, rollbackErr, cleanupErr)
		}
	}

	return nil
}

// resolvePathUnsafe returns the absolute path of a conversation file by consulting
// the ID index. Falls back to the legacy flat-file location.
// Caller must hold d.mu (at least read).
func (d *DirectoryFileStorage) resolvePathUnsafe(id string) (string, error) {
	canonicalPath := filepath.Join(d.baseDir, id, "conversation.json")
	if _, err := os.Stat(canonicalPath); err == nil {
		return canonicalPath, nil
	}

	idxPath := filepath.Join(d.baseDir, IndexDir, id)
	wsDir, err := os.ReadFile(idxPath)
	if err == nil {
		return filepath.Join(d.baseDir, strings.TrimSpace(string(wsDir)), id+".json"), nil
	}

	// Legacy fallback: flat file at baseDir/<id>.json.
	legacyPath := filepath.Join(d.baseDir, id+".json")
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	}

	return "", ErrConversationNotFound
}

// ResolveConversationPath returns the absolute filesystem path of the JSON file
// for the given conversation ID. Returns an empty string if the conversation
// cannot be found (does not exist or storage error).
// This is used by the compaction system to embed the file path in summary
// messages and pointer stubs so the agent can read raw history when needed.
func (d *DirectoryFileStorage) ResolveConversationPath(id string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	path, err := d.resolvePathUnsafe(id)
	if err != nil {
		return ""
	}
	return path
}

// listFromDirsUnsafe reads conversations from all workspace subdirectories.
// If workspacePath is non-empty, only that workspace's directory is read.
// Caller must hold d.mu (at least read).
func (d *DirectoryFileStorage) listFromDirsUnsafe(workspacePath string) ([]*conversation.Conversation, error) {
	return d.listFromDirsFilteredUnsafe(workspacePath, Filter{})
}

// listFromDirsFilteredUnsafe is like listFromDirsUnsafe but respects Filter.ExcludeMessages
// and Filter.FirstMessagePreview to dramatically reduce memory pressure for sidebar listings.
func (d *DirectoryFileStorage) listFromDirsFilteredUnsafe(workspacePath string, filter Filter) ([]*conversation.Conversation, error) {
	var dirs []string

	if workspacePath != "" {
		dirs = []string{EncodeWorkspacePath(workspacePath)}
	} else {
		entries, err := os.ReadDir(d.baseDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read base dir: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() && IsWorkspaceDir(e.Name()) {
				dirs = append(dirs, e.Name())
			}
		}
	}

	var results []*conversation.Conversation
	for _, wsDir := range dirs {
		wsPath := filepath.Join(d.baseDir, wsDir)
		workspacePathHint, _ := DecodeWorkspacePath(wsDir)
		entries, err := os.ReadDir(wsPath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".json")

			// Cache hit — clone then optionally strip.
			if cached, ok := d.cache[id]; ok {
				c := cloneConversation(cached.conv)
				normalizeWorkspacePath(c, workspacePathHint)
				applyMessageFilter(c, filter)
				results = append(results, c)
				continue
			}

			// Read from disk.
			data, err := os.ReadFile(filepath.Join(wsPath, e.Name()))
			if err != nil {
				continue
			}
			var conv conversation.Conversation
			if err := json.Unmarshal(data, &conv); err != nil {
				continue
			}
			normalizeWorkspacePath(&conv, workspacePathHint)
			applyMessageFilter(&conv, filter)
			results = append(results, &conv)
		}
	}

	return results, nil
}

// applyMessageFilter strips or truncates the Messages slice according to filter flags.
// This is applied after unmarshalling so the full JSON is only parsed once.
func applyMessageFilter(c *conversation.Conversation, filter Filter) {
	if filter.ExcludeMessages {
		c.Messages = nil
		return
	}
	if filter.FirstMessagePreview > 0 && len(c.Messages) > filter.FirstMessagePreview {
		c.Messages = c.Messages[:filter.FirstMessagePreview]
	}
}

// ---- Legacy migration --------------------------------------------------------

// migrateFlatFiles moves any top-level flat JSON files in baseDir into the
// workspace-partitioned layout. This is idempotent (skips already-indexed IDs)
// and non-destructive (originals are never deleted).
func (d *DirectoryFileStorage) migrateFlatFiles() error {
	entries, err := os.ReadDir(d.baseDir)
	if err != nil {
		return fmt.Errorf("failed to read base dir for migration: %w", err)
	}

	migrated := 0
	refreshed := 0
	skipped := 0

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}

		id := strings.TrimSuffix(e.Name(), ".json")
		srcPath := filepath.Join(d.baseDir, e.Name())
		idxPath := filepath.Join(d.baseDir, IndexDir, id)

		// If already in index, check whether the flat file is newer than the
		// workspace copy (can happen when the server writes to the flat file
		// concurrently with a migration run). If so, refresh the workspace copy.
		if wsBytes, err := os.ReadFile(idxPath); err == nil {
			wsDir := strings.TrimSpace(string(wsBytes))
			destPath := filepath.Join(d.baseDir, wsDir, e.Name())
			srcInfo, srcErr := e.Info()
			destInfo, destErr := os.Stat(destPath)
			if srcErr == nil && destErr == nil && srcInfo.ModTime().After(destInfo.ModTime()) {
				// Flat file updated after migration – refresh workspace copy.
				if data, readErr := os.ReadFile(srcPath); readErr == nil {
					if writeErr := atomicfile.Write(destPath, data, atomicfile.WithPerm(0o644)); writeErr == nil {
						refreshed++
					}
				}
			} else {
				skipped++
			}
			continue
		}

		// Parse the file to extract workspace path.
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}

		// Minimal parse – we only need WorkspacePath and Metadata.Custom.
		var conv conversation.Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}

		// Determine workspace directory.
		wsDir := workspaceDirForConv(&conv)
		subDir := filepath.Join(d.baseDir, wsDir)
		if err := os.MkdirAll(subDir, 0755); err != nil {
			continue
		}

		// Copy file to new location (atomic).
		destPath := filepath.Join(subDir, e.Name())
		if err := atomicfile.Write(destPath, data, atomicfile.WithPerm(0o644)); err != nil {
			continue
		}

		// Write index entry.
		if err := atomicfile.Write(idxPath, []byte(wsDir), atomicfile.WithPerm(0o644)); err != nil {
			// Non-fatal; next boot will re-try.
			fmt.Fprintf(os.Stderr, "[dir-storage] index write failed for %s: %v\n", id, err)
		}

		migrated++
	}

	if migrated > 0 || refreshed > 0 {
		fmt.Fprintf(os.Stderr, "[dir-storage] migrated %d legacy conversations to workspace layout (%d refreshed, %d already done)\n", migrated, refreshed, skipped)
	}

	return nil
}

// ---- Cache helpers (identical to FileStorage) --------------------------------

func (d *DirectoryFileStorage) updateCacheUnsafe(conv *conversation.Conversation) {
	if len(d.cache) >= d.cacheSize && d.cache[conv.ID] == nil {
		d.evictLRUUnsafe()
	}
	d.cache[conv.ID] = &cachedConversation{
		conv:         cloneConversation(conv),
		lastAccessed: time.Now(),
		dirty:        false,
	}
	d.updateLRUUnsafe(conv.ID)
}

func (d *DirectoryFileStorage) updateLRUUnsafe(id string) {
	d.removeLRUUnsafe(id)
	d.cacheLRU = append([]string{id}, d.cacheLRU...)
}

func (d *DirectoryFileStorage) removeLRUUnsafe(id string) {
	for i, v := range d.cacheLRU {
		if v == id {
			d.cacheLRU = append(d.cacheLRU[:i], d.cacheLRU[i+1:]...)
			return
		}
	}
}

func (d *DirectoryFileStorage) evictLRUUnsafe() {
	if len(d.cacheLRU) == 0 {
		return
	}
	evictID := d.cacheLRU[len(d.cacheLRU)-1]
	d.cacheLRU = d.cacheLRU[:len(d.cacheLRU)-1]
	if cached, ok := d.cache[evictID]; ok && cached.dirty {
		_ = d.writeToDiskUnsafe(cached.conv)
	}
	delete(d.cache, evictID)
}

// compactUnsafe applies simple tail-retention compaction (see FileStorage for full logic).
func (d *DirectoryFileStorage) compactUnsafe(conv *conversation.Conversation) *conversation.Conversation {
	targetTokens := int(float64(d.maxContextWindow) * d.minRetainPercent)
	keepN := 0
	current := 0
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		t := 0
		if conv.Messages[i].Tokens != nil {
			t = conv.Messages[i].Tokens.Total
		} else {
			t = len(conv.Messages[i].Content) / 4
		}
		if current+t > targetTokens {
			break
		}
		current += t
		keepN++
	}
	if keepN == 0 {
		keepN = 1
	}
	start := max(len(conv.Messages)-keepN, 0)

	compacted := cloneConversation(conv)
	compacted.Messages = conv.Messages[start:]
	compacted.TotalTokens = current
	return compacted
}
