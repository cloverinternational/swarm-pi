// Package prerender provides background pre-rendering capabilities for heavy content.
// This decouples the rendering work from the UI thread, allowing smooth scrolling
// even when processing large amounts of bash output.
package prerender

import (
	"sync"
	"sync/atomic"
)

// RenderJob represents a unit of work to be pre-rendered.
type RenderJob struct {
	MessageID string   // Unique identifier for the message
	Content   string   // Raw content to render
	Renderer  Renderer // Renderer to use for this job
}

// RenderResult contains the pre-rendered output.
type RenderResult struct {
	MessageID string   // Message ID this result belongs to
	Lines     []string // Pre-rendered lines ready for display
	Error     error    // Any error that occurred during rendering
}

// Renderer is an interface for content renderers.
type Renderer interface {
	// Render performs the heavy rendering work and returns lines.
	Render(content string, width int) ([]string, error)
}

// Buffer manages background pre-rendering of content.
// It uses a producer-consumer pattern with a goroutine pool.
type Buffer struct {
	// Configuration
	maxWorkers int
	bufferSize int

	// Channels
	jobs    chan RenderJob
	results chan RenderResult

	// State
	running  atomic.Bool
	wg       sync.WaitGroup
	workerWg sync.WaitGroup

	// Storage for pre-rendered content
	store sync.Map // messageID -> []string

	// Width for rendering
	width   int
	widthMu sync.RWMutex
}

// NewBuffer creates a new pre-render buffer with the specified configuration.
func NewBuffer(maxWorkers, bufferSize int) *Buffer {
	if maxWorkers <= 0 {
		maxWorkers = 2
	}
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &Buffer{
		maxWorkers: maxWorkers,
		bufferSize: bufferSize,
		jobs:       make(chan RenderJob, bufferSize),
		results:    make(chan RenderResult, bufferSize),
	}
}

// Start begins the background rendering workers.
func (b *Buffer) Start() {
	if b.running.Swap(true) {
		return // Already running
	}

	// Start worker goroutines
	for i := 0; i < b.maxWorkers; i++ {
		b.workerWg.Add(1)
		go b.worker(i)
	}

	// Start result collector
	b.wg.Add(1)
	go b.resultCollector()
}

// Stop shuts down the buffer and waits for workers to finish.
func (b *Buffer) Stop() {
	if !b.running.Swap(false) {
		return // Already stopped
	}

	close(b.jobs)
	b.workerWg.Wait()

	close(b.results)
	b.wg.Wait()
}

// SetWidth updates the rendering width.
func (b *Buffer) SetWidth(width int) {
	b.widthMu.Lock()
	b.width = width
	b.widthMu.Unlock()
}

// GetWidth returns the current rendering width.
func (b *Buffer) GetWidth() int {
	b.widthMu.RLock()
	defer b.widthMu.RUnlock()
	return b.width
}

// Submit enqueues a render job for background processing.
// Returns false if the buffer is full or not running.
func (b *Buffer) Submit(job RenderJob) bool {
	if !b.running.Load() {
		return false
	}

	select {
	case b.jobs <- job:
		return true
	default:
		// Buffer full, job dropped
		return false
	}
}

// SubmitWait enqueues a render job and waits for it to complete.
func (b *Buffer) SubmitWait(job RenderJob) ([]string, error) {
	if !b.running.Load() {
		return b.renderSync(job)
	}

	// Use a direct channel for synchronous wait
	resultCh := make(chan RenderResult, 1)

	wrappedJob := RenderJob{
		MessageID: job.MessageID,
		Content:   job.Content,
		Renderer:  &syncRenderer{job.Renderer, resultCh},
	}

	select {
	case b.jobs <- wrappedJob:
		result := <-resultCh
		return result.Lines, result.Error
	default:
		// Buffer full, render synchronously
		return b.renderSync(job)
	}
}

// Get retrieves pre-rendered lines for a message.
// Returns nil if not found or not yet rendered.
func (b *Buffer) Get(messageID string) []string {
	if v, ok := b.store.Load(messageID); ok {
		return v.([]string)
	}
	return nil
}

// Has returns true if pre-rendered content exists for the message.
func (b *Buffer) Has(messageID string) bool {
	_, ok := b.store.Load(messageID)
	return ok
}

// Delete removes pre-rendered content for a message.
func (b *Buffer) Delete(messageID string) {
	b.store.Delete(messageID)
}

// Clear removes all pre-rendered content.
func (b *Buffer) Clear() {
	b.store.Range(func(key, value any) bool {
		b.store.Delete(key)
		return true
	})
}

// worker is a background goroutine that processes render jobs.
func (b *Buffer) worker(id int) {
	defer b.workerWg.Done()

	for job := range b.jobs {
		width := b.GetWidth()
		lines, err := job.Renderer.Render(job.Content, width)

		result := RenderResult{
			MessageID: job.MessageID,
			Lines:     lines,
			Error:     err,
		}

		select {
		case b.results <- result:
		default:
			// Results channel full, store directly
			if err == nil {
				b.store.Store(job.MessageID, lines)
			}
		}
	}
}

// resultCollector processes render results and stores them.
func (b *Buffer) resultCollector() {
	defer b.wg.Done()

	for result := range b.results {
		if result.Error == nil {
			b.store.Store(result.MessageID, result.Lines)
		}
	}
}

// renderSync performs synchronous rendering as fallback.
func (b *Buffer) renderSync(job RenderJob) ([]string, error) {
	width := b.GetWidth()
	lines, err := job.Renderer.Render(job.Content, width)
	if err == nil {
		b.store.Store(job.MessageID, lines)
	}
	return lines, err
}

// syncRenderer wraps a renderer to send results to a channel.
type syncRenderer struct {
	Renderer Renderer
	resultCh chan<- RenderResult
}

func (s *syncRenderer) Render(content string, width int) ([]string, error) {
	lines, err := s.Renderer.Render(content, width)
	s.resultCh <- RenderResult{
		MessageID: "",
		Lines:     lines,
		Error:     err,
	}
	return lines, err
}
