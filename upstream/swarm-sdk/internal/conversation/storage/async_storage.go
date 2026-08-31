// Package storage provides async, decoupled storage operations
package storage

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/pkg/fastjson"
	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// AsyncStorage provides decoupled storage operations
type AsyncStorage struct {
	// Underlying storage backend
	backend Storage

	// JSON engine for optimized marshaling
	jsonEngine *fastjson.Engine

	// Work queues
	saveQueue   chan SaveRequest
	loadQueue   chan LoadRequest
	deleteQueue chan DeleteRequest

	// Workers
	workers    []*StorageWorker
	workerPool *pool.Manager

	// Pre-rendered cache
	renderCache *RenderCache

	// Stats
	stats *StorageStats

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Request types
type SaveRequest struct {
	ID           string
	Conversation *conversation.Conversation
	Priority     int
	Callback     func(error)
}

type LoadRequest struct {
	ID       string
	Callback func(*conversation.Conversation, error)
}

type DeleteRequest struct {
	ID       string
	Callback func(error)
}

// StorageWorker processes storage operations
type StorageWorker struct {
	id      int
	storage *AsyncStorage
}

// StorageStats tracks performance metrics
type StorageStats struct {
	SaveRequests    uint64
	SaveCompletions uint64
	SaveErrors      uint64
	AvgSaveTime     uint64 // nanoseconds

	LoadRequests    uint64
	LoadCompletions uint64
	LoadFromCache   uint64
	AvgLoadTime     uint64

	QueueDepth    uint64
	WorkersActive uint64
}

// RenderCache pre-renders conversations for fast UI updates
type RenderCache struct {
	mu       sync.RWMutex
	rendered map[string]*RenderedConversation
	maxSize  int
}

type RenderedConversation struct {
	ID           string
	HTML         string
	LastMessages []RenderedMessage
	UpdatedAt    time.Time
}

type RenderedMessage struct {
	ID      string
	HTML    string
	Preview string
}

// NewAsyncStorage creates a new async storage wrapper
func NewAsyncStorage(backend Storage, poolManager *pool.Manager) *AsyncStorage {
	ctx, cancel := context.WithCancel(context.Background())

	s := &AsyncStorage{
		backend:     backend,
		jsonEngine:  fastjson.NewEngine(poolManager),
		saveQueue:   make(chan SaveRequest, 100),
		loadQueue:   make(chan LoadRequest, 100),
		deleteQueue: make(chan DeleteRequest, 10),
		renderCache: NewRenderCache(1000),
		stats:       &StorageStats{},
		workerPool:  poolManager,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Start workers
	workerCount := 4
	s.workers = make([]*StorageWorker, workerCount)
	for i := range workerCount {
		w := &StorageWorker{id: i, storage: s}
		s.workers[i] = w
		s.wg.Add(1)
		go w.run()
	}

	// Start stats reporter
	s.wg.Add(1)
	go s.statsReporter()

	return s
}

// Save asynchronously saves a conversation
func (s *AsyncStorage) Save(ctx context.Context, conv *conversation.Conversation) error {
	atomic.AddUint64(&s.stats.SaveRequests, 1)

	// Pre-render for cache
	s.preRender(conv)

	// Create request
	req := SaveRequest{
		ID:           conv.ID,
		Conversation: conv,
		Priority:     0,
	}

	// Try to queue
	select {
	case s.saveQueue <- req:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Queue full, bump priority and try again with timeout
		req.Priority = 1
		select {
		case s.saveQueue <- req:
			return nil
		case <-time.After(100 * time.Millisecond):
			return fmt.Errorf("save queue full")
		}
	}
}

// SaveAsync saves with a callback
func (s *AsyncStorage) SaveAsync(conv *conversation.Conversation, callback func(error)) {
	atomic.AddUint64(&s.stats.SaveRequests, 1)

	// Pre-render immediately
	s.preRender(conv)

	req := SaveRequest{
		ID:           conv.ID,
		Conversation: conv,
		Callback:     callback,
	}

	// Non-blocking send
	select {
	case s.saveQueue <- req:
		// Queued successfully
	default:
		// Queue full, call callback with error
		if callback != nil {
			callback(fmt.Errorf("save queue full"))
		}
	}
}

// Load loads a conversation (checks cache first)
func (s *AsyncStorage) Load(ctx context.Context, id string) (*conversation.Conversation, error) {
	atomic.AddUint64(&s.stats.LoadRequests, 1)

	// Check render cache for metadata
	if cached := s.renderCache.Get(id); cached != nil {
		atomic.AddUint64(&s.stats.LoadFromCache, 1)
		// Cache hit, but still need to load full conversation
		// In production, we'd store more data in cache
	}

	// Create response channel
	respChan := make(chan struct {
		conv *conversation.Conversation
		err  error
	}, 1)

	req := LoadRequest{
		ID: id,
		Callback: func(c *conversation.Conversation, e error) {
			respChan <- struct {
				conv *conversation.Conversation
				err  error
			}{c, e}
		},
	}

	// Queue request
	select {
	case s.loadQueue <- req:
		// Wait for response
		select {
		case resp := <-respChan:
			if resp.err == nil {
				s.preRender(resp.conv)
			}
			return resp.conv, resp.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Worker implementation
func (w *StorageWorker) run() {
	defer w.storage.wg.Done()

	for {
		atomic.AddUint64(&w.storage.stats.WorkersActive, 1)

		select {
		case req := <-w.storage.saveQueue:
			w.processSave(req)

		case req := <-w.storage.loadQueue:
			w.processLoad(req)

		case req := <-w.storage.deleteQueue:
			w.processDelete(req)

		case <-w.storage.ctx.Done():
			atomic.AddUint64(&w.storage.stats.WorkersActive, ^uint64(0))
			return
		}

		atomic.AddUint64(&w.storage.stats.WorkersActive, ^uint64(0))
	}
}

func (w *StorageWorker) processSave(req SaveRequest) {
	start := time.Now()

	// Use async JSON marshaling
	var marshalErr error
	var marshalDone = make(chan struct{})

	w.storage.jsonEngine.MarshalAsync(req.Conversation, func(data []byte, err error) {
		marshalErr = err
		close(marshalDone)
	})

	// Do other work while marshaling happens
	// (In real implementation, we'd prepare file paths, etc.)

	// Wait for marshal to complete
	<-marshalDone

	if marshalErr != nil {
		atomic.AddUint64(&w.storage.stats.SaveErrors, 1)
		if req.Callback != nil {
			req.Callback(marshalErr)
		}
		return
	}

	// Save using backend
	err := w.storage.backend.Save(context.Background(), req.Conversation)

	// Update stats
	duration := time.Since(start)
	atomic.AddUint64(&w.storage.stats.SaveCompletions, 1)
	if err != nil {
		atomic.AddUint64(&w.storage.stats.SaveErrors, 1)
	}

	// Update average save time (simplified - in production use proper averaging)
	atomic.StoreUint64(&w.storage.stats.AvgSaveTime, uint64(duration.Nanoseconds()))

	// Call callback
	if req.Callback != nil {
		req.Callback(err)
	}
}

func (w *StorageWorker) processLoad(req LoadRequest) {
	start := time.Now()

	// Load from backend
	conv, err := w.storage.backend.Load(context.Background(), req.ID)

	// Update stats
	duration := time.Since(start)
	atomic.AddUint64(&w.storage.stats.LoadCompletions, 1)
	atomic.StoreUint64(&w.storage.stats.AvgLoadTime, uint64(duration.Nanoseconds()))

	// Call callback
	if req.Callback != nil {
		req.Callback(conv, err)
	}
}

func (w *StorageWorker) processDelete(req DeleteRequest) {
	err := w.storage.backend.Delete(context.Background(), req.ID)
	if req.Callback != nil {
		req.Callback(err)
	}
}

// preRender pre-renders a conversation for fast UI updates
func (s *AsyncStorage) preRender(conv *conversation.Conversation) {
	// In production, this would actually render HTML/markdown
	// For now, just cache the conversation metadata

	rendered := &RenderedConversation{
		ID:        conv.ID,
		UpdatedAt: time.Now(),
		HTML:      fmt.Sprintf("<div>Conversation %s</div>", conv.ID),
	}

	// Extract last few messages
	if len(conv.Messages) > 0 {
		start := max(len(conv.Messages)-3, 0)

		for i := start; i < len(conv.Messages); i++ {
			msg := conv.Messages[i]
			rendered.LastMessages = append(rendered.LastMessages, RenderedMessage{
				ID:      msg.ID,
				Preview: truncate(msg.Content, 100),
				HTML:    fmt.Sprintf("<div>%s</div>", msg.Content),
			})
		}
	}

	s.renderCache.Put(conv.ID, rendered)
}

// Stats reporter
func (s *AsyncStorage) statsReporter() {
	defer s.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats := s.GetStats()
			if stats.SaveRequests > 0 {
				fmt.Printf("Storage Stats: Saves=%d/%d (%.1f%% success), Loads=%d (%.1f%% cached), Queue=%d\n",
					stats.SaveCompletions,
					stats.SaveRequests,
					float64(stats.SaveCompletions-stats.SaveErrors)*100/float64(stats.SaveRequests),
					stats.LoadRequests,
					float64(stats.LoadFromCache)*100/float64(max(stats.LoadRequests, 1)),
					atomic.LoadUint64(&s.stats.QueueDepth),
				)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// GetStats returns current statistics
func (s *AsyncStorage) GetStats() StorageStats {
	return StorageStats{
		SaveRequests:    atomic.LoadUint64(&s.stats.SaveRequests),
		SaveCompletions: atomic.LoadUint64(&s.stats.SaveCompletions),
		SaveErrors:      atomic.LoadUint64(&s.stats.SaveErrors),
		AvgSaveTime:     atomic.LoadUint64(&s.stats.AvgSaveTime),
		LoadRequests:    atomic.LoadUint64(&s.stats.LoadRequests),
		LoadCompletions: atomic.LoadUint64(&s.stats.LoadCompletions),
		LoadFromCache:   atomic.LoadUint64(&s.stats.LoadFromCache),
		AvgLoadTime:     atomic.LoadUint64(&s.stats.AvgLoadTime),
		QueueDepth:      uint64(len(s.saveQueue)),
		WorkersActive:   atomic.LoadUint64(&s.stats.WorkersActive),
	}
}

// Shutdown gracefully shuts down the storage
func (s *AsyncStorage) Shutdown() {
	s.cancel()
	s.wg.Wait()
	s.jsonEngine.Shutdown()
}

// RenderCache implementation
func NewRenderCache(maxSize int) *RenderCache {
	return &RenderCache{
		rendered: make(map[string]*RenderedConversation),
		maxSize:  maxSize,
	}
}

func (c *RenderCache) Get(id string) *RenderedConversation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rendered[id]
}

func (c *RenderCache) Put(id string, rendered *RenderedConversation) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simple eviction
	if len(c.rendered) >= c.maxSize {
		// Delete oldest
		var oldest string
		var oldestTime time.Time
		for k, v := range c.rendered {
			if oldest == "" || v.UpdatedAt.Before(oldestTime) {
				oldest = k
				oldestTime = v.UpdatedAt
			}
		}
		delete(c.rendered, oldest)
	}

	c.rendered[id] = rendered
}

// Helper functions
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// Interface compliance
func (s *AsyncStorage) List(ctx context.Context) ([]*conversation.Conversation, error) {
	return s.backend.List(ctx)
}

func (s *AsyncStorage) Delete(ctx context.Context, id string) error {
	req := DeleteRequest{ID: id}
	select {
	case s.deleteQueue <- req:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

/*
func (s *AsyncStorage) DeleteAll(ctx context.Context) error {
	return s.backend.DeleteAll(ctx)
}

func (s *AsyncStorage) Metrics(ctx context.Context) (*StorageMetrics, error) {
	return s.backend.Metrics(ctx)
}
*/
