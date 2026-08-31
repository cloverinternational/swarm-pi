package voice

import (
	"context"
	"sync"
	"time"
)

// AudioStreamer manages the audio streaming lifecycle
type AudioStreamer struct {
	capture AudioCapture
	onAudio func(chunk []byte)
	onError func(err error)
	onDone  func()

	// State
	running bool
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewAudioStreamer creates a new audio streamer
func NewAudioStreamer(capture AudioCapture) *AudioStreamer {
	return &AudioStreamer{
		capture: capture,
	}
}

// SetOnAudio sets the audio chunk callback
func (s *AudioStreamer) SetOnAudio(fn func([]byte)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAudio = fn
}

// SetOnError sets the error callback
func (s *AudioStreamer) SetOnError(fn func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onError = fn
}

// SetOnDone sets the completion callback
func (s *AudioStreamer) SetOnDone(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDone = fn
}

// Start begins streaming audio
func (s *AudioStreamer) Start(parentCtx context.Context) (<-chan []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil, ErrAlreadyRecording
	}

	s.ctx, s.cancel = context.WithCancel(parentCtx)
	s.running = true

	audioChan, err := s.capture.Start(s.ctx)
	if err != nil {
		s.running = false
		return nil, err
	}

	// Wrap channel with callbacks
	outputChan := make(chan []byte, 100)

	go func() {
		defer close(outputChan)
		defer func() {
			s.mu.Lock()
			s.running = false
			if s.onDone != nil {
				s.onDone()
			}
			s.mu.Unlock()
		}()

		for {
			select {
			case chunk, ok := <-audioChan:
				if !ok {
					return
				}

				s.mu.RLock()
				onAudio := s.onAudio
				s.mu.RUnlock()

				if onAudio != nil {
					onAudio(chunk)
				}

				select {
				case outputChan <- chunk:
				case <-s.ctx.Done():
					return
				}

			case <-s.ctx.Done():
				return
			}
		}
	}()

	return outputChan, nil
}

// Stop stops streaming audio
func (s *AudioStreamer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if s.cancel != nil {
		s.cancel()
	}

	if err := s.capture.Stop(); err != nil {
		return err
	}

	s.running = false
	return nil
}

// IsRunning returns whether streaming is active
func (s *AudioStreamer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// StreamManager coordinates audio streaming with WebSocket transmission
type StreamManager struct {
	config   *Config
	streamer *AudioStreamer
	transmit func([]byte) error
	stats    *StreamStats

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// StreamStats holds streaming statistics
type StreamStats struct {
	StartTime     time.Time
	BytesSent     int64
	ChunksSent    int64
	LastChunkTime time.Time
	AvgChunkSize  float64
	Errors        int64
}

// NewStreamManager creates a new stream manager
func NewStreamManager(cfg *Config, capture AudioCapture, transmit func([]byte) error) *StreamManager {
	streamer := NewAudioStreamer(capture)
	return &StreamManager{
		config:   cfg,
		streamer: streamer,
		transmit: transmit,
		stats:    &StreamStats{},
	}
}

// Start begins audio streaming and transmission
func (m *StreamManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ctx, m.cancel = context.WithCancel(ctx)
	m.stats.StartTime = time.Now()

	audioChan, err := m.streamer.Start(m.ctx)
	if err != nil {
		return err
	}

	m.wg.Add(1)
	go m.transmitLoop(audioChan)

	return nil
}

// Stop stops streaming
func (m *StreamManager) Stop() error {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()

	m.wg.Wait()
	return m.streamer.Stop()
}

// transmitLoop handles audio transmission
func (m *StreamManager) transmitLoop(audioChan <-chan []byte) {
	defer m.wg.Done()

	for {
		select {
		case chunk, ok := <-audioChan:
			if !ok {
				return
			}

			if err := m.transmit(chunk); err != nil {
				m.mu.Lock()
				m.stats.Errors++
				m.mu.Unlock()
				continue
			}

			m.mu.Lock()
			m.stats.BytesSent += int64(len(chunk))
			m.stats.ChunksSent++
			m.stats.LastChunkTime = time.Now()
			if m.stats.ChunksSent > 0 {
				m.stats.AvgChunkSize = float64(m.stats.BytesSent) / float64(m.stats.ChunksSent)
			}
			m.mu.Unlock()

		case <-m.ctx.Done():
			return
		}
	}
}

// GetStats returns current streaming statistics
func (m *StreamManager) GetStats() StreamStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.stats
}

// Duration returns the streaming duration
func (s *StreamStats) Duration() time.Duration {
	if s.StartTime.IsZero() {
		return 0
	}
	if s.LastChunkTime.IsZero() {
		return time.Since(s.StartTime)
	}
	return s.LastChunkTime.Sub(s.StartTime)
}

// Bitrate returns the current bitrate in bits per second
func (s *StreamStats) Bitrate() float64 {
	duration := s.Duration().Seconds()
	if duration == 0 {
		return 0
	}
	return float64(s.BytesSent*8) / duration
}
