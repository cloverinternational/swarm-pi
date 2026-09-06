package voice

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type lifecycleCapture struct {
	mu      sync.Mutex
	chunks  chan []byte
	stopped bool
}

func (c *lifecycleCapture) Initialize() error { return nil }
func (c *lifecycleCapture) Start(context.Context) (<-chan []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chunks = make(chan []byte, 2)
	c.stopped = false
	return c.chunks, nil
}
func (c *lifecycleCapture) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stopped {
		close(c.chunks)
		c.stopped = true
	}
	return nil
}
func (c *lifecycleCapture) IsAvailable() bool                          { return true }
func (c *lifecycleCapture) Close() error                               { return c.Stop() }
func (c *lifecycleCapture) Name() string                               { return "lifecycle" }
func (c *lifecycleCapture) DiscoverDevices() ([]DeviceInfo, error)     { return nil, nil }
func (c *lifecycleCapture) SetDevice(string) error                     { return nil }
func (c *lifecycleCapture) GetDevice() string                          { return "" }
func (c *lifecycleCapture) GetAudioLevels() AudioLevels                { return AudioLevels{} }
func (c *lifecycleCapture) StartLevelMonitoring(context.Context) error { return nil }
func (c *lifecycleCapture) StopLevelMonitoring() error                 { return nil }
func (c *lifecycleCapture) IsMonitoring() bool                         { return false }

type lifecycleProvider struct {
	events chan VoiceEvent
	gate   chan struct{}
	once   sync.Once
}

func newLifecycleProvider() *lifecycleProvider {
	return &lifecycleProvider{events: make(chan VoiceEvent, 8), gate: make(chan struct{})}
}
func (p *lifecycleProvider) Initialize(context.Context) error {
	p.events <- EventReady{Provider: ProviderREST}
	return nil
}
func (p *lifecycleProvider) SendAudio(context.Context, []byte) error { return nil }
func (p *lifecycleProvider) Flush(context.Context) error {
	<-p.gate
	p.events <- EventFinal{Text: "swarm voice works"}
	return nil
}
func (p *lifecycleProvider) Close() error {
	p.once.Do(func() { close(p.events) })
	return nil
}
func (p *lifecycleProvider) Events() <-chan VoiceEvent { return p.events }
func (p *lifecycleProvider) Name() string              { return "lifecycle" }
func (p *lifecycleProvider) IsStreaming() bool         { return false }

func TestRESTCompletionWaitsForFinalTranscript(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = string(ProviderREST)
	cfg.SafetyTimeout = time.Second
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	capture := &lifecycleCapture{}
	provider := newLifecycleProvider()
	client.capture = capture
	client.SetProvider(provider)

	if err := client.StartRecording(context.Background()); err != nil {
		t.Fatal(err)
	}
	capture.chunks <- []byte{0, 0, 1, 0}

	stopDone := make(chan error, 1)
	go func() { stopDone <- client.StopRecording(context.Background()) }()

	select {
	case err := <-stopDone:
		t.Fatalf("stop returned before provider final result: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(provider.gate)
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}

	var sawFinal, sawStopped bool
	for len(client.events) > 0 {
		event := <-client.events
		switch event.(type) {
		case EventFinal:
			if transcript := client.GetTranscript(); !strings.Contains(transcript, "swarm voice works") {
				t.Fatalf("final event published before transcript commit: %q", transcript)
			}
			sawFinal = true
		case EventRecordingStopped:
			if !sawFinal {
				t.Fatal("recording stopped was emitted before final transcript")
			}
			sawStopped = true
		}
	}
	if !sawFinal || !sawStopped {
		t.Fatalf("events missing: final=%v stopped=%v", sawFinal, sawStopped)
	}
	if got := client.GetProvider(); got != nil {
		t.Fatalf("closed provider was retained: %T", got)
	}
}

func TestRESTProviderFinalTailSubmittedOnce(t *testing.T) {
	cfg := &ProviderConfig{
		Type:           ProviderREST,
		SampleRate:     16000,
		Channels:       1,
		ChunkDuration:  10,
		RequestTimeout: int64(time.Second),
	}
	provider := NewRESTProvider(cfg)
	var mu sync.Mutex
	calls := 0
	provider.doTranscribeFunc = func(context.Context, []byte) (*TranscriptResult, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return &TranscriptResult{Text: "tail"}, nil
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.SendAudio(context.Background(), []byte{0, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("transcription calls = %d, want 1", calls)
	}
}

func TestRESTProviderPublishesTerminalEventsInChunkOrder(t *testing.T) {
	cfg := &ProviderConfig{
		Type:           ProviderREST,
		SampleRate:     16000,
		Channels:       1,
		ChunkDuration:  10,
		RequestTimeout: int64(time.Second),
	}
	provider := NewRESTProvider(cfg)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondFinished := make(chan struct{})
	firstErr := errors.New("first chunk failed")
	provider.doTranscribeFunc = func(_ context.Context, audio []byte) (*TranscriptResult, error) {
		switch audio[0] {
		case 1:
			close(firstStarted)
			<-releaseFirst
			return nil, firstErr
		case 2:
			close(secondFinished)
			return &TranscriptResult{Text: "second chunk"}, nil
		default:
			return nil, errors.New("unexpected audio chunk")
		}
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := (<-provider.Events()).(EventReady); !ok {
		t.Fatal("provider did not emit ready event first")
	}

	if err := provider.SendAudio(context.Background(), []byte{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-firstStarted
	if err := provider.SendAudio(context.Background(), []byte{2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-secondFinished

	select {
	case event := <-provider.Events():
		t.Fatalf("later chunk published before the first completed: %T", event)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)

	select {
	case event := <-provider.Events():
		got, ok := event.(EventError)
		if !ok || !errors.Is(got.Error, firstErr) {
			t.Fatalf("first terminal event = %#v, want first chunk error", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first terminal event")
	}
	select {
	case event := <-provider.Events():
		got, ok := event.(EventFinal)
		if !ok || got.Text != "second chunk" {
			t.Fatalf("second terminal event = %#v, want second chunk final", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second terminal event")
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRESTProviderCloseWithUndrainedFullEventChannel(t *testing.T) {
	cfg := &ProviderConfig{
		Type:           ProviderREST,
		SampleRate:     16000,
		Channels:       1,
		ChunkDuration:  10,
		RequestTimeout: int64(time.Second),
	}
	provider := NewRESTProvider(cfg)
	requestFinished := make(chan struct{})
	provider.doTranscribeFunc = func(context.Context, []byte) (*TranscriptResult, error) {
		close(requestFinished)
		return &TranscriptResult{Text: "undrained final"}, nil
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	for len(provider.events) < cap(provider.events) {
		provider.events <- EventError{Error: errors.New("fill")}
	}
	if err := provider.SendAudio(context.Background(), []byte{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-requestFinished

	closeDone := make(chan error, 1)
	go func() { closeDone <- provider.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked with a full undrained event channel")
	}
}

type failingLifecycleProvider struct {
	events chan VoiceEvent
	mu     sync.Mutex
	inits  int
	closes int
	once   sync.Once
}

func newFailingLifecycleProvider() *failingLifecycleProvider {
	return &failingLifecycleProvider{events: make(chan VoiceEvent)}
}

func (p *failingLifecycleProvider) Initialize(context.Context) error {
	p.mu.Lock()
	p.inits++
	p.mu.Unlock()
	return errors.New("startup failed")
}
func (p *failingLifecycleProvider) SendAudio(context.Context, []byte) error { return nil }
func (p *failingLifecycleProvider) Flush(context.Context) error             { return nil }
func (p *failingLifecycleProvider) Close() error {
	p.mu.Lock()
	p.closes++
	p.mu.Unlock()
	p.once.Do(func() { close(p.events) })
	return nil
}
func (p *failingLifecycleProvider) Events() <-chan VoiceEvent { return p.events }
func (p *failingLifecycleProvider) Name() string              { return "failing" }
func (p *failingLifecycleProvider) IsStreaming() bool         { return false }

type timeoutLifecycleProvider struct {
	events       chan VoiceEvent
	flushStarted chan struct{}
	canceled     chan struct{}
	flushOnce    sync.Once
	cancelOnce   sync.Once
	closeOnce    sync.Once
}

func newTimeoutLifecycleProvider() *timeoutLifecycleProvider {
	return &timeoutLifecycleProvider{
		events:       make(chan VoiceEvent, 1),
		flushStarted: make(chan struct{}),
		canceled:     make(chan struct{}),
	}
}

func (p *timeoutLifecycleProvider) Initialize(context.Context) error { return nil }
func (p *timeoutLifecycleProvider) SendAudio(context.Context, []byte) error {
	return nil
}
func (p *timeoutLifecycleProvider) Flush(ctx context.Context) error {
	p.flushOnce.Do(func() { close(p.flushStarted) })
	<-ctx.Done()
	p.cancelOnce.Do(func() { close(p.canceled) })
	return ctx.Err()
}
func (p *timeoutLifecycleProvider) Close() error {
	p.closeOnce.Do(func() { close(p.events) })
	return nil
}
func (p *timeoutLifecycleProvider) Events() <-chan VoiceEvent { return p.events }
func (p *timeoutLifecycleProvider) Name() string              { return "timeout" }
func (p *timeoutLifecycleProvider) IsStreaming() bool         { return false }

func newRESTLifecycleClient(t *testing.T, safetyTimeout time.Duration) (*Client, *lifecycleCapture) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Provider = string(ProviderREST)
	cfg.SafetyTimeout = safetyTimeout
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	capture := &lifecycleCapture{}
	client.capture = capture
	return client, capture
}

func TestRESTCloseDuringRecordingDoesNotDeadlock(t *testing.T) {
	client, _ := newRESTLifecycleClient(t, time.Second)
	provider := newLifecycleProvider()
	client.SetProvider(provider)
	if err := client.StartRecordingREST(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	close(provider.gate)

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked during active REST recording")
	}
	if state := client.GetState(); state != StateClosed {
		t.Fatalf("state = %s, want closed", state)
	}
}

func TestRESTConcurrentStopAndCloseLeavesClosed(t *testing.T) {
	for i := 0; i < 25; i++ {
		client, _ := newRESTLifecycleClient(t, time.Second)
		provider := newLifecycleProvider()
		client.SetProvider(provider)
		if err := client.StartRecordingREST(context.Background()); err != nil {
			t.Fatal(err)
		}

		stopDone := make(chan error, 1)
		closeDone := make(chan error, 1)
		go func() { stopDone <- client.StopRecordingREST(context.Background()) }()
		go func() { closeDone <- client.Close() }()
		close(provider.gate)

		select {
		case <-stopDone:
		case <-time.After(time.Second):
			t.Fatal("concurrent StopRecordingREST deadlocked")
		}
		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent Close deadlocked")
		}
		if state := client.GetState(); state != StateClosed {
			t.Fatalf("iteration %d: state = %s, want closed", i, state)
		}
	}
}

func TestRESTStartupFailureDetachesClosedProvider(t *testing.T) {
	client, _ := newRESTLifecycleClient(t, time.Second)
	failed := newFailingLifecycleProvider()
	client.SetProvider(failed)
	if err := client.StartRecordingREST(context.Background()); err == nil {
		t.Fatal("StartRecordingREST succeeded with failing provider")
	}
	if provider := client.GetProvider(); provider != nil {
		t.Fatalf("failed provider retained as %T", provider)
	}
	failed.mu.Lock()
	inits, closes := failed.inits, failed.closes
	failed.mu.Unlock()
	if inits != 1 || closes != 1 {
		t.Fatalf("failed provider lifecycle: initializes=%d closes=%d", inits, closes)
	}

	working := newLifecycleProvider()
	close(working.gate)
	client.SetProvider(working)
	if err := client.StartRecordingREST(context.Background()); err != nil {
		t.Fatalf("fresh provider did not start: %v", err)
	}
	if err := client.StopRecordingREST(context.Background()); err != nil {
		t.Fatalf("fresh provider did not stop: %v", err)
	}
}

func TestRESTSafetyTimeoutCancelsAndReturnsIdle(t *testing.T) {
	client, _ := newRESTLifecycleClient(t, 20*time.Millisecond)
	provider := newTimeoutLifecycleProvider()
	client.SetProvider(provider)
	if err := client.StartRecordingREST(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := client.StopRecordingREST(context.Background())
	if err == nil {
		t.Fatal("StopRecordingREST succeeded despite blocked cleanup")
	}
	select {
	case <-provider.flushStarted:
	default:
		t.Fatal("provider cleanup never started")
	}
	select {
	case <-provider.canceled:
	case <-time.After(time.Second):
		t.Fatal("SafetyTimeout did not cancel provider cleanup")
	}
	if state := client.GetState(); state != StateIdle {
		t.Fatalf("state = %s, want idle", state)
	}
	if client.IsRecording() || client.IsConnected() {
		t.Fatal("client remained recording or connected after timeout")
	}
	if provider := client.GetProvider(); provider != nil {
		t.Fatalf("timed-out provider retained as %T", provider)
	}
}

func TestRESTRepeatedRecordingsReleaseEventConsumers(t *testing.T) {
	client, _ := newRESTLifecycleClient(t, time.Second)
	for i := 0; i < 40; i++ {
		provider := newLifecycleProvider()
		close(provider.gate)
		client.SetProvider(provider)
		if err := client.StartRecordingREST(context.Background()); err != nil {
			t.Fatalf("iteration %d start: %v", i, err)
		}
		session := client.restSession
		if err := client.StopRecordingREST(context.Background()); err != nil {
			t.Fatalf("iteration %d stop: %v", i, err)
		}
		select {
		case <-session.eventsDone:
		default:
			t.Fatalf("iteration %d retained provider event consumer", i)
		}
		if client.restSession != nil {
			t.Fatalf("iteration %d retained REST session", i)
		}
	}
}
