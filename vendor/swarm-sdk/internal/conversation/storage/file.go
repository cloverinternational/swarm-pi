package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// FileStorage implements Storage using JSON files with caching and compaction.
// Stores conversations in ~/.swarm/conversations/ (paths.ConversationsDir) by default.
// Thread-safe with LRU cache for performance.
type FileStorage struct {
	mu      sync.RWMutex
	baseDir string
	closed  bool

	// Cache layer
	cache     map[string]*cachedConversation
	cacheSize int
	cacheLRU  []string // LRU tracking

	// Compaction settings (context-window aware)
	compactionThresholdTokens int     // Calculated from percent * context window
	maxContextWindow          int     // Max tokens for the model
	minRetainPercent          float64 // Percent of messages to keep
	autoCompact               bool

	// Metrics
	metrics *FileStorageMetrics
}

// cachedConversation wraps a conversation with cache metadata.
type cachedConversation struct {
	conv         *conversation.Conversation
	lastAccessed time.Time
	dirty        bool
}

// FileStorageMetrics tracks storage statistics.
type FileStorageMetrics struct {
	mu            sync.RWMutex
	CacheHits     int64
	CacheMisses   int64
	DiskReads     int64
	DiskWrites    int64
	Compactions   int64
	CachedEntries int
}

// FileStorageConfig provides configuration for file storage.
type FileStorageConfig struct {
	BaseDir string

	// Cache configuration
	CacheSize int

	// Compaction configuration (context-window aware)
	// CompactionThresholdPercent is the percentage of context window at which to compact (default: 80)
	CompactionThresholdPercent float64

	// MaxContextWindow is the model's max context window in tokens (default: 200000 when unknown)
	MaxContextWindow int

	// MinRetainPercent is the percentage of recent messages to keep after compaction (default: 30)
	MinRetainPercent float64

	// AutoCompact enables automatic compaction when threshold is reached
	AutoCompact bool
}

// NewFileStorage creates a new file-based storage with caching.
func NewFileStorage(config FileStorageConfig) (*FileStorage, error) {
	if config.BaseDir == "" {
		config.BaseDir = paths.ConversationsDir()
	}

	if config.CacheSize <= 0 {
		config.CacheSize = 100
	}

	// Context-window aware compaction defaults
	if config.MaxContextWindow <= 0 {
		config.MaxContextWindow = 200_000 // Unknown-model fallback; resolved model limits should be supplied.
	}

	if config.CompactionThresholdPercent <= 0 {
		config.CompactionThresholdPercent = 0.80 // Default: 80% of context window
	}

	if config.MinRetainPercent <= 0 {
		config.MinRetainPercent = 0.30 // Default: retain 30% of messages
	}

	// Calculate threshold in tokens
	compactionThresholdTokens := int(float64(config.MaxContextWindow) * config.CompactionThresholdPercent)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(config.BaseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &FileStorage{
		baseDir:                   config.BaseDir,
		closed:                    false,
		cache:                     make(map[string]*cachedConversation),
		cacheSize:                 config.CacheSize,
		cacheLRU:                  make([]string, 0, config.CacheSize),
		compactionThresholdTokens: compactionThresholdTokens,
		maxContextWindow:          config.MaxContextWindow,
		minRetainPercent:          config.MinRetainPercent,
		autoCompact:               config.AutoCompact,
		metrics:                   &FileStorageMetrics{},
	}, nil
}

// Save saves or updates a conversation with caching and optional compaction.
func (f *FileStorage) Save(ctx context.Context, conv *conversation.Conversation) error {
	if conv == nil || conv.ID == "" {
		return ErrInvalidID
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return ErrStorageClosed
	}

	// Auto-compact if current context size exceeds threshold.
	// NOTE: TotalTokens MUST NOT be used directly here as it double-counts
	// history; estimateContextSize prefers CurrentContextSize, then per-message
	// totals, and only falls back to TotalTokens as a last resort.
	if f.autoCompact && estimateContextSize(conv) > f.compactionThresholdTokens {
		conv = f.compactConversationUnsafe(conv)
		f.metrics.mu.Lock()
		f.metrics.Compactions++
		f.metrics.mu.Unlock()
	}

	// Update cache
	f.updateCacheUnsafe(conv)

	// Write to disk atomically
	if err := f.writeToDiskUnsafe(conv); err != nil {
		return fmt.Errorf("failed to write conversation to disk: %w", err)
	}

	f.metrics.mu.Lock()
	f.metrics.DiskWrites++
	f.metrics.mu.Unlock()

	return nil
}

// Load retrieves a conversation by ID with caching.
func (f *FileStorage) Load(ctx context.Context, id string) (*conversation.Conversation, error) {
	if id == "" {
		return nil, ErrInvalidID
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil, ErrStorageClosed
	}

	// Check cache first
	if cached, exists := f.cache[id]; exists {
		f.updateLRUUnsafe(id)
		cached.lastAccessed = time.Now()

		f.metrics.mu.Lock()
		f.metrics.CacheHits++
		f.metrics.mu.Unlock()

		return cloneConversation(cached.conv), nil
	}

	// Cache miss - load from disk
	f.metrics.mu.Lock()
	f.metrics.CacheMisses++
	f.metrics.DiskReads++
	f.metrics.mu.Unlock()

	filename := filepath.Join(f.baseDir, id+".json")
	data, err := os.ReadFile(filename)
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

	// Add to cache
	f.updateCacheUnsafe(&conv)

	return cloneConversation(&conv), nil
}

// Delete removes a conversation by ID from cache and disk.
func (f *FileStorage) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidID
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return ErrStorageClosed
	}

	// Remove from cache
	delete(f.cache, id)
	f.removeLRUUnsafe(id)

	// Delete file
	filename := filepath.Join(f.baseDir, id+".json")
	if err := os.Remove(filename); err != nil {
		if os.IsNotExist(err) {
			return nil // Idempotent
		}
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// Query finds conversations matching the filter with cache awareness.
func (f *FileStorage) Query(ctx context.Context, filter Filter) ([]*conversation.Conversation, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.closed {
		return nil, ErrStorageClosed
	}

	entries, err := os.ReadDir(f.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	results := make([]*conversation.Conversation, 0)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")

		// Try cache first
		var conv *conversation.Conversation
		if cached, exists := f.cache[id]; exists {
			conv = cached.conv
		} else {
			// Load from disk
			filename := filepath.Join(f.baseDir, entry.Name())
			data, err := os.ReadFile(filename)
			if err != nil {
				continue
			}

			var c conversation.Conversation
			if err := json.Unmarshal(data, &c); err != nil {
				continue
			}
			conv = &c
		}

		if matchesFilter(conv, filter) {
			results = append(results, cloneConversation(conv))
		}

		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}

	sortResults(results, filter)

	if filter.Offset > 0 && filter.Offset < len(results) {
		results = results[filter.Offset:]
	}

	return results, nil
}

// List returns all conversations.
func (f *FileStorage) List(ctx context.Context) ([]*conversation.Conversation, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.closed {
		return nil, ErrStorageClosed
	}

	// Read all conversation files
	entries, err := os.ReadDir(f.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	results := make([]*conversation.Conversation, 0)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		// Read and parse conversation
		filename := filepath.Join(f.baseDir, entry.Name())
		data, err := os.ReadFile(filename)
		if err != nil {
			continue // Skip files we can't read
		}

		var conv conversation.Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue // Skip invalid files
		}

		results = append(results, cloneConversation(&conv))
	}

	return results, nil
}

// Exists checks if a conversation exists.
func (f *FileStorage) Exists(ctx context.Context, id string) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.closed {
		return false, ErrStorageClosed
	}

	filename := filepath.Join(f.baseDir, id+".json")
	_, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// Stream streams messages from a conversation as they are added.
func (f *FileStorage) Stream(ctx context.Context, id string) (<-chan *conversation.Message, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.closed {
		return nil, ErrStorageClosed
	}

	// Check if conversation exists
	exists, err := f.Exists(ctx, id)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrConversationNotFound
	}

	// For file storage, streaming is not implemented
	// Return a closed channel
	ch := make(chan *conversation.Message)
	close(ch)
	return ch, nil
}

// Close closes the storage and flushes dirty cache entries.
func (f *FileStorage) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil
	}

	// Flush dirty cache entries
	for id, cached := range f.cache {
		if cached.dirty {
			if err := f.writeToDiskUnsafe(cached.conv); err != nil {
				return fmt.Errorf("failed to flush conversation %s: %w", id, err)
			}
		}
	}

	f.closed = true
	f.cache = nil
	f.cacheLRU = nil

	return nil
}

// Compact performs manual compaction on a conversation.
func (f *FileStorage) Compact(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return ErrStorageClosed
	}

	// Load conversation
	var conv *conversation.Conversation
	if cached, exists := f.cache[id]; exists {
		conv = cached.conv
	} else {
		filename := filepath.Join(f.baseDir, id+".json")
		data, err := os.ReadFile(filename)
		if err != nil {
			if os.IsNotExist(err) {
				return ErrConversationNotFound
			}
			return fmt.Errorf("failed to read file: %w", err)
		}

		var c conversation.Conversation
		if err := json.Unmarshal(data, &c); err != nil {
			return fmt.Errorf("failed to unmarshal conversation: %w", err)
		}
		conv = &c
	}

	// Compact
	compacted := f.compactConversationUnsafe(conv)

	// Write back
	if err := f.writeToDiskUnsafe(compacted); err != nil {
		return fmt.Errorf("failed to write compacted conversation: %w", err)
	}

	// Update cache
	if cached, exists := f.cache[id]; exists {
		cached.conv = compacted
		cached.dirty = false
	}

	f.metrics.mu.Lock()
	f.metrics.Compactions++
	f.metrics.mu.Unlock()

	return nil
}

// CacheMetrics returns cache statistics.
func (f *FileStorage) CacheMetrics() *FileStorageMetrics {
	f.metrics.mu.RLock()
	defer f.metrics.mu.RUnlock()

	f.mu.RLock()
	cached := len(f.cache)
	f.mu.RUnlock()

	metrics := FileStorageMetrics{
		CacheHits:     f.metrics.CacheHits,
		CacheMisses:   f.metrics.CacheMisses,
		DiskReads:     f.metrics.DiskReads,
		DiskWrites:    f.metrics.DiskWrites,
		Compactions:   f.metrics.Compactions,
		CachedEntries: cached,
	}
	return &metrics
}

// Metrics returns storage statistics.
func (f *FileStorage) Metrics(ctx context.Context) (*StorageMetrics, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Count conversations and calculate total size
	entries, err := os.ReadDir(f.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var count int64
	var totalSize int64

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		count++

		info, err := entry.Info()
		if err == nil {
			totalSize += info.Size()
		}
	}

	return &StorageMetrics{
		TotalConversations: count,
		TotalSizeBytes:     totalSize,
		BackendType:        "file",
	}, nil
}

// --- Internal helpers (unsafe = caller must hold lock) ---

// writeToDiskUnsafe atomically writes a conversation to disk.
func (f *FileStorage) writeToDiskUnsafe(conv *conversation.Conversation) error {
	filename := filepath.Join(f.baseDir, conv.ID+".json")

	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation: %w", err)
	}

	// Atomic write (temp file -> fsync -> rename -> fsync dir) via atomicfile.
	if err := atomicfile.Write(filename, data, atomicfile.WithPerm(0o644)); err != nil {
		return fmt.Errorf("failed to write conversation file: %w", err)
	}

	return nil
}

// updateCacheUnsafe adds or updates a conversation in the cache.
func (f *FileStorage) updateCacheUnsafe(conv *conversation.Conversation) {
	// Evict if cache is full and this is a new entry
	if len(f.cache) >= f.cacheSize && f.cache[conv.ID] == nil {
		f.evictLRUUnsafe()
	}

	f.cache[conv.ID] = &cachedConversation{
		conv:         cloneConversation(conv),
		lastAccessed: time.Now(),
		dirty:        false,
	}

	f.updateLRUUnsafe(conv.ID)
}

// updateLRUUnsafe moves an ID to the front of the LRU list.
func (f *FileStorage) updateLRUUnsafe(id string) {
	f.removeLRUUnsafe(id)
	f.cacheLRU = append([]string{id}, f.cacheLRU...)
}

// removeLRUUnsafe removes an ID from the LRU list.
func (f *FileStorage) removeLRUUnsafe(id string) {
	for i, cachedID := range f.cacheLRU {
		if cachedID == id {
			f.cacheLRU = append(f.cacheLRU[:i], f.cacheLRU[i+1:]...)
			break
		}
	}
}

// evictLRUUnsafe evicts the least recently used entry.
func (f *FileStorage) evictLRUUnsafe() {
	if len(f.cacheLRU) == 0 {
		return
	}

	evictID := f.cacheLRU[len(f.cacheLRU)-1]
	f.cacheLRU = f.cacheLRU[:len(f.cacheLRU)-1]

	if cached, exists := f.cache[evictID]; exists && cached.dirty {
		f.writeToDiskUnsafe(cached.conv)
	}

	delete(f.cache, evictID)
}

// compactConversationUnsafe reduces conversation size based on context window usage.
// Uses a context-aware strategy that keeps recent messages within minRetainPercent of context.
// CRITICAL: Maintains tool_use/tool_result pairing to avoid API errors.
// estimateContextSize returns the best available estimate of how many context
// tokens a conversation is carrying, for threshold decisions. Order of
// preference:
//  1. conv.CurrentContextSize (authoritative, set after an assistant turn)
//  2. the latest assistant message's Input+Output (history is folded into Input)
//  3. the sum of per-message Total tokens (handles user-only conversations)
//  4. conv.TotalTokens as a last resort
//
// TotalTokens is only used as a last resort because it can double-count history.
func estimateContextSize(conv *conversation.Conversation) int {
	if conv.CurrentContextSize > 0 {
		return conv.CurrentContextSize
	}

	size := 0
	for _, msg := range conv.Messages {
		if msg.Tokens != nil && msg.Role == conversation.RoleAssistant {
			size = msg.Tokens.Input + msg.Tokens.Output
		}
	}
	if size > 0 {
		return size
	}

	// No assistant messages to anchor the estimate (e.g. a user-only
	// conversation) — sum per-message totals so the signal is non-zero.
	for _, msg := range conv.Messages {
		if msg.Tokens != nil {
			size += msg.Tokens.Total
		}
	}
	if size > 0 {
		return size
	}

	return conv.TotalTokens
}

func (f *FileStorage) compactConversationUnsafe(conv *conversation.Conversation) *conversation.Conversation {
	// Use authoritative CurrentContextSize for threshold decisions,
	// falling back to a best-effort estimate when it is not yet set.
	currentSize := estimateContextSize(conv)

	if currentSize <= f.compactionThresholdTokens {
		return conv
	}

	// Calculate target token count (minRetainPercent of max context)
	targetTokens := int(float64(f.maxContextWindow) * f.minRetainPercent)

	// Build maps to track tool_use/tool_result relationships
	toolUseToMsgIdx := make(map[string]int)
	toolResultToMsgIdx := make(map[string]int)
	for i, msg := range conv.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				toolUseToMsgIdx[tc.ID] = i
			}
		}
		for _, tr := range msg.ToolResults {
			if tr.CallID != "" {
				toolResultToMsgIdx[tr.CallID] = i
			}
		}
	}

	// Keep recent messages that fit within target
	keepIdx := make(map[int]bool)
	currentTokens := 0
	removedCount := 0
	removedTokens := 0

	// Work backwards from most recent messages
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if keepIdx[i] {
			continue // Already marked for keeping
		}

		msg := conv.Messages[i]
		msgTokens := 0

		if msg.Tokens != nil {
			msgTokens = msg.Tokens.Total
		} else {
			// Estimate: ~4 chars per token
			msgTokens = len(msg.Content) / 4
		}

		// Calculate additional tokens needed for related tool messages
		relatedTokens := 0
		relatedMsgs := make([]int, 0)

		// If this message has tool_use, include corresponding tool_result
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				if resultIdx, exists := toolResultToMsgIdx[tc.ID]; exists && !keepIdx[resultIdx] {
					relatedMsgs = append(relatedMsgs, resultIdx)
					resultMsg := conv.Messages[resultIdx]
					if resultMsg.Tokens != nil {
						relatedTokens += resultMsg.Tokens.Total
					} else {
						relatedTokens += len(resultMsg.Content) / 4
					}
				}
			}
		}

		// If this message has tool_result, include corresponding tool_use
		for _, tr := range msg.ToolResults {
			if tr.CallID != "" {
				if useIdx, exists := toolUseToMsgIdx[tr.CallID]; exists && !keepIdx[useIdx] {
					relatedMsgs = append(relatedMsgs, useIdx)
					useMsg := conv.Messages[useIdx]
					if useMsg.Tokens != nil {
						relatedTokens += useMsg.Tokens.Total
					} else {
						relatedTokens += len(useMsg.Content) / 4
					}
				}
			}
		}

		if currentTokens+msgTokens+relatedTokens <= targetTokens {
			keepIdx[i] = true
			currentTokens += msgTokens

			// Also mark related messages for keeping
			for _, relIdx := range relatedMsgs {
				keepIdx[relIdx] = true
				relMsg := conv.Messages[relIdx]
				if relMsg.Tokens != nil {
					currentTokens += relMsg.Tokens.Total
				} else {
					currentTokens += len(relMsg.Content) / 4
				}
			}
		} else {
			removedCount++
			removedTokens += msgTokens
		}
	}

	// Build the list of messages to keep
	keepMessages := make([]*conversation.Message, 0)
	for i, msg := range conv.Messages {
		if keepIdx[i] {
			keepMessages = append(keepMessages, msg)
		}
	}

	// Ensure we keep at least the last 10 messages (with tool pairing)
	if len(keepMessages) < 10 && len(conv.Messages) >= 10 {
		// Keep last 10, plus any tool_use messages needed for tool_results in those 10
		keepIdx = make(map[int]bool)
		for i := len(conv.Messages) - 10; i < len(conv.Messages); i++ {
			keepIdx[i] = true
		}

		// Add any tool_use messages that correspond to tool_results in the kept messages
		for i := len(conv.Messages) - 10; i < len(conv.Messages); i++ {
			msg := conv.Messages[i]
			for _, tr := range msg.ToolResults {
				if tr.CallID != "" {
					if useIdx, exists := toolUseToMsgIdx[tr.CallID]; exists {
						keepIdx[useIdx] = true
					}
				}
			}
		}

		keepMessages = make([]*conversation.Message, 0)
		for i, msg := range conv.Messages {
			if keepIdx[i] {
				keepMessages = append(keepMessages, msg)
			}
		}

		// Recalculate tokens
		currentTokens = 0
		for _, msg := range keepMessages {
			if msg.Tokens != nil {
				currentTokens += msg.Tokens.Total
			} else {
				currentTokens += len(msg.Content) / 4
			}
		}
		removedCount = len(conv.Messages) - len(keepMessages)
		removedTokens = currentSize - currentTokens
	}

	// Final validation: remove any orphaned tool_results
	for {
		changed := false
		remainingToolUses := make(map[string]bool)
		for _, msg := range keepMessages {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					remainingToolUses[tc.ID] = true
				}
			}
		}

		newKeepMessages := make([]*conversation.Message, 0, len(keepMessages))
		for _, msg := range keepMessages {
			hasOrphanedResult := false
			for _, tr := range msg.ToolResults {
				if tr.CallID != "" && !remainingToolUses[tr.CallID] {
					hasOrphanedResult = true
					break
				}
			}
			if hasOrphanedResult {
				changed = true
				if msg.Tokens != nil {
					currentTokens -= msg.Tokens.Total
				}
				removedCount++
				continue
			}
			newKeepMessages = append(newKeepMessages, msg)
		}
		keepMessages = newKeepMessages

		if !changed {
			break
		}
	}

	// Ensure first message is from user
	for len(keepMessages) > 0 && keepMessages[0].Role != conversation.RoleUser && keepMessages[0].Role != conversation.RoleSystem {
		msg := keepMessages[0]
		// Remove orphaned tool_results that reference this message's tool_use
		toolIDsToRemove := make(map[string]bool)
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				toolIDsToRemove[tc.ID] = true
			}
		}

		newKeepMessages := make([]*conversation.Message, 0)
		for i, m := range keepMessages {
			if i == 0 {
				continue // Skip the first message we're removing
			}
			hasToolResultToRemove := false
			for _, tr := range m.ToolResults {
				if toolIDsToRemove[tr.CallID] {
					hasToolResultToRemove = true
					break
				}
			}
			if !hasToolResultToRemove {
				newKeepMessages = append(newKeepMessages, m)
			}
		}
		keepMessages = newKeepMessages
	}

	compacted := cloneConversation(conv)

	// Create context-aware summary message
	summaryMsg := &conversation.Message{
		Role: conversation.RoleSystem,
		Content: fmt.Sprintf(
			"[Compacted: %d messages (%d tokens) removed. Retained %d recent messages (%d tokens) within %.0f%% of %dk context window]",
			removedCount,
			removedTokens,
			len(keepMessages),
			currentTokens,
			f.minRetainPercent*100,
			f.maxContextWindow/1000,
		),
		Timestamp: conv.Messages[0].Timestamp,
	}

	// Update messages and token count
	compacted.Messages = append([]*conversation.Message{summaryMsg}, keepMessages...)
	compacted.TotalTokens = currentTokens

	return compacted
}

// sortResults sorts conversations based on filter criteria.
func sortResults(results []*conversation.Conversation, filter Filter) {
	sortBy := filter.SortBy
	if sortBy == "" {
		sortBy = "updated_at"
	}

	sort.Slice(results, func(i, j int) bool {
		var less bool

		switch sortBy {
		case "created_at":
			less = results[i].CreatedAt.Before(results[j].CreatedAt)
		case "updated_at":
			less = results[i].UpdatedAt.Before(results[j].UpdatedAt)
		case "total_tokens":
			less = results[i].TotalTokens < results[j].TotalTokens
		default:
			less = results[i].UpdatedAt.Before(results[j].UpdatedAt)
		}

		// sort.Slice is not stable, so equal keys would otherwise order by
		// whatever sequence the loader happened to produce — which varies with
		// parallel I/O completion order and with cache hits vs disk reads.
		// Conversations saved in the same second are common, and without a
		// tiebreaker they visibly shuffle position between listings. Break
		// ties on the unique ID to make listings reproducible.
		if equalSortKey(results[i], results[j], sortBy) {
			// Mirror the requested direction, otherwise a descending listing
			// would order its ties ascending.
			if filter.SortOrder == SortDescending {
				return results[i].ID > results[j].ID
			}
			return results[i].ID < results[j].ID
		}

		if filter.SortOrder == SortDescending {
			return !less
		}
		return less
	})
}

// equalSortKey reports whether two conversations tie on the active sort key.
func equalSortKey(a, b *conversation.Conversation, sortBy string) bool {
	switch sortBy {
	case "created_at":
		return a.CreatedAt.Equal(b.CreatedAt)
	case "total_tokens":
		return a.TotalTokens == b.TotalTokens
	default: // "updated_at" and any unknown key both sort by UpdatedAt
		return a.UpdatedAt.Equal(b.UpdatedAt)
	}
}
