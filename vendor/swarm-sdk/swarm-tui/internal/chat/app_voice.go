package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/voice"
	voiceruntime "github.com/Swarm-Code/mono/swarm-sdk/internal/voice/runtime"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"

	tea "charm.land/bubbletea/v2"
)

// listenForVoiceEvent returns a tea.Cmd that blocks on the voice event channel
// and delivers the next event directly into the bubbletea Update() loop.
// Each voice event handler must return this cmd to keep the chain alive.
func listenForVoiceEvent(ch chan any) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil // channel closed; stop listening
		}
		return event
	}
}

// handleVoiceToggle handles the /voice command
func (a *App) handleVoiceToggle() (tea.Model, tea.Cmd) {
	if a.voiceManager == nil {
		a.addNotification("error", i18n.T("classic_chat.voice.unavailable"))
		return a, nil
	}

	// Run ToggleRecording in a goroutine so the TUI event loop is not blocked
	// (StartRecording dials a WebSocket which can take up to ConnectTimeout).
	// All state-change events are emitted through voiceEventCh and delivered
	// to Update() via the listenForVoiceEvent cmd chain started in Init().
	go func() {
		if err := a.voiceManager.ToggleRecording(); err != nil {
			// "please wait" is a soft rejection — don't surface as error notification.
		} else {
		}
	}()

	return a, nil
}

// handleVoiceStateChange handles voice state change messages
func (a *App) handleVoiceStateChange(msg VoiceStateChangeMsg) (tea.Model, tea.Cmd) {
	switch msg.State {
	case VoiceStateConnecting:
		a.voiceRecording = false
		a.voiceTranscribing = false
		a.addNotification("info", i18n.T("chat_b.voice.connecting"))
	case VoiceStateRecording:
		a.voiceRecording = true
		a.voiceTranscribing = false
		a.addNotification("info", i18n.T("classic_chat.voice.recording_stop"))
		// Start the pulse tick loop if not already running
		if !a.voicePulseActive {
			a.voicePulseActive = true
			return a, tea.Batch(listenForVoiceEvent(a.voiceEventCh), voiceTickCmd())
		}
	case VoiceStateTranscribing:
		a.voiceRecording = false
		a.voiceTranscribing = true
		a.addNotification("info", i18n.T("chat_b.voice.transcribing"))
	case VoiceStateIdle:
		a.voiceRecording = false
		a.voiceTranscribing = false
		a.voiceInterimText = ""
	case VoiceStateError:
		a.voiceRecording = false
		a.voiceTranscribing = false
	}

	if a.settingsManager != nil && a.settingsManager.GetVoiceSettings() != nil {
		phase := settings.VoicePhaseReady
		detail := msg.State.String()
		switch msg.State {
		case VoiceStateConnecting:
			phase = settings.VoicePhaseChecking
		case VoiceStateRecording:
			phase = settings.VoicePhaseRecording
		case VoiceStateTranscribing:
			phase = settings.VoicePhaseTranscribing
		case VoiceStateError:
			phase = settings.VoicePhaseFailed
		}
		a.settingsManager.GetVoiceSettings().SetRuntimePhase(phase, detail)
	}

	// Re-chain the listener so the next event is delivered.
	return a, listenForVoiceEvent(a.voiceEventCh)
}

// handleVoiceInterim handles interim transcript messages
func (a *App) handleVoiceInterim(msg VoiceInterimMsg) (tea.Model, tea.Cmd) {
	a.voiceInterimText = msg.Text
	return a, listenForVoiceEvent(a.voiceEventCh)
}

// handleVoiceFinal handles final transcript messages
func (a *App) handleVoiceFinal(msg VoiceFinalMsg) (tea.Model, tea.Cmd) {
	a.voiceFinalText = appendFinalText(a.voiceFinalText, msg.Text)
	a.voiceInterimText = ""
	return a, listenForVoiceEvent(a.voiceEventCh)
}

// handleVoiceComplete handles voice completion messages
func (a *App) handleVoiceComplete(msg VoiceCompleteMsg) (tea.Model, tea.Cmd) {
	a.voiceRecording = false
	a.voiceTranscribing = false
	a.voiceInterimText = ""

	text := msg.Text
	autoSubmit := false

	// Check for auto-submit command phrases at end of transcript
	if text != "" {
		lowerText := strings.ToLower(strings.TrimSpace(text))
		for _, phrase := range []string{"send prompt", "send message", "submit"} {
			if strings.HasSuffix(lowerText, phrase) {
				// Strip the command phrase from the end
				text = strings.TrimSpace(text[:len(text)-len(phrase)])
				autoSubmit = true
				break
			}
		}
	}

	// Insert transcript into the active input — either the chat input (ScreenChat)
	// or the home input (landing screen).
	if text != "" {
		var target *SimpleInput
		if a.screen == ScreenChat && a.textInput != nil {
			target = a.textInput
		} else if a.homeInput != nil {
			target = a.homeInput
		}
		if target != nil {
			currentVal := target.Value()
			if currentVal != "" {
				target.SetValue(currentVal + " " + text)
			} else {
				target.SetValue(text)
			}
		}
		a.addNotification("success", i18n.T("classic_chat.voice.transcribed", text))
	}

	a.voiceFinalText = ""

	var cmds []tea.Cmd
	cmds = append(cmds, listenForVoiceEvent(a.voiceEventCh))

	if autoSubmit && text != "" {
		if submitCmd := a.submitFromVoice(); submitCmd != nil {
			cmds = append(cmds, submitCmd)
		}
	}

	// Auto-record: restart recording after a delay
	if a.voiceAutoRecord {
		cmds = append(cmds, voiceAutoRecordCmd())
	}

	return a, tea.Batch(cmds...)
}

// handleVoiceError handles voice error messages
func (a *App) handleVoiceError(msg VoiceErrorMsg) (tea.Model, tea.Cmd) {
	a.voiceRecording = false
	a.voiceTranscribing = false
	a.voiceInterimText = ""
	a.voiceFinalText = ""

	// If auto-record is enabled, a capture/service failure can otherwise loop
	// into repeated recording attempts and repeated Voice error notifications.
	// Disable it on error; the user can re-enable it once the underlying voice
	// service/microphone issue is fixed.
	if a.voiceAutoRecord {
		a.voiceAutoRecord = false
		a.addNotification("warning", i18n.T("classic_chat.voice.auto_record_disabled"))
	}

	notificationKind, errText, dedupeWindow := voiceErrorNotification(msg.Error)
	now := time.Now()
	if errText != a.lastVoiceError || now.Sub(a.lastVoiceErrorAt) > dedupeWindow {
		a.addNotification(notificationKind, errText)
		a.lastVoiceError = errText
		a.lastVoiceErrorAt = now
	} else {
	}

	// Re-chain even on error so future recording attempts work.
	return a, listenForVoiceEvent(a.voiceEventCh)
}

func voiceErrorNotification(err error) (kind string, text string, dedupeWindow time.Duration) {
	if err == nil {
		return "error", i18n.T("classic_chat.voice.unknown_error"), 10 * time.Second
	}
	raw := err.Error()
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "connection refused") &&
		(strings.Contains(lower, "127.0.0.1") || strings.Contains(lower, "localhost")) {
		return "warning",
			i18n.T("classic_chat.voice.local_server_stopped"),
			60 * time.Second
	}
	return "error", i18n.T("classic_chat.voice.error", err), 10 * time.Second
}

// loadVoiceOAuthToken reads the OAuth access token from ~/.swarmos/oauth.json
func loadVoiceOAuthToken() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "oauth.json"))
	if err != nil {
		return ""
	}
	var oauthFile struct {
		Token struct {
			AccessToken string `json:"access_token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(data, &oauthFile); err != nil {
		return ""
	}
	return oauthFile.Token.AccessToken
}

// initVoice initializes the voice manager from the same canonical configuration
// used by the settings screen. Local providers do not require Anthropic auth.
func (a *App) initVoice() {
	home, err := os.UserHomeDir()
	if err != nil {
		a.voiceEnabled = false
		return
	}
	configPath := filepath.Join(home, ".swarmos", "voice_config.json")
	settings, err := voice.LoadTranscriptionSettings(configPath)
	if err != nil {
		a.addNotification("error", i18n.T("classic_chat.voice.config_error", err))
		settings = voice.DefaultTranscriptionSettings()
	}

	provider := settings.SelectedProvider
	providerSettings := settings.Provider()
	// Migrate the old empty custom-REST selection to the discoverable local
	// OpenAI-compatible service instead of leaving voice silently unusable.
	if provider == voice.ProviderREST && strings.TrimSpace(providerSettings.BaseURL) == "" {
		provider = voice.ProviderOpenAICompatible
		providerSettings = voice.DefaultTranscriptionSettings().Provider()
	}

	apiKey := providerSettings.APIKey
	baseURL := strings.TrimSpace(providerSettings.BaseURL)
	if apiKey == "" {
		switch provider {
		case voice.ProviderGroq:
			apiKey = os.Getenv("GROQ_API_KEY")
		case voice.ProviderOpenAI:
			apiKey = os.Getenv("OPENAI_API_KEY")
		case voice.ProviderOpenRouter:
			apiKey = os.Getenv("OPENROUTER_API_KEY")
		case voice.ProviderOpenAICompatible:
			apiKey = os.Getenv("VOICE_OPENAI_COMPATIBLE_API_KEY")
		case voice.ProviderREST:
			apiKey = os.Getenv("VOICE_REST_API_KEY")
		}
	}

	if provider == voice.ProviderWebSocket {
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("CLAUDE_API_KEY")
		}
		if apiKey == "" {
			key, _, loadErr := loadProviderCredentials("anthropic")
			if loadErr == nil {
				apiKey = key
			}
		}
		if apiKey == "" {
			apiKey = loadVoiceOAuthToken()
		}
		if baseURL == "" {
			baseURL = os.Getenv("VOICE_STREAM_BASE_URL")
		}
		if baseURL == "" {
			baseURL = "wss://api.anthropic.com/api/ws/speech_to_text/voice_stream"
		}
	}
	if provider == voice.ProviderOpenAICompatible && baseURL == "" {
		baseURL = voice.DefaultLocalTranscriptionURL
	}

	voiceCfg := &VoiceConfig{
		BaseURL:        baseURL,
		AuthToken:      apiKey,
		UserAgent:      "SwarmTUI",
		AppID:          "swarm-tui",
		Provider:       provider,
		ProviderAPIKey: apiKey,
		ProviderURL:    baseURL,
		ProviderModel:  providerSettings.Model,
		Language:       providerSettings.Language,
		SelectedDevice: settings.SelectedDevice,
	}

	vm, err := NewVoiceManager(voiceCfg)
	if err != nil {
		a.voiceEnabled = false
		a.addNotification("error", i18n.T("classic_chat.voice.init_failed", err))
		return
	}

	eventCh := make(chan any, 100)
	vm.SetEventCallback(func(event any) {
		select {
		case eventCh <- event:
		default:
		}
	})

	a.voiceEventCh = eventCh
	a.voiceManager = vm
	a.voiceEnabled = true
}

type voiceRuntimeResultMsg struct {
	action     settings.VoiceRuntimeAction
	inspection voiceruntime.Inspection
	state      voiceruntime.State
	health     voiceruntime.Health
	err        error
}

func voiceRuntimeCapabilities(c voiceruntime.Capabilities) settings.VoiceRuntimeCapabilities {
	return settings.VoiceRuntimeCapabilities{
		CanCheck:       c.Connect,
		CanDownload:    c.Install,
		CanStart:       c.Start,
		CanRecord:      c.Connect,
		CanTranscribe:  c.Connect,
		ManagedRuntime: c.Mode == voiceruntime.ModeManaged || c.Mode == voiceruntime.ModeAdopted,
	}
}

// handleVoiceRuntimeAction performs setup work outside View/Update's render path.
func (a *App) handleVoiceRuntimeAction(msg settings.VoiceRuntimeActionMsg) tea.Cmd {
	if a.settingsManager == nil || a.settingsManager.GetVoiceSettings() == nil {
		return nil
	}
	voiceSettings := a.settingsManager.GetVoiceSettings()
	configured := voiceSettings.TranscriptionSettings()
	providerSettings := configured.Provider()

	// Apply the latest saved provider/device selection before testing it.
	a.closeVoice()
	a.initVoice()
	voiceSettings.ApplyRuntimeUpdate(settings.VoiceRuntimeUpdate{
		Phase:         settings.VoicePhaseChecking,
		Detail:        i18n.T("classic_chat.voice.checking_provider"),
		Capabilities:  voiceSettings.RuntimeCapabilities(),
		ProgressLabel: i18n.T("classic_chat.voice.checking"),
	})

	if !msg.Managed && msg.Provider != voice.ProviderOpenAICompatible {
		return func() tea.Msg {
			return voiceRuntimeResultMsg{
				action: msg.Action,
				health: voiceruntime.Health{Healthy: true, Status: "configured", Endpoint: providerSettings.BaseURL},
			}
		}
	}

	manager, err := voiceruntime.NewNemoManager(voiceruntime.NemoConfig{
		Endpoint: providerSettings.BaseURL,
		Port:     configured.Runtime.Port,
		Model:    providerSettings.Model,
	})
	if err != nil {
		return func() tea.Msg { return voiceRuntimeResultMsg{action: msg.Action, err: err} }
	}
	if msg.Action != settings.VoiceActionCheck {
		voiceSettings.SetRuntimePhase(settings.VoicePhaseStarting, i18n.T("classic_chat.voice.preparing_nvidia"))
	}

	return func() tea.Msg {
		inspection, inspectErr := manager.Inspect(context.Background())
		result := voiceRuntimeResultMsg{action: msg.Action, inspection: inspection, state: inspection.State}
		if inspectErr != nil {
			result.err = inspectErr
			return result
		}
		if inspection.State.Status == voiceruntime.StatusRunning {
			result.health = voiceruntime.Health{Healthy: true, Status: "ready", Endpoint: inspection.State.Endpoint}
			return result
		}
		if msg.Action == settings.VoiceActionCheck {
			result.err = fmt.Errorf("transcription service is not ready: %s", inspection.State.Message)
			return result
		}

		progress := func(update voiceruntime.Progress) {
		}
		switch inspection.State.Status {
		case voiceruntime.StatusMissing, voiceruntime.StatusStopped, voiceruntime.StatusUnhealthy:
			result.state, result.err = manager.Start(context.Background(), progress)
		default:
			result.err = fmt.Errorf("local runtime cannot start from %s: %s", inspection.State.Status, inspection.State.Message)
		}
		if result.err != nil {
			return result
		}
		inspection, result.err = manager.Inspect(context.Background())
		result.inspection = inspection
		result.state = inspection.State
		if result.err == nil && inspection.State.Status != voiceruntime.StatusRunning {
			result.err = fmt.Errorf("transcription service is not ready: %s", inspection.State.Message)
		}
		if result.err == nil {
			result.health = voiceruntime.Health{Healthy: true, Status: "ready", Endpoint: inspection.State.Endpoint}
		}
		return result
	}
}

func voiceRuntimeReady(msg voiceRuntimeResultMsg) bool {
	return msg.health.Status == "configured" || msg.inspection.State.Status == voiceruntime.StatusRunning
}

func (a *App) handleVoiceRuntimeResult(msg voiceRuntimeResultMsg) (tea.Model, tea.Cmd) {
	if a.settingsManager == nil || a.settingsManager.GetVoiceSettings() == nil {
		return a, nil
	}
	voiceSettings := a.settingsManager.GetVoiceSettings()
	capabilities := voiceRuntimeCapabilities(msg.inspection.Capabilities)
	if msg.err != nil {
		voiceSettings.ApplyRuntimeUpdate(settings.VoiceRuntimeUpdate{
			Phase: settings.VoicePhaseFailed, Detail: msg.err.Error(), Capabilities: capabilities,
		})
		a.addNotification("error", i18n.T("classic_chat.voice.setup_error", msg.err))
		return a, nil
	}
	if voiceRuntimeReady(msg) {
		detail := i18n.T("classic_chat.voice.provider_ready")
		if msg.health.Endpoint != "" {
			detail = i18n.T("classic_chat.voice.ready_at", msg.health.Endpoint)
		}
		voiceSettings.ApplyRuntimeUpdate(settings.VoiceRuntimeUpdate{
			Phase: settings.VoicePhaseReady, Detail: detail, Progress: 1, ProgressLabel: i18n.T("classic_chat.voice.ready"), Capabilities: capabilities,
		})
		a.addNotification("success", detail)
		a.closeVoice()
		a.initVoice()
		return a, nil
	}
	voiceSettings.ApplyRuntimeUpdate(settings.VoiceRuntimeUpdate{
		Phase: settings.VoicePhaseFailed, Detail: i18n.T("classic_chat.voice.provider_unhealthy"), Capabilities: capabilities,
	})
	return a, nil
}

// closeVoice cleans up the voice manager
func (a *App) closeVoice() {
	if a.voiceManager != nil {
		_ = a.voiceManager.Close()
		a.voiceManager = nil
	}
	if a.voiceEventCh != nil {
		close(a.voiceEventCh)
		a.voiceEventCh = nil
	}
}

// voiceTickCmd returns a tea.Cmd that sends a voiceTickMsg after 100ms.
func voiceTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return voiceTickMsg{}
	})
}

// voiceAutoRecordCmd returns a tea.Cmd that sends a voiceAutoRecordMsg after 500ms.
func voiceAutoRecordCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return voiceAutoRecordMsg{}
	})
}

// submitFromVoice simulates pressing Enter to submit the current input.
func (a *App) submitFromVoice() tea.Cmd {
	if a.screen == ScreenChat && a.textInput != nil {
		// GetSubmitValue() expands paste-indicator chips to their real content so
		// pasted text is included in slash commands (e.g. /goal <pasted text>).
		inputValue := strings.TrimSpace(a.textInput.GetSubmitValue())
		if inputValue == "" {
			return nil
		}
		if strings.HasPrefix(inputValue, "/") {
			return a.handleSlashCommand(inputValue)
		}
		return a.handleSendMessage()
	} else if a.homeInput != nil {
		inputText := strings.TrimSpace(a.homeInput.GetSubmitValue())
		if inputText == "" {
			return nil
		}
		if strings.HasPrefix(inputText, "/") {
			if isClearSlashCommand(inputText) {
				a.textInput.SetValue(inputText)
				a.homeInput.SetValue("")
				return a.handleSlashCommand(inputText)
			}
			a.startNewChatDirect()
			a.textInput.SetValue(inputText)
			a.homeInput.SetValue("")
			return a.handleSlashCommand(inputText)
		}
		a.startNewChatDirect()
		a.textInput.SetValue(inputText)
		a.homeInput.SetValue("")
		return a.handleSendMessage()
	}
	return nil
}
