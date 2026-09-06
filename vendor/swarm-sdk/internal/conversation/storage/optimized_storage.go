// Package storage provides optimized conversation storage with compression and pooling
package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/pkg/fastjson"
	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/klauspost/compress/zstd"
)

// OptimizedStorage wraps conversation storage with performance optimizations
type OptimizedStorage struct {
	backend     Storage
	poolManager *pool.Manager
	jsonEngine  *fastjson.Engine

	// Compression for storage and transmission
	compressionLevel int
	useZstd          bool // Zstd is faster than gzip

	// Write coalescing
	writeBuffer   *WriteBuffer
	flushInterval time.Duration

	// Read cache for hot conversations
	cache     *ConversationCache
	cacheSize int

	// Object pools
	bufferPool     sync.Pool
	compressorPool sync.Pool

	// Metrics
	metrics *OptimizedStorageMetrics
}

// OptimizedStorageMetrics tracks storage performance
type OptimizedStorageMetrics struct {
	// Operations
	Reads       uint64
	Writes      uint64
	CacheHits   uint64
	CacheMisses uint64

	// Performance
	CompressionRatio float64
	AvgReadLatency   uint64 // nanoseconds
	AvgWriteLatency  uint64 // nanoseconds

	// Memory
	BytesSaved      uint64
	AllocReductions uint64
}

// NewOptimizedStorage creates an optimized storage wrapper
func NewOptimizedStorage(backend Storage, poolManager *pool.Manager) *OptimizedStorage {
	s := &OptimizedStorage{
		backend:          backend,
		poolManager:      poolManager,
		jsonEngine:       fastjson.NewEngine(poolManager),
		compressionLevel: 1, // Fast compression for CloudFlare
		useZstd:          true,
		flushInterval:    100 * time.Millisecond,
		cacheSize:        100,
		metrics:          &OptimizedStorageMetrics{},
	}

	// Initialize write buffer
	s.writeBuffer = NewWriteBuffer(s.flushToBackend, s.flushInterval)

	// Initialize cache
	s.cache = NewConversationCache(s.cacheSize)

	// Initialize pools
	s.bufferPool = sync.Pool{
		New: func() any {
			return bytes.NewBuffer(make([]byte, 0, 8192))
		},
	}

	if s.useZstd {
		s.compressorPool = sync.Pool{
			New: func() any {
				encoder, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
				return encoder
			},
		}
	} else {
		s.compressorPool = sync.Pool{
			New: func() any {
				return gzip.NewWriter(nil)
			},
		}
	}

	return s
}

// Save saves a conversation with optimizations
func (s *OptimizedStorage) Save(ctx context.Context, conv *conversation.Conversation) error {
	start := time.Now()
	defer func() {
		s.metrics.Writes++
		s.metrics.AvgWriteLatency = uint64(time.Since(start).Nanoseconds())
	}()

	// Update cache
	s.cache.Put(conv.ID, conv)

	// Compress conversation data
	compressed, err := s.compressConversation(ctx, conv)
	if err != nil {
		return fmt.Errorf("compression failed: %w", err)
	}

	// Calculate compression ratio
	originalSize := s.estimateConversationSize(conv)
	compressedSize := len(compressed)
	s.metrics.CompressionRatio = float64(originalSize) / float64(compressedSize)
	s.metrics.BytesSaved += uint64(originalSize - compressedSize)

	// Queue for write coalescing
	return s.writeBuffer.Add(conv.ID, compressed)
}

// Load loads a conversation with caching
func (s *OptimizedStorage) Load(ctx context.Context, id string) (*conversation.Conversation, error) {
	start := time.Now()
	defer func() {
		s.metrics.Reads++
		s.metrics.AvgReadLatency = uint64(time.Since(start).Nanoseconds())
	}()

	// Check cache first
	if conv, ok := s.cache.Get(id); ok {
		s.metrics.CacheHits++
		return conv, nil
	}
	s.metrics.CacheMisses++

	// Load from backend
	conv, err := s.backend.Load(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update cache
	s.cache.Put(id, conv)

	return conv, nil
}

// compressConversation compresses conversation data efficiently
func (s *OptimizedStorage) compressConversation(ctx context.Context, conv *conversation.Conversation) ([]byte, error) {
	// Get buffer from pool
	buf := s.bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		s.bufferPool.Put(buf)
	}()

	// Serialize to JSON first
	data, err := s.jsonEngine.Marshal(conv)
	if err != nil {
		return nil, fmt.Errorf("json marshal failed: %w", err)
	}

	// Skip compression for small data
	if len(data) < 1024 {
		return data, nil
	}

	// Compress using pool
	compressed := make([]byte, 0, len(data)/2) // Estimate 50% compression

	err = s.poolManager.Submit(ctx, pool.PoolTypeCompression, func() {
		if s.useZstd {
			encoder := s.compressorPool.Get().(*zstd.Encoder)
			defer s.compressorPool.Put(encoder)

			encoder.Reset(buf)
			_, err = encoder.Write(data)
			if err == nil {
				err = encoder.Close()
				if err == nil {
					compressed = buf.Bytes()
				}
			}
		} else {
			gzWriter := s.compressorPool.Get().(*gzip.Writer)
			defer s.compressorPool.Put(gzWriter)

			gzWriter.Reset(buf)
			_, err = gzWriter.Write(data)
			if err == nil {
				err = gzWriter.Close()
				if err == nil {
					compressed = buf.Bytes()
				}
			}
		}
	})

	if err != nil {
		// Fallback to uncompressed
		return data, nil
	}

	return compressed, nil
}

// estimateConversationSize estimates the size of a conversation
func (s *OptimizedStorage) estimateConversationSize(conv *conversation.Conversation) int {
	// Rough estimation
	size := 100 // Base overhead
	for _, msg := range conv.Messages {
		size += len(msg.Content) + 50 // Content + metadata
		for _, tc := range msg.ToolCalls {
			size += len(tc.Name) + 50 // Name + metadata
			// Estimate parameters size
			if paramsData, err := s.jsonEngine.Marshal(tc.Parameters); err == nil {
				size += len(paramsData)
			}
		}
	}
	return size
}

// ConversationCache provides thread-safe LRU caching
type ConversationCache struct {
	mu       sync.RWMutex
	data     map[string]*cacheEntry
	order    []string
	capacity int
}

type cacheEntry struct {
	conv     *conversation.Conversation
	accessed time.Time
	size     int
}

func NewConversationCache(capacity int) *ConversationCache {
	return &ConversationCache{
		data:     make(map[string]*cacheEntry),
		order:    make([]string, 0, capacity),
		capacity: capacity,
	}
}

func (c *ConversationCache) Get(id string) (*conversation.Conversation, bool) {
	c.mu.RLock()
	entry, ok := c.data[id]
	c.mu.RUnlock()

	if ok {
		c.mu.Lock()
		entry.accessed = time.Now()
		c.mu.Unlock()
		return entry.conv, true
	}

	return nil, false
}

func (c *ConversationCache) Put(id string, conv *conversation.Conversation) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if already exists
	if _, exists := c.data[id]; exists {
		c.data[id].conv = conv
		c.data[id].accessed = time.Now()
		return
	}

	// Evict if at capacity
	if len(c.data) >= c.capacity {
		c.evictOldest()
	}

	// Add new entry
	c.data[id] = &cacheEntry{
		conv:     conv,
		accessed: time.Now(),
		size:     estimateSize(conv),
	}
	c.order = append(c.order, id)
}

func (c *ConversationCache) evictOldest() {
	if len(c.order) == 0 {
		return
	}

	// Simple FIFO for now, could be LRU
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.data, oldest)
}

func estimateSize(conv *conversation.Conversation) int {
	size := 100
	for _, msg := range conv.Messages {
		size += len(msg.Content) + 100
	}
	return size
}

// WriteBuffer coalesces writes for efficiency
type WriteBuffer struct {
	mu            sync.Mutex
	pending       map[string][]byte
	flushFunc     func(map[string][]byte) error
	flushInterval time.Duration
	timer         *time.Timer
}

func NewWriteBuffer(flushFunc func(map[string][]byte) error, interval time.Duration) *WriteBuffer {
	wb := &WriteBuffer{
		pending:       make(map[string][]byte),
		flushFunc:     flushFunc,
		flushInterval: interval,
	}
	wb.timer = time.AfterFunc(interval, wb.flush)
	return wb
}

func (wb *WriteBuffer) Add(id string, data []byte) error {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	wb.pending[id] = data

	// Flush if buffer is getting large
	if len(wb.pending) >= 10 {
		return wb.flushLocked()
	}

	// Reset timer
	wb.timer.Reset(wb.flushInterval)
	return nil
}

func (wb *WriteBuffer) flush() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.flushLocked()
}

func (wb *WriteBuffer) flushLocked() error {
	if len(wb.pending) == 0 {
		return nil
	}

	// Copy pending data
	toFlush := wb.pending
	wb.pending = make(map[string][]byte)

	// Flush in background
	go func() {
		if err := wb.flushFunc(toFlush); err != nil {
			// Log error
			slog.Error("storage.write_buffer_flush_error", "error", err)
		}
	}()

	return nil
}

// flushToBackend is called by WriteBuffer to persist buffered conversation data.
// data maps conversation IDs to zstd-compressed conversation bytes.
//
// NOTE: This flush path is not yet implemented — compressed bytes are dropped.
// A complete implementation would decompress each entry and call s.backend.Save.
// Until then, callers that require durability should use FileStorage or AsyncStorage directly.
func (s *OptimizedStorage) flushToBackend(data map[string][]byte) error {
	// TODO: decompress each entry and delegate to s.backend.Save.
	_ = data
	return nil
}
