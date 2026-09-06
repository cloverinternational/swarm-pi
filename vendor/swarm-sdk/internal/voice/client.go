package voice

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Client is the main voice input client
type Client struct {
	config  *Config
	capture AudioCapture
	ws      *WSConnection

	// Provider factory for REST-based providers
	providerFactory *ProviderFactory
	provider        Provider

	// Channels
	events chan VoiceEvent

	// State
	state     int32 // atomic State
	connected int32 // atomic bool

	// Context management
	ctx    context.Context
	cancel context.CancelFunc

	// Transcript accumulation
	transcripts *TranscriptBuffer

	// Statistics
	stats   VoiceStats
	statsMu sync.RWMutex

	// Synchronization
	mu          sync.RWMutex
	wg          sync.WaitGroup
	lifecycleMu sync.Mutex
	eventsMu    sync.RWMutex
	eventsClose bool
	restSession *restRecordingSession
}

type restRecordingSession struct {
	ctx        context.Context
	cancel     context.CancelFunc
	provider   Provider
	streamDone chan struct{}
	eventsDone chan struct{}
}

// NewClient creates a new voice client
func NewClient(cfg *Config) (*Client, error) {
	// Apply defaults
	if cfg == nil {
		cfg = DefaultConfig()
	} else {
		cfg = cfg.Clone()
		cfg.ApplyDefaults()
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create audio capture
	capture, err := NewAudioCapture(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create audio capture: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		config:      cfg,
		capture:     capture,
		ws:          NewWSConnection(cfg),
		events:      make(chan VoiceEvent, 100),
		ctx:         ctx,
		cancel:      cancel,
		transcripts: NewTranscriptBuffer(),
		state:       int32(StateIdle),
	}, nil
}

// StartRecording begins capturing and streaming audio
func (c *Client) StartRecording(ctx context.Context) error {
	// Check if using REST-based provider
	if c.isRESTProvider() {
		return c.StartRecordingREST(ctx)
	}

	// WebSocket-based recording (original implementation)
	// Check if already recording
	if !atomic.CompareAndSwapInt32(&c.state, int32(StateIdle), int32(StateConnecting)) {
		currentState := State(atomic.LoadInt32(&c.state))
		if currentState == StateRecording {
			return ErrAlreadyRecording
		}
		return fmt.Errorf("cannot start recording in state: %s", currentState)
	}

	// Initialize audio capture
	if err := c.capture.Initialize(); err != nil {
		atomic.StoreInt32(&c.state, int32(StateIdle))
		return fmt.Errorf("failed to initialize audio capture: %w", err)
	}

	// Connect to WebSocket
	if err := c.ws.Connect(ctx); err != nil {
		atomic.StoreInt32(&c.state, int32(StateIdle))
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Start keepalive
	c.ws.StartKeepAlive()
	atomic.StoreInt32(&c.connected, 1)

	// Transition to recording state
	atomic.StoreInt32(&c.state, int32(StateRecording))

	// Clear previous transcripts
	c.transcripts.Clear()

	// Start audio capture
	audioChan, err := c.capture.Start(ctx)
	if err != nil {
		c.ws.Close()
		atomic.StoreInt32(&c.state, int32(StateIdle))
		atomic.StoreInt32(&c.connected, 0)
		return fmt.Errorf("failed to start audio capture: %w", err)
	}

	// Emit recording started event
	c.emitEvent(EventRecordingStarted{})

	// Start streaming goroutines
	c.wg.Add(2)
	go c.streamAudio(ctx, audioChan)
	go c.receiveTranscripts(ctx)

	return nil
}

// StopRecording stops capture and finalizes transcription
func (c *Client) StopRecording(ctx context.Context) error {
	// Check if using REST-based provider
	if c.isRESTProvider() {
		return c.StopRecordingREST(ctx)
	}

	// WebSocket-based stopping (original implementation)
	// Check if recording
	if !atomic.CompareAndSwapInt32(&c.state, int32(StateRecording), int32(StateFinalizing)) {
		currentState := State(atomic.LoadInt32(&c.state))
		if currentState != StateRecording {
			return ErrNotRecording
		}
	}

	// Stop audio capture
	if err := c.capture.Stop(); err != nil {
		// Log but continue
	}

	// Send CloseStream to get remaining transcripts
	if err := c.ws.CloseStream(); err != nil {
		// Log but continue
	}

	// Wait for final transcripts with timeout
	finalCtx, cancel := context.WithTimeout(ctx, c.config.SafetyTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines finished
	case <-finalCtx.Done():
		// Timeout waiting for finalization
	}

	// Close WebSocket
	c.ws.Close()
	atomic.StoreInt32(&c.connected, 0)
	atomic.StoreInt32(&c.state, int32(StateIdle))

	// Emit recording stopped event
	c.emitEvent(EventRecordingStopped{})

	return nil
}

// IsRecording returns current recording state
func (c *Client) IsRecording() bool {
	return State(atomic.LoadInt32(&c.state)) == StateRecording
}

// IsConnected returns whether connected to the server
func (c *Client) IsConnected() bool {
	return atomic.LoadInt32(&c.connected) == 1
}

// GetState returns the current client state
func (c *Client) GetState() State {
	return State(atomic.LoadInt32(&c.state))
}

// Events returns the event channel for transcript updates
func (c *Client) Events() <-chan VoiceEvent {
	return c.events
}

// GetTranscript returns the accumulated transcript text
func (c *Client) GetTranscript() string {
	return c.transcripts.GetFullText()
}

// GetStats returns voice session statistics
func (c *Client) GetStats() VoiceStats {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	return c.stats
}

// Close shuts down the client
func (c *Client) Close() error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	// Guard against double-close.
	if State(atomic.LoadInt32(&c.state)) == StateClosed {
		return nil
	}

	// Stop an active REST session without re-entering lifecycleMu. A concurrent
	// StopRecordingREST is serialized by the same mutex.
	if c.isRESTProvider() && c.restSession != nil {
		_ = c.stopRecordingRESTLocked(context.Background())
	} else if c.IsRecording() {
		_ = c.StopRecording(context.Background())
	}

	// Cancel context first to signal non-REST goroutines to exit.
	c.cancel()

	if c.ws != nil {
		_ = c.ws.Close()
	}
	c.wg.Wait()

	if c.capture != nil {
		_ = c.capture.Close()
	}

	atomic.StoreInt32(&c.state, int32(StateClosed))

	// Synchronize channel closure with emitEvent so a timed-out REST worker can
	// finish later without sending on a closed channel.
	c.eventsMu.Lock()
	if !c.eventsClose {
		c.eventsClose = true
		close(c.events)
	}
	c.eventsMu.Unlock()
	return nil
}

// SetCaptureDevice selects the input device used by subsequent recordings.
func (c *Client) SetCaptureDevice(deviceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capture == nil {
		return ErrNoAudioCapture
	}
	state := State(atomic.LoadInt32(&c.state))
	if state == StateRecording || state == StateFinalizing {
		return fmt.Errorf("cannot change audio device while %s", state)
	}
	return c.capture.SetDevice(deviceID)
}

// SetProviderFactory sets the provider factory for REST-based transcription
func (c *Client) SetProviderFactory(factory *ProviderFactory) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.providerFactory = factory
}

// SetProvider sets a specific provider for transcription
func (c *Client) SetProvider(provider Provider) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = provider
}

// GetProvider returns the current provider
func (c *Client) GetProvider() Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.provider
}

// isRESTProvider returns true if the configured provider uses REST (not WebSocket)
func (c *Client) isRESTProvider() bool {
	providerType := ProviderType(c.config.Provider)
	switch providerType {
	case ProviderGroq, ProviderOpenAI, ProviderOpenRouter, ProviderREST, ProviderOpenAICompatible:
		return true
	case ProviderWebSocket, ProviderDeepgram, ProviderAssemblyAI:
		return false
	default:
		return false
	}
}

// getOrCreateProvider returns the current provider, creating one from the factory if needed
func (c *Client) getOrCreateProvider() (Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.provider != nil {
		return c.provider, nil
	}

	if c.providerFactory == nil {
		c.providerFactory = NewProviderFactory()
	}

	providerType := ProviderType(c.config.Provider)
	if providerType == "" {
		providerType = c.providerFactory.DefaultProvider
	}

	provider, err := c.providerFactory.Create(providerType,
		WithModel(c.config.Model),
		WithLanguage(c.config.Language),
		WithSampleRate(c.config.SampleRate),
		WithChunkDuration(c.config.ChunkDuration),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}

	c.provider = provider
	return provider, nil
}

// streamAudio reads audio chunks and sends them over WebSocket
func (c *Client) streamAudio(ctx context.Context, audioChan <-chan []byte) {
	defer c.wg.Done()

	var chunkNum int64
	for {
		select {
		case chunk, ok := <-audioChan:
			if !ok {
				return
			}

			// Send audio chunk
			if err := c.ws.SendAudio(chunk); err != nil {
				c.emitEvent(EventError{Error: err})
				return
			}

			// Update stats
			c.statsMu.Lock()
			chunkNum++
			c.stats.TotalChunks = int(chunkNum)
			c.stats.TotalAudioBytes += int64(len(chunk))
			c.statsMu.Unlock()

		case <-ctx.Done():
			return
		case <-c.ctx.Done():
			return
		}
	}
}

// receiveTranscripts receives transcript messages from the server
func (c *Client) receiveTranscripts(ctx context.Context) {
	defer c.wg.Done()

	for {
		// Set read timeout
		c.ws.SetReadTimeout(c.config.NoDataTimeout)

		msg, err := c.ws.ReadMessage()
		if err != nil {
			// Check if context was cancelled (expected shutdown)
			select {
			case <-ctx.Done():
				return
			case <-c.ctx.Done():
				return
			default:
				c.emitEvent(EventError{Error: err})
				return
			}
		}
		// nil msg means an unrecognized but non-fatal frame; skip and continue.
		if msg == nil {
			continue
		}

		switch m := msg.(type) {
		case TranscriptTextMsg:
			// Use GetText() to resolve data/text field differences across providers.
			text := m.GetText()

			// The Anthropic API does not send is_final — every TranscriptText is a
			// complete utterance. Treat it as final so it accumulates in finalText
			// and surfaces through EventRecordingStopped → VoiceCompleteMsg.
			isFinal := m.IsFinal || text != ""

			transcript := Transcript{
				Text:     text,
				IsFinal:  isFinal,
				Channel:  m.Channel,
				Start:    m.Start,
				Duration: m.Duration,
			}

			if isFinal {
				c.transcripts.AppendFinal(text)
				c.emitEvent(EventFinal{Text: text})
			} else {
				c.transcripts.SetInterim(text)
				c.emitEvent(EventInterim{Text: text})
			}

			c.emitEvent(EventTranscript{Transcript: transcript})

			// Update stats
			c.statsMu.Lock()
			c.stats.TotalTranscripts++
			c.statsMu.Unlock()

		case TranscriptEndpointMsg:
			// Utterance endpoint reached - nothing specific to do

		case TranscriptErrorMsg:
			c.emitEvent(EventError{
				Error: fmt.Errorf("transcription error [%s]: %s", m.Code, m.Message),
			})

		case ServerErrorMsg:
			c.emitEvent(EventError{
				Error: fmt.Errorf("server error [%s]: %s", m.Code, m.Message),
			})

		case KeepAliveMsg:
			// Server echoed KeepAlive; nothing to do.

		case CloseStreamMsg:
			// Server is closing the stream; stop the receive loop gracefully.
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-c.ctx.Done():
			return
		default:
		}
	}
}

// emitEvent sends an event to the event channel (non-blocking)
func (c *Client) emitEvent(event VoiceEvent) {
	c.eventsMu.RLock()
	defer c.eventsMu.RUnlock()

	if c.eventsClose || State(atomic.LoadInt32(&c.state)) == StateClosed {
		return
	}

	select {
	case c.events <- event:
	default:
		// Channel full, drop event
	}
}

// Connect connects to the server without starting recording
func (c *Client) Connect(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&c.state, int32(StateIdle), int32(StateConnecting)) {
		return fmt.Errorf("cannot connect in state: %s", c.GetState())
	}

	if err := c.ws.Connect(ctx); err != nil {
		atomic.StoreInt32(&c.state, int32(StateIdle))
		return err
	}

	c.ws.StartKeepAlive()
	atomic.StoreInt32(&c.connected, 1)
	atomic.StoreInt32(&c.state, int32(StateConnected))

	c.emitEvent(EventConnected{})
	return nil
}

// Disconnect disconnects from the server
func (c *Client) Disconnect() error {
	if atomic.LoadInt32(&c.connected) == 0 {
		return nil
	}

	c.ws.Close()
	atomic.StoreInt32(&c.connected, 0)
	atomic.StoreInt32(&c.state, int32(StateIdle))

	c.emitEvent(EventDisconnected{})
	return nil
}

// StartRecordingREST begins capturing audio and transcribing via REST API
// This is used for providers like Groq, OpenAI, and custom REST servers
func (c *Client) StartRecordingREST(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	if !atomic.CompareAndSwapInt32(&c.state, int32(StateIdle), int32(StateConnecting)) {
		currentState := State(atomic.LoadInt32(&c.state))
		if currentState == StateRecording {
			return ErrAlreadyRecording
		}
		return fmt.Errorf("cannot start recording in state: %s", currentState)
	}

	provider, err := c.getOrCreateProvider()
	if err != nil {
		atomic.StoreInt32(&c.state, int32(StateIdle))
		return fmt.Errorf("failed to get provider: %w", err)
	}

	// Every failure after provider acquisition closes and detaches that exact
	// instance. Closed REST providers have closed event channels and are never
	// safe to reuse.
	if err := provider.Initialize(ctx); err != nil {
		_ = provider.Close()
		c.detachProvider(provider)
		atomic.StoreInt32(&c.state, int32(StateIdle))
		return fmt.Errorf("failed to initialize provider: %w", err)
	}
	if err := c.capture.Initialize(); err != nil {
		_ = provider.Close()
		c.detachProvider(provider)
		atomic.StoreInt32(&c.state, int32(StateIdle))
		return fmt.Errorf("failed to initialize audio capture: %w", err)
	}

	audioChan, err := c.capture.Start(ctx)
	if err != nil {
		_ = provider.Close()
		c.detachProvider(provider)
		atomic.StoreInt32(&c.state, int32(StateIdle))
		atomic.StoreInt32(&c.connected, 0)
		return fmt.Errorf("failed to start audio capture: %w", err)
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	session := &restRecordingSession{
		ctx:        sessionCtx,
		cancel:     sessionCancel,
		provider:   provider,
		streamDone: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	c.restSession = session
	c.transcripts.Clear()
	atomic.StoreInt32(&c.state, int32(StateRecording))
	atomic.StoreInt32(&c.connected, 1)

	go c.handleProviderEvents(session)
	go c.streamAudioREST(session, audioChan)

	// Publish the started event only after the complete session is visible.
	c.emitEvent(EventRecordingStarted{})
	return nil
}

// streamAudioREST reads audio chunks and transcribes them via REST API.
func (c *Client) streamAudioREST(session *restRecordingSession, audioChan <-chan []byte) {
	defer close(session.streamDone)
	defer func() { _ = session.provider.Close() }()

	var totalBytes int64
	chunkDuration := c.config.ChunkDuration
	if chunkDuration <= 0 {
		chunkDuration = 5.0
	}
	bytesPerChunk := int(float64(c.config.SampleRate*2*c.config.Channels) * chunkDuration)

	for {
		select {
		case chunk, ok := <-audioChan:
			if !ok {
				_ = session.provider.Flush(session.ctx)
				return
			}
			if err := session.provider.SendAudio(session.ctx, chunk); err != nil {
				c.emitEvent(EventError{Error: fmt.Errorf("failed to send audio: %w", err)})
				continue
			}

			totalBytes += int64(len(chunk))
			c.statsMu.Lock()
			c.stats.TotalChunks++
			c.stats.TotalAudioBytes += int64(len(chunk))
			c.statsMu.Unlock()

			if totalBytes >= int64(bytesPerChunk) {
				if err := session.provider.Flush(session.ctx); err != nil {
					c.emitEvent(EventError{Error: fmt.Errorf("failed to flush: %w", err)})
				}
				totalBytes = 0
			}

		case <-session.ctx.Done():
			return
		}
	}
}

// handleProviderEvents reads events from one recording's provider channel.
func (c *Client) handleProviderEvents(session *restRecordingSession) {
	defer close(session.eventsDone)

	for {
		// Give cancellation priority so timeout cleanup cannot leave a permanent
		// consumer attached to an abandoned provider.
		select {
		case <-session.ctx.Done():
			return
		default:
		}

		select {
		case event, ok := <-session.provider.Events():
			if !ok {
				return
			}

			// Commit transcript state before publishing the matching event so an
			// event consumer always observes the updated transcript.
			switch e := event.(type) {
			case EventFinal:
				c.transcripts.AppendFinal(e.Text)
				c.statsMu.Lock()
				c.stats.TotalTranscripts++
				c.statsMu.Unlock()
			case EventInterim:
				c.transcripts.SetInterim(e.Text)
			}
			c.emitEvent(event)

		case <-session.ctx.Done():
			return
		}
	}
}

// StopRecordingREST stops REST-based recording.
func (c *Client) StopRecordingREST(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.stopRecordingRESTLocked(ctx)
}

func (c *Client) stopRecordingRESTLocked(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&c.state, int32(StateRecording), int32(StateFinalizing)) {
		return ErrNotRecording
	}

	_ = c.capture.Stop()

	session := c.restSession
	if session == nil {
		c.finishRESTSession(nil)
		return ErrNotRecording
	}

	finalCtx, cancel := context.WithTimeout(ctx, c.config.SafetyTimeout)
	defer cancel()

	var finalErr error
	select {
	case <-session.streamDone:
	case <-finalCtx.Done():
		finalErr = fmt.Errorf("timed out finalizing transcription: %w", finalCtx.Err())
	}

	if finalErr == nil {
		select {
		case <-session.eventsDone:
		case <-finalCtx.Done():
			finalErr = fmt.Errorf("timed out forwarding final transcription: %w", finalCtx.Err())
		}
	}

	if finalErr != nil {
		session.cancel()
		// The consumer is cancellation-aware; wait for it before publishing the
		// stopped event so no final transcript can be forwarded afterward.
		<-session.eventsDone
	}

	c.finishRESTSession(session)
	c.emitEvent(EventRecordingStopped{})
	return finalErr
}

func (c *Client) finishRESTSession(session *restRecordingSession) {
	if session != nil {
		session.cancel()
		c.detachProvider(session.provider)
		if c.restSession == session {
			c.restSession = nil
		}
	}
	atomic.StoreInt32(&c.connected, 0)
	atomic.CompareAndSwapInt32(&c.state, int32(StateFinalizing), int32(StateIdle))
}

func (c *Client) detachProvider(provider Provider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider == provider {
		c.provider = nil
	}
}
