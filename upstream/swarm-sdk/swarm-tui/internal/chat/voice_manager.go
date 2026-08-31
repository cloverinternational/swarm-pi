package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/voice"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// VoiceState represents the current state of voice input
type VoiceState int

const (
	VoiceStateIdle VoiceState = iota
	VoiceStateConnecting
	VoiceStateRecording
	VoiceStateTranscribing
	VoiceStateError
)

// String returns a human-readable state
func (s VoiceState) String() string {
	switch s {
	case VoiceStateIdle:
		return i18n.T("chat_b.voice.state_idle")
	case VoiceStateConnecting:
		return i18n.T("chat_b.voice.state_connecting")
	case VoiceStateRecording:
		return i18n.T("chat_b.voice.state_recording")
	case VoiceStateTranscribing:
		return i18n.T("chat_b.voice.state_transcribing")
	case VoiceStateError:
		return i18n.T("chat_b.voice.state_error")
	default:
		return i18n.T("chat_b.voice.state_unknown")
	}
}

// VoiceManager manages the voice input lifecycle for the TUI
type VoiceManager struct {
	client          *voice.Client
	sdkConfig       *voice.Config // stored for client recreation after error
	providerFactory *voice.ProviderFactory
	selectedDevice  string
	state           VoiceState
	mu              sync.RWMutex

	// Current transcript state
	interimText string
	finalText   string

	// Error state
	lastError error

	// Context for operations
	ctx    context.Context
	cancel context.CancelFunc

	// Event callback - sends tea.Msg
	onEvent func(any)
}

// VoiceConfig holds configuration for the voice manager
type VoiceConfig struct {
	BaseURL   string
	AuthToken string
	UserAgent string
	AppID     string

	// Provider configuration
	Provider       voice.ProviderType
	ProviderAPIKey string
	ProviderURL    string // Custom URL for REST/local providers
	ProviderModel  string
	Language       string
	SelectedDevice string

	// API keys for all providers (optional, can use env vars)
	APIKeys map[voice.ProviderType]string
}

// NewVoiceManager creates a new voice manager
func NewVoiceManager(cfg *VoiceConfig) (*VoiceManager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%s", i18n.T("chat_b.voice.config_required"))
	}

	// Create provider factory with configuration
	factoryCfg := &voice.ProviderFactoryConfig{
		DefaultProvider: cfg.Provider,
		APIKeys:         make(map[string]string),
		BaseURLs:        make(map[string]string),
	}

	// Add provider-specific API key
	if cfg.ProviderAPIKey != "" {
		factoryCfg.APIKeys[string(cfg.Provider)] = cfg.ProviderAPIKey
	}

	// Add provider-specific URL
	if cfg.ProviderURL != "" {
		factoryCfg.BaseURLs[string(cfg.Provider)] = cfg.ProviderURL
	}

	// Add all API keys from config
	if cfg.APIKeys != nil {
		for providerType, key := range cfg.APIKeys {
			factoryCfg.APIKeys[string(providerType)] = key
		}
	}

	providerFactory := voice.NewProviderFactoryWithConfig(factoryCfg)

	// If no provider specified, use default from factory
	if cfg.Provider == "" {
		cfg.Provider = providerFactory.DefaultProvider
	}

	// Create SDK config. REST inference may include model warm-up, so it needs a
	// longer finalization window than the streaming WebSocket path.
	language := cfg.Language
	if language == "" {
		language = voice.DefaultLanguage
	}
	safetyTimeout := 5 * time.Second
	if cfg.Provider != voice.ProviderWebSocket {
		safetyTimeout = 60 * time.Second
	}
	sdkCfg := &voice.Config{
		BaseURL:        cfg.BaseURL,
		AuthToken:      cfg.AuthToken,
		UserAgent:      cfg.UserAgent,
		AppID:          cfg.AppID,
		SampleRate:     16000,
		Channels:       1,
		Encoding:       voice.Linear16,
		EndpointingMs:  300,
		UtteranceMs:    1000,
		Language:       language,
		Model:          cfg.ProviderModel,
		ConnectTimeout: 10 * time.Second,
		SafetyTimeout:  safetyTimeout,
		NoDataTimeout:  30 * time.Second, // Applied per-read; normal silence can exceed 1.5s.
		KeepAliveInt:   8 * time.Second,
		Provider:       string(cfg.Provider), // Use configured provider
	}

	client, err := voice.NewClient(sdkCfg)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("chat_b.voice.create_client_failed"), err)
	}

	// Set the provider factory and selected capture device on the client.
	client.SetProviderFactory(providerFactory)
	if cfg.SelectedDevice != "" {
		if err := client.SetCaptureDevice(cfg.SelectedDevice); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf(i18n.T("chat_b.voice.select_device_failed"), err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &VoiceManager{
		client:          client,
		sdkConfig:       sdkCfg,
		providerFactory: providerFactory,
		selectedDevice:  cfg.SelectedDevice,
		state:           VoiceStateIdle,
		ctx:             ctx,
		cancel:          cancel,
	}, nil
}

// SetProviderFactory sets a custom provider factory
func (vm *VoiceManager) SetProviderFactory(factory *voice.ProviderFactory) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.providerFactory = factory
	if vm.client != nil {
		vm.client.SetProviderFactory(factory)
	}
}

// UpdateProvider updates the provider configuration while preserving the legacy API.
func (vm *VoiceManager) UpdateProvider(provider voice.ProviderType, apiKey, customURL string) error {
	return vm.UpdateProviderConfig(provider, apiKey, customURL, "", "")
}

// UpdateProviderConfig applies the complete provider selection and recreates the
// underlying client so the next recording uses it immediately.
func (vm *VoiceManager) UpdateProviderConfig(provider voice.ProviderType, apiKey, customURL, model, language string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.state == VoiceStateRecording || vm.state == VoiceStateTranscribing {
		return fmt.Errorf("%s", i18n.T("chat_b.voice.cannot_change_provider", vm.state))
	}
	if vm.providerFactory != nil {
		if apiKey == "" {
			delete(vm.providerFactory.APIKeys, provider)
		} else {
			vm.providerFactory.APIKeys[provider] = apiKey
		}
		if customURL == "" {
			delete(vm.providerFactory.BaseURLs, provider)
		} else {
			vm.providerFactory.BaseURLs[provider] = customURL
		}
		vm.providerFactory.DefaultProvider = provider
	}

	vm.sdkConfig.Provider = string(provider)
	vm.sdkConfig.BaseURL = customURL
	vm.sdkConfig.AuthToken = apiKey
	vm.sdkConfig.Model = model
	if language != "" {
		vm.sdkConfig.Language = language
	}
	if provider == voice.ProviderWebSocket {
		vm.sdkConfig.SafetyTimeout = 5 * time.Second
	} else {
		vm.sdkConfig.SafetyTimeout = 60 * time.Second
	}

	if vm.client != nil {
		_ = vm.client.Close()
	}
	client, err := voice.NewClient(vm.sdkConfig)
	if err != nil {
		return fmt.Errorf(i18n.T("chat_b.voice.recreate_client_failed"), err)
	}
	client.SetProviderFactory(vm.providerFactory)
	if vm.selectedDevice != "" {
		if err := client.SetCaptureDevice(vm.selectedDevice); err != nil {
			_ = client.Close()
			return fmt.Errorf(i18n.T("chat_b.voice.select_device_failed"), err)
		}
	}
	vm.client = client
	vm.state = VoiceStateIdle
	vm.lastError = nil
	return nil
}

// SetEventCallback sets the callback for voice events
// The callback will be called with tea.Msg compatible events
func (vm *VoiceManager) SetEventCallback(callback func(any)) {
	vm.mu.Lock()
	vm.onEvent = callback
	vm.mu.Unlock()
}

// StartRecording begins voice recording
func (vm *VoiceManager) StartRecording() error {
	vm.mu.Lock()

	if vm.state != VoiceStateIdle {
		vm.mu.Unlock()
		return fmt.Errorf("%s", i18n.T("chat_b.voice.cannot_start_state", vm.state))
	}

	// Clear previous state
	vm.interimText = ""
	vm.finalText = ""
	vm.lastError = nil

	// Transition to connecting
	vm.state = VoiceStateConnecting
	vm.mu.Unlock()
	vm.emitEvent(VoiceStateChangeMsg{State: VoiceStateConnecting})

	// Start recording
	if err := vm.client.StartRecording(vm.ctx); err != nil {
		vm.mu.Lock()
		vm.state = VoiceStateError
		vm.lastError = err
		vm.mu.Unlock()
		vm.emitEvent(VoiceErrorMsg{Error: err})
		return err
	}

	// Transition to recording
	vm.mu.Lock()
	vm.state = VoiceStateRecording
	vm.mu.Unlock()
	vm.emitEvent(VoiceStateChangeMsg{State: VoiceStateRecording})

	// Start processing events
	go vm.processEvents()

	return nil
}

// StopRecording stops voice recording
func (vm *VoiceManager) StopRecording() error {
	vm.mu.Lock()
	if vm.state != VoiceStateRecording {
		vm.mu.Unlock()
		return fmt.Errorf("%s", i18n.T("chat_b.voice.not_recording"))
	}

	// Transition to transcribing
	vm.state = VoiceStateTranscribing
	vm.mu.Unlock()
	vm.emitEvent(VoiceStateChangeMsg{State: VoiceStateTranscribing})

	// Stop recording and drain the capture channel to avoid goroutine leaks
	if err := vm.stopCapture(); err != nil {
		vm.mu.Lock()
		vm.state = VoiceStateError
		vm.lastError = err
		vm.mu.Unlock()
		vm.emitEvent(VoiceErrorMsg{Error: err})
		return err
	}

	return nil
}

// stopCapture stops the underlying client recording. The SDK client emits every
// buffered final transcript before EventRecordingStopped, and processEvents is
// the single consumer of that channel, so no draining may happen here: a second
// reader would race processEvents and reorder final/complete delivery.
func (vm *VoiceManager) stopCapture() error {
	return vm.client.StopRecording(vm.ctx)
}

// ToggleRecording toggles recording state
func (vm *VoiceManager) ToggleRecording() error {
	vm.mu.RLock()
	state := vm.state
	vm.mu.RUnlock()

	if state == VoiceStateRecording {
		return vm.StopRecording()
	}

	// If we're transcribing (waiting for server), ignore the toggle — the result
	// will arrive shortly and the state will return to Idle automatically.
	if state == VoiceStateTranscribing {
		return fmt.Errorf("%s", i18n.T("chat_b.voice.wait_transcription"))
	}

	// For Idle or Error states, start a fresh recording.
	// On Error we recreate the underlying client since it may be in a broken state.
	if state == VoiceStateError {
		vm.mu.Lock()
		// Close old client (best-effort)
		if vm.client != nil {
			_ = vm.client.Close()
		}
		newClient, err := voice.NewClient(vm.sdkConfig)
		if err != nil {
			vm.mu.Unlock()
			return fmt.Errorf(i18n.T("chat_b.voice.recreate_client_failed"), err)
		}
		newClient.SetProviderFactory(vm.providerFactory)
		if vm.selectedDevice != "" {
			if err := newClient.SetCaptureDevice(vm.selectedDevice); err != nil {
				_ = newClient.Close()
				vm.mu.Unlock()
				return fmt.Errorf(i18n.T("chat_b.voice.select_device_failed"), err)
			}
		}
		vm.client = newClient
		vm.state = VoiceStateIdle
		vm.lastError = nil
		vm.interimText = ""
		vm.finalText = ""
		vm.mu.Unlock()
	}

	return vm.StartRecording()
}

// processEvents processes voice client events
func (vm *VoiceManager) processEvents() {
	events := vm.client.Events()

	for {
		select {
		case <-vm.ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			vm.handleEvent(event)
		}
	}
}

// appendFinalText joins successive final transcript chunks with a single
// space so chunked REST results do not concatenate into "localmodels".
func appendFinalText(existing, addition string) string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing
	}
	if existing == "" {
		return addition
	}
	return strings.TrimRight(existing, " ") + " " + addition
}

// handleEvent handles a single voice event
func (vm *VoiceManager) handleEvent(event voice.VoiceEvent) {
	switch e := event.(type) {
	case voice.EventInterim:
		vm.mu.Lock()
		vm.interimText = e.Text
		vm.mu.Unlock()
		vm.emitEvent(VoiceInterimMsg{Text: e.Text})

	case voice.EventFinal:
		vm.mu.Lock()
		vm.finalText = appendFinalText(vm.finalText, e.Text)
		vm.interimText = ""
		vm.mu.Unlock()
		vm.emitEvent(VoiceFinalMsg{Text: e.Text})

	case voice.EventTranscript:
		if e.Transcript.IsFinal {
			vm.mu.Lock()
			vm.finalText = appendFinalText(vm.finalText, e.Transcript.Text)
			vm.interimText = ""
			vm.mu.Unlock()
		} else {
			vm.mu.Lock()
			vm.interimText = e.Transcript.Text
			vm.mu.Unlock()
		}
		vm.emitEvent(VoiceTranscriptMsg{
			Text:    e.Transcript.Text,
			IsFinal: e.Transcript.IsFinal,
		})

	case voice.EventRecordingStopped:
		vm.mu.Lock()
		vm.state = VoiceStateIdle
		finalText := vm.finalText
		interimText := vm.interimText
		vm.interimText = ""
		vm.finalText = ""
		vm.mu.Unlock()
		// Include interim as fallback — the Anthropic API sends transcripts without
		// is_final, so they may arrive as interim even though they are complete.
		text := finalText
		if text == "" {
			text = interimText
		}
		vm.emitEvent(VoiceStateChangeMsg{State: VoiceStateIdle})
		vm.emitEvent(VoiceCompleteMsg{Text: text})

	case voice.EventError:
		vm.mu.Lock()
		wasTranscribing := vm.state == VoiceStateTranscribing
		finalText := vm.finalText
		interimText := vm.interimText
		vm.interimText = ""
		vm.finalText = ""
		if wasTranscribing {
			// Error during transcribing phase is typically the WebSocket closing
			// after the server finishes. Treat it as a successful completion.
			vm.state = VoiceStateIdle
		} else {
			vm.state = VoiceStateError
			vm.lastError = e.Error
		}
		vm.mu.Unlock()
		if wasTranscribing {
			text := finalText
			if text == "" {
				text = interimText
			}
			vm.emitEvent(VoiceStateChangeMsg{State: VoiceStateIdle})
			vm.emitEvent(VoiceCompleteMsg{Text: text})
		} else {
			vm.emitEvent(VoiceErrorMsg{Error: e.Error})
			vm.emitEvent(VoiceStateChangeMsg{State: VoiceStateError})
		}
	}
}

// emitEvent sends an event through the callback
func (vm *VoiceManager) emitEvent(event any) {
	vm.mu.RLock()
	callback := vm.onEvent
	vm.mu.RUnlock()

	if callback != nil {
		callback(event)
	}
}

// GetState returns the current voice state
func (vm *VoiceManager) GetState() VoiceState {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.state
}

// GetInterimText returns the current interim transcript
func (vm *VoiceManager) GetInterimText() string {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.interimText
}

// GetFinalText returns the accumulated final transcript
func (vm *VoiceManager) GetFinalText() string {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.finalText
}

// GetLastError returns the last error
func (vm *VoiceManager) GetLastError() error {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.lastError
}

// Close cleans up the voice manager
func (vm *VoiceManager) Close() error {
	vm.cancel()
	if vm.client != nil {
		return vm.client.Close()
	}
	return nil
}

// IsRecording returns true if currently recording
func (vm *VoiceManager) IsRecording() bool {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.state == VoiceStateRecording
}

// IsIdle returns true if idle
func (vm *VoiceManager) IsIdle() bool {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.state == VoiceStateIdle
}

// ==========================================
// Tea Messages for TUI integration
// ==========================================

// VoiceStateChangeMsg is emitted when voice state changes
type VoiceStateChangeMsg struct {
	State VoiceState
}

// VoiceInterimMsg is emitted when an interim transcript is received
type VoiceInterimMsg struct {
	Text string
}

// VoiceFinalMsg is emitted when a final transcript is received
type VoiceFinalMsg struct {
	Text string
}

// VoiceTranscriptMsg is emitted for any transcript
type VoiceTranscriptMsg struct {
	Text    string
	IsFinal bool
}

// VoiceCompleteMsg is emitted when voice input is complete
type VoiceCompleteMsg struct {
	Text string
}

// VoiceErrorMsg is emitted when an error occurs
type VoiceErrorMsg struct {
	Error error
}

// VoiceToggleMsg is emitted when user requests to toggle voice
type VoiceToggleMsg struct{}
