package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/voice"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// Theme colors (explicit background, Tokyo Night palette used by this screen)
// ---------------------------------------------------------------------------

var (
	voiceBgColor     = lipgloss.Color("#1a1b26")
	voiceTitleColor  = lipgloss.Color("#7dcfff")
	voiceAccentColor = lipgloss.Color("#bb9af7")
	voiceLabelColor  = lipgloss.Color("#7aa2f7")
	voiceTextColor   = lipgloss.Color("#a9b1d6")
	voiceMutedColor  = lipgloss.Color("#565f89")
	voiceGoodColor   = lipgloss.Color("#9ece6a")
	voiceWarnColor   = lipgloss.Color("#e0af68")
	voiceBadColor    = lipgloss.Color("#f7768e")
	voiceSelectBg    = lipgloss.Color("#3d59a1")
)

// ---------------------------------------------------------------------------
// Runtime phase model (pure state; app integration drives transitions)
// ---------------------------------------------------------------------------

// VoiceRuntimePhase describes the transcription pipeline readiness lifecycle.
type VoiceRuntimePhase int

const (
	VoicePhaseNotConfigured VoiceRuntimePhase = iota
	VoicePhaseChecking
	VoicePhaseDownloading
	VoicePhaseStarting
	VoicePhaseReady
	VoicePhaseRecording
	VoicePhaseTranscribing
	VoicePhaseFailed
)

// String returns the user-facing name of the phase.
func (p VoiceRuntimePhase) String() string {
	switch p {
	case VoicePhaseChecking:
		return i18n.T("settings.integrations.voice.phase.checking")
	case VoicePhaseDownloading:
		return i18n.T("settings.integrations.voice.phase.downloading")
	case VoicePhaseStarting:
		return i18n.T("settings.integrations.voice.phase.starting")
	case VoicePhaseReady:
		return i18n.T("settings.integrations.voice.phase.ready")
	case VoicePhaseRecording:
		return i18n.T("settings.integrations.voice.phase.recording")
	case VoicePhaseTranscribing:
		return i18n.T("settings.integrations.voice.phase.transcribing")
	case VoicePhaseFailed:
		return i18n.T("settings.integrations.voice.phase.failed")
	default:
		return i18n.T("settings.integrations.voice.phase.not_configured")
	}
}

// VoiceRuntimeCapabilities describes which integration-owned operations are
// currently available. Recording/runtime execution remains outside this view.
type VoiceRuntimeCapabilities struct {
	CanCheck       bool
	CanDownload    bool
	CanStart       bool
	CanRecord      bool
	CanTranscribe  bool
	ManagedRuntime bool
}

// VoiceRuntimeUpdate is the complete pure state update accepted from app
// integration. Progress is clamped to 0..1.
type VoiceRuntimeUpdate struct {
	Phase         VoiceRuntimePhase
	Detail        string
	Progress      float64
	ProgressLabel string
	Capabilities  VoiceRuntimeCapabilities
}

// VoiceRuntimeAction identifies the primary action the user requested.
// The settings screen never executes Docker or network work itself; it emits
// this message so app integration can perform the action.
type VoiceRuntimeAction int

const (
	VoiceActionCheck VoiceRuntimeAction = iota
	VoiceActionStartRuntime
	VoiceActionRetry
)

// VoiceRuntimeActionMsg is emitted when the user activates the primary action.
type VoiceRuntimeActionMsg struct {
	Action   VoiceRuntimeAction
	Provider voice.ProviderType
	Managed  bool
}

// ---------------------------------------------------------------------------
// Provider options (UI-level; two options share ProviderOpenAICompatible)
// ---------------------------------------------------------------------------

type transcriptionOption struct {
	ID          string
	Provider    voice.ProviderType
	Managed     bool
	Name        string
	Description string
	NeedsKey    bool // required API key
	NeedsURL    bool // required base URL
	OptionalKey bool // auth shown but optional
	EnvHint     string
}

var transcriptionOptions = []transcriptionOption{
	{ID: "nemo-local", Provider: voice.ProviderOpenAICompatible, Managed: true,
		Name: "Local NVIDIA NeMo (managed)", Description: "Runs on this machine; no API key needed"},
	{ID: "compatible", Provider: voice.ProviderOpenAICompatible,
		Name: "OpenAI-compatible / Custom", Description: "Any OpenAI-compatible transcription server",
		NeedsURL: true, OptionalKey: true},
	{ID: "groq", Provider: voice.ProviderGroq,
		Name: "Groq (Whisper)", Description: "Fast, affordable transcription",
		NeedsKey: true, EnvHint: "GROQ_API_KEY"},
	{ID: "openai", Provider: voice.ProviderOpenAI,
		Name: "OpenAI (Whisper)", Description: "High quality transcription",
		NeedsKey: true, EnvHint: "OPENAI_API_KEY"},
	{ID: "openrouter", Provider: voice.ProviderOpenRouter,
		Name: "OpenRouter", Description: "Multiple model support",
		NeedsKey: true, EnvHint: "OPENROUTER_API_KEY"},
	{ID: "anthropic", Provider: voice.ProviderWebSocket,
		Name: "Anthropic (streaming)", Description: "Streaming transcription proxy",
		NeedsKey: true, EnvHint: "ANTHROPIC_API_KEY"},
}

func localizedVoiceProviderName(opt transcriptionOption) string {
	switch opt.ID {
	case "nemo-local":
		return i18n.T("settings.residual_final.voice.provider.nemo")
	case "compatible":
		return i18n.T("settings.residual_final.voice.provider.compatible")
	case "anthropic":
		return i18n.T("settings.residual_final.voice.provider.anthropic")
	default:
		return opt.Name
	}
}

func localizedVoiceProviderDescription(opt transcriptionOption) string {
	switch opt.ID {
	case "nemo-local":
		return i18n.T("settings.residual_final.voice.provider.nemo_description")
	case "compatible":
		return i18n.T("settings.residual_final.voice.provider.compatible_description")
	case "groq":
		return i18n.T("settings.residual_final.voice.provider.groq_description")
	case "openai":
		return i18n.T("settings.residual_final.voice.provider.openai_description")
	case "openrouter":
		return i18n.T("settings.residual_final.voice.provider.openrouter_description")
	case "anthropic":
		return i18n.T("settings.residual_final.voice.provider.anthropic_description")
	default:
		return opt.Description
	}
}

// ---------------------------------------------------------------------------
// Row model (progressive disclosure)
// ---------------------------------------------------------------------------

type voiceRowID int

const (
	rowProvider voiceRowID = iota
	rowPrimaryAction
	rowAPIKey
	rowBaseURL
	rowModel
	rowMicrophone
	rowAdvancedToggle
	rowLanguage
	rowRuntimePort
	rowAutoStart
	rowMonitor
)

type voiceRow struct {
	id    voiceRowID
	label string
	value string
}

type voiceEditField int

const (
	editNone voiceEditField = iota
	editKey
	editURL
	editModel
	editLanguage
	editPort
)

// ---------------------------------------------------------------------------
// VoiceSettings
// ---------------------------------------------------------------------------

// VoiceSettings renders the minimal progressive Transcription screen and
// owns microphone selection plus audio level monitoring.
type VoiceSettings struct {
	settings   voice.TranscriptionSettings
	configPath string

	// Audio
	devices        []voice.DeviceInfo
	audioLevels    voice.AudioLevels
	isMonitoring   bool
	monitorCancel  context.CancelFunc
	capture        voice.AudioCapture
	deviceListOpen bool
	deviceIndex    int

	// Navigation
	cursor       int
	pickerOpen   bool
	pickerIndex  int
	advancedOpen bool

	// Inline editing
	editing voiceEditField
	input   string

	// Runtime status (pure state; set by app integration)
	phase         VoiceRuntimePhase
	phaseDetail   string
	progressFrac  float64
	progressLabel string
	capabilities  VoiceRuntimeCapabilities
	saveErr       string
}

// NewVoiceSettings creates the transcription settings section using the
// canonical config location (~/.swarmos/voice_config.json).
func NewVoiceSettings() *VoiceSettings {
	path := ""
	if home, err := os.UserHomeDir(); err == nil {
		path = filepath.Join(home, ".swarmos", "voice_config.json")
	}
	return NewVoiceSettingsWithConfigPath(path)
}

// NewVoiceSettingsWithConfigPath creates the section against an explicit
// config path. Used by tests and embedding callers.
func NewVoiceSettingsWithConfigPath(path string) *VoiceSettings {
	vs := &VoiceSettings{
		configPath: path,
		settings:   voice.DefaultTranscriptionSettings(),
	}
	if path != "" {
		if loaded, err := voice.LoadTranscriptionSettings(path); err == nil {
			vs.settings = loaded
		}
	}
	vs.settings.Normalize()
	return vs
}

// ---------------------------------------------------------------------------
// Option / capability helpers (pure)
// ---------------------------------------------------------------------------

func (v *VoiceSettings) currentOption() transcriptionOption {
	sel := v.settings.SelectedProvider
	prov := v.settings.Providers[string(sel)]
	for _, opt := range transcriptionOptions {
		if opt.Provider != sel {
			continue
		}
		if sel == voice.ProviderOpenAICompatible && opt.Managed != prov.Managed {
			continue
		}
		return opt
	}
	// Legacy providers (rest, deepgram, ...) fall back to the custom option.
	return transcriptionOptions[1]
}

func (v *VoiceSettings) providerSettings() voice.TranscriptionProviderSettings {
	return v.settings.Providers[string(v.settings.SelectedProvider)]
}

func (v *VoiceSettings) setProviderSettings(p voice.TranscriptionProviderSettings) {
	if v.settings.Providers == nil {
		v.settings.Providers = make(map[string]voice.TranscriptionProviderSettings)
	}
	v.settings.Providers[string(v.settings.SelectedProvider)] = p
}

// IsConfigured reports whether the selected provider has its required inputs.
func (v *VoiceSettings) IsConfigured() bool {
	opt := v.currentOption()
	p := v.providerSettings()
	if opt.NeedsKey && strings.TrimSpace(p.APIKey) == "" {
		return false
	}
	if opt.NeedsURL && strings.TrimSpace(p.BaseURL) == "" && !opt.Managed {
		// The canonical config defaults compatible BaseURL, so treat the
		// normalized default as configured.
		return v.settings.Provider().BaseURL != ""
	}
	return true
}

// RuntimePhase returns the current lifecycle phase.
func (v *VoiceSettings) RuntimePhase() VoiceRuntimePhase { return v.phase }

// SetRuntimePhase records a lifecycle transition pushed by app integration.
// It performs no I/O.
func (v *VoiceSettings) SetRuntimePhase(phase VoiceRuntimePhase, detail string) {
	v.phase = phase
	v.phaseDetail = detail
	if phase != VoicePhaseDownloading && phase != VoicePhaseStarting {
		v.progressFrac = 0
		v.progressLabel = ""
	}
}

// SetRuntimeProgress records download/startup progress (0..1). Pure state.
func (v *VoiceSettings) SetRuntimeProgress(fraction float64, label string) {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	v.progressFrac = fraction
	v.progressLabel = label
}

// RuntimeCapabilities returns the latest integration-owned capability set.
func (v *VoiceSettings) RuntimeCapabilities() VoiceRuntimeCapabilities {
	return v.capabilities
}

// SetRuntimeCapabilities replaces the integration-owned capability set.
func (v *VoiceSettings) SetRuntimeCapabilities(capabilities VoiceRuntimeCapabilities) {
	v.capabilities = capabilities
}

// ApplyRuntimeUpdate atomically applies readiness, progress, and capability
// state. It is deliberately pure and safe to call from an app update handler.
func (v *VoiceSettings) ApplyRuntimeUpdate(update VoiceRuntimeUpdate) {
	v.SetRuntimePhase(update.Phase, update.Detail)
	v.SetRuntimeProgress(update.Progress, update.ProgressLabel)
	v.SetRuntimeCapabilities(update.Capabilities)
}

// StatusLine returns the readiness summary shown in the header.
func (v *VoiceSettings) StatusLine() string {
	s := v.phase.String()
	if v.phase == VoicePhaseNotConfigured && !v.IsConfigured() {
		opt := v.currentOption()
		if opt.NeedsKey {
			s = i18n.T("settings.integrations.voice.not_configured_key")
		} else if opt.NeedsURL {
			s = i18n.T("settings.integrations.voice.not_configured_url")
		}
	}
	if v.phaseDetail != "" {
		s += " — " + v.phaseDetail
	}
	return s
}

// PrimaryAction returns the single primary action label and whether it can be
// activated right now.
func (v *VoiceSettings) PrimaryAction() (string, bool) {
	opt := v.currentOption()
	switch v.phase {
	case VoicePhaseChecking:
		return i18n.T("settings.integrations.voice.checking"), false
	case VoicePhaseDownloading:
		return i18n.T("settings.integrations.voice.downloading"), false
	case VoicePhaseStarting:
		return i18n.T("settings.integrations.voice.starting"), false
	case VoicePhaseReady:
		return i18n.T("settings.integrations.voice.ready"), false
	case VoicePhaseRecording:
		return i18n.T("settings.integrations.voice.recording"), false
	case VoicePhaseTranscribing:
		return i18n.T("settings.integrations.voice.transcribing"), false
	case VoicePhaseFailed:
		return i18n.T("settings.integrations.voice.retry"), true
	}
	p := v.providerSettings()
	if opt.NeedsKey && strings.TrimSpace(p.APIKey) == "" {
		return i18n.T("settings.integrations.voice.add_key"), true
	}
	if opt.NeedsURL && !opt.Managed && strings.TrimSpace(p.BaseURL) == "" &&
		v.settings.Provider().BaseURL == "" {
		return i18n.T("settings.integrations.voice.set_url"), true
	}
	if opt.Managed {
		return i18n.T("settings.integrations.voice.setup_local"), true
	}
	return i18n.T("settings.integrations.voice.check_readiness"), true
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

func (v *VoiceSettings) visibleRows() []voiceRow {
	opt := v.currentOption()
	p := v.providerSettings()

	rows := []voiceRow{{id: rowProvider, label: i18n.T("settings.residual_final.common.provider"), value: localizedVoiceProviderName(opt)}}

	actionLabel, _ := v.PrimaryAction()
	rows = append(rows, voiceRow{id: rowPrimaryAction, label: i18n.T("settings.residual_final.voice.action"), value: actionLabel})

	if opt.NeedsKey || opt.OptionalKey {
		rows = append(rows, voiceRow{id: rowAPIKey, label: apiKeyLabel(opt), value: v.maskedKey(p.APIKey)})
	}
	if opt.NeedsURL && !opt.Managed {
		url := p.BaseURL
		if url == "" {
			url = voice.DefaultLocalTranscriptionURL
		}
		rows = append(rows, voiceRow{id: rowBaseURL, label: i18n.T("settings.residual_final.voice.server_url"), value: url})
		model := p.Model
		if model == "" {
			model = voice.DefaultLocalTranscriptionModel
		}
		rows = append(rows, voiceRow{id: rowModel, label: i18n.T("settings.residual_final.common.model"), value: model})
	}

	rows = append(rows, voiceRow{id: rowMicrophone, label: i18n.T("settings.residual_final.voice.microphone"), value: v.selectedDeviceName()})

	marker := "▸"
	if v.advancedOpen {
		marker = "▾"
	}
	rows = append(rows, voiceRow{id: rowAdvancedToggle, label: i18n.T("settings.residual_final.voice.advanced", marker)})

	if v.advancedOpen {
		lang := p.Language
		if lang == "" {
			lang = v.settings.Provider().Language
		}
		rows = append(rows, voiceRow{id: rowLanguage, label: i18n.T("settings.residual_final.voice.language"), value: lang})
		if !opt.NeedsURL || opt.Managed {
			model := p.Model
			if model == "" && opt.Managed {
				model = voice.DefaultLocalTranscriptionModel
			}
			rows = append(rows, voiceRow{id: rowModel, label: i18n.T("settings.residual_final.common.model"), value: model})
		}
		if opt.Managed {
			rows = append(rows,
				voiceRow{id: rowRuntimePort, label: i18n.T("settings.residual_final.voice.runtime_port"), value: strconv.Itoa(v.runtimePort())},
				voiceRow{id: rowAutoStart, label: i18n.T("settings.residual_final.voice.auto_start"), value: onOff(v.settings.Runtime.AutoStart)},
			)
		}
		rows = append(rows, voiceRow{id: rowMonitor, label: i18n.T("settings.residual_final.voice.level_monitor"), value: onOff(v.isMonitoring)})
	}
	return rows
}

func apiKeyLabel(opt transcriptionOption) string {
	if opt.OptionalKey {
		return i18n.T("settings.residual_final.voice.api_key_optional")
	}
	return i18n.T("settings.residual_final.voice.api_key")
}

func onOff(b bool) string {
	if b {
		return i18n.T("settings.residual_final.common.on_lower")
	}
	return i18n.T("settings.residual_final.common.off_lower")
}

func (v *VoiceSettings) maskedKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return i18n.T("settings.residual_final.common.not_set")
	}
	if len(key) > 8 {
		return "••••" + key[len(key)-4:]
	}
	return "••••"
}

func (v *VoiceSettings) runtimePort() int {
	if v.settings.Runtime.Port > 0 {
		return v.settings.Runtime.Port
	}
	return 8001
}

func (v *VoiceSettings) selectedDeviceName() string {
	if len(v.devices) == 0 {
		return i18n.T("settings.residual_final.voice.no_microphones")
	}
	for _, d := range v.devices {
		if d.DeviceID == v.settings.SelectedDevice {
			name := d.Name
			if d.IsDefault {
				name += " (default)"
			}
			return name
		}
	}
	return v.devices[0].Name
}

func (v *VoiceSettings) clampCursor() {
	rows := v.visibleRows()
	if v.cursor >= len(rows) {
		v.cursor = len(rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
}

// ---------------------------------------------------------------------------
// Lifecycle / audio plumbing
// ---------------------------------------------------------------------------

// Init initializes voice settings, discovers devices, and honors managed runtime auto-start.
func (v *VoiceSettings) Init() tea.Cmd {
	discover := v.discoverDevices()
	opt := v.currentOption()
	if !v.settings.Runtime.AutoStart || !opt.Managed {
		return discover
	}
	return tea.Batch(discover, func() tea.Msg {
		return VoiceRuntimeActionMsg{Action: VoiceActionStartRuntime, Provider: opt.Provider, Managed: true}
	})
}

// SetCapture sets the audio capture implementation.
func (v *VoiceSettings) SetCapture(capture voice.AudioCapture) {
	v.capture = capture
}

func (v *VoiceSettings) discoverDevices() tea.Cmd {
	return func() tea.Msg {
		if v.capture == nil {
			cfg := voice.DefaultConfig()
			capture, err := voice.NewAudioCapture(cfg)
			if err != nil {
				return devicesDiscoveredMsg{err: err}
			}
			v.capture = capture
		}
		devices, err := v.capture.DiscoverDevices()
		return devicesDiscoveredMsg{devices: devices, err: err}
	}
}

type devicesDiscoveredMsg struct {
	devices []voice.DeviceInfo
	err     error
}

type audioLevelsUpdateMsg struct {
	levels voice.AudioLevels
}

type configSavedMsg struct {
	err error
}

// HandleMsg handles async messages for voice settings.
func (v *VoiceSettings) HandleMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case devicesDiscoveredMsg:
		if msg.err == nil {
			v.devices = msg.devices
			if v.settings.SelectedDevice == "" && len(v.devices) > 0 {
				for _, dev := range v.devices {
					if dev.IsDefault {
						v.settings.SelectedDevice = dev.DeviceID
						break
					}
				}
				if v.settings.SelectedDevice == "" {
					v.settings.SelectedDevice = v.devices[0].DeviceID
				}
			}
		}
		return nil
	case audioLevelsUpdateMsg:
		v.audioLevels = msg.levels
		return v.requestLevelsUpdate()
	case configSavedMsg:
		if msg.err != nil {
			v.saveErr = msg.err.Error()
		} else {
			v.saveErr = ""
		}
		return nil
	}
	return nil
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

// Update handles key events for the transcription screen.
func (v *VoiceSettings) Update(key string) tea.Cmd {
	if v.editing != editNone {
		return v.handleEditKey(key)
	}
	if v.pickerOpen {
		return v.handlePickerKey(key)
	}
	if v.deviceListOpen {
		return v.handleDeviceKey(key)
	}

	switch key {
	case "up", "k":
		if v.cursor > 0 {
			v.cursor--
		}
	case "down", "j", "tab":
		if v.cursor < len(v.visibleRows())-1 {
			v.cursor++
		}
	case "enter", " ":
		return v.activateRow()
	case "ctrl+r":
		return v.toggleMonitoring()
	case "ctrl+s":
		return v.saveConfiguration()
	}
	return nil
}

func (v *VoiceSettings) handlePickerKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		if v.pickerIndex > 0 {
			v.pickerIndex--
		}
	case "down", "j", "tab":
		if v.pickerIndex < len(transcriptionOptions)-1 {
			v.pickerIndex++
		}
	case "enter", " ":
		v.pickerOpen = false
		return v.selectOption(transcriptionOptions[v.pickerIndex])
	case "esc", "escape":
		v.pickerOpen = false
	}
	return nil
}

func (v *VoiceSettings) handleDeviceKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		if v.deviceIndex > 0 {
			v.deviceIndex--
		}
	case "down", "j", "tab":
		if v.deviceIndex < len(v.devices)-1 {
			v.deviceIndex++
		}
	case "enter", " ":
		v.deviceListOpen = false
		if v.deviceIndex >= 0 && v.deviceIndex < len(v.devices) {
			v.SetSelectedDevice(v.devices[v.deviceIndex].DeviceID)
			return v.saveConfiguration()
		}
	case "esc", "escape":
		v.deviceListOpen = false
	}
	return nil
}

func (v *VoiceSettings) handleEditKey(key string) tea.Cmd {
	switch key {
	case "enter":
		return v.commitEdit()
	case "esc", "escape":
		v.editing = editNone
		v.input = ""
	case "backspace":
		if len(v.input) > 0 {
			v.input = v.input[:len(v.input)-1]
		}
	default:
		if len(key) == 1 {
			v.input += key
		}
	}
	return nil
}

func (v *VoiceSettings) commitEdit() tea.Cmd {
	p := v.providerSettings()
	value := strings.TrimSpace(v.input)
	switch v.editing {
	case editKey:
		p.APIKey = value
	case editURL:
		p.BaseURL = value
	case editModel:
		p.Model = value
	case editLanguage:
		p.Language = value
	case editPort:
		if port, err := strconv.Atoi(value); err == nil && port > 0 && port < 65536 {
			v.settings.Runtime.Port = port
			if v.currentOption().Managed {
				p.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
			}
		}
	}
	v.setProviderSettings(p)
	v.editing = editNone
	v.input = ""
	return v.saveConfiguration()
}

func (v *VoiceSettings) activateRow() tea.Cmd {
	rows := v.visibleRows()
	v.clampCursor()
	if len(rows) == 0 {
		return nil
	}
	p := v.providerSettings()
	switch rows[v.cursor].id {
	case rowProvider:
		v.pickerOpen = true
		cur := v.currentOption()
		v.pickerIndex = 0
		for i, opt := range transcriptionOptions {
			if opt.ID == cur.ID {
				v.pickerIndex = i
				break
			}
		}
	case rowPrimaryAction:
		return v.activatePrimaryAction()
	case rowAPIKey:
		v.editing = editKey
		v.input = p.APIKey
	case rowBaseURL:
		v.editing = editURL
		v.input = p.BaseURL
	case rowModel:
		v.editing = editModel
		v.input = p.Model
	case rowLanguage:
		v.editing = editLanguage
		v.input = p.Language
	case rowRuntimePort:
		v.editing = editPort
		v.input = strconv.Itoa(v.runtimePort())
	case rowMicrophone:
		if len(v.devices) > 0 {
			v.deviceListOpen = true
			v.deviceIndex = 0
			for i, d := range v.devices {
				if d.DeviceID == v.settings.SelectedDevice {
					v.deviceIndex = i
					break
				}
			}
		}
	case rowAdvancedToggle:
		v.advancedOpen = !v.advancedOpen
		v.clampCursor()
	case rowAutoStart:
		v.settings.Runtime.AutoStart = !v.settings.Runtime.AutoStart
		return v.saveConfiguration()
	case rowMonitor:
		return v.toggleMonitoring()
	}
	return nil
}

func (v *VoiceSettings) activatePrimaryAction() tea.Cmd {
	opt := v.currentOption()
	p := v.providerSettings()
	if opt.NeedsKey && strings.TrimSpace(p.APIKey) == "" {
		v.editing = editKey
		v.input = ""
		return nil
	}
	if opt.NeedsURL && !opt.Managed && strings.TrimSpace(p.BaseURL) == "" &&
		v.settings.Provider().BaseURL == "" {
		v.editing = editURL
		v.input = ""
		return nil
	}
	action := VoiceActionCheck
	switch {
	case v.phase == VoicePhaseFailed:
		action = VoiceActionRetry
	case opt.Managed:
		action = VoiceActionStartRuntime
	}
	if _, enabled := v.PrimaryAction(); !enabled {
		return nil
	}
	provider := opt.Provider
	managed := opt.Managed
	return func() tea.Msg {
		return VoiceRuntimeActionMsg{Action: action, Provider: provider, Managed: managed}
	}
}

func (v *VoiceSettings) selectOption(opt transcriptionOption) tea.Cmd {
	v.settings.SelectedProvider = opt.Provider
	p := v.providerSettings()
	if opt.Provider == voice.ProviderOpenAICompatible {
		p.Managed = opt.Managed
		if opt.Managed {
			p.Runtime = "nemo"
			if p.BaseURL == "" {
				p.BaseURL = voice.DefaultLocalTranscriptionURL
			}
			if p.Model == "" {
				p.Model = voice.DefaultLocalTranscriptionModel
			}
			if v.settings.Runtime.ID == "" {
				v.settings.Runtime.ID = "nemo"
			}
		} else {
			p.Runtime = ""
		}
	}
	v.setProviderSettings(p)
	v.settings.Normalize()
	v.SetRuntimePhase(VoicePhaseNotConfigured, "")
	v.clampCursor()
	return v.saveConfiguration()
}

// saveConfiguration persists via the canonical saver (0600, atomic).
func (v *VoiceSettings) saveConfiguration() tea.Cmd {
	path := v.configPath
	snapshot := v.settings
	return func() tea.Msg {
		if path == "" {
			return configSavedMsg{}
		}
		return configSavedMsg{err: voice.SaveTranscriptionSettings(path, snapshot)}
	}
}

// ---------------------------------------------------------------------------
// Monitoring (unchanged behavior; runs in commands, not View)
// ---------------------------------------------------------------------------

func (v *VoiceSettings) toggleMonitoring() tea.Cmd {
	if v.isMonitoring {
		return v.stopMonitoring()
	}
	return v.startMonitoring()
}

func (v *VoiceSettings) startMonitoring() tea.Cmd {
	if v.capture == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.monitorCancel = cancel
	if err := v.capture.StartLevelMonitoring(ctx); err != nil {
		return nil
	}
	v.isMonitoring = true
	return v.requestLevelsUpdate()
}

func (v *VoiceSettings) stopMonitoring() tea.Cmd {
	if v.capture != nil {
		_ = v.capture.StopLevelMonitoring()
	}
	if v.monitorCancel != nil {
		v.monitorCancel()
		v.monitorCancel = nil
	}
	v.isMonitoring = false
	return nil
}

func (v *VoiceSettings) requestLevelsUpdate() tea.Cmd {
	return tea.Tick(audioLevelUpdateInterval*time.Millisecond, func(t time.Time) tea.Msg {
		if v.capture != nil && v.isMonitoring {
			return audioLevelsUpdateMsg{levels: v.capture.GetAudioLevels()}
		}
		return nil
	})
}

const audioLevelUpdateInterval = 50 // ms

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View renders the transcription screen. Pure: no I/O, no runtime commands.
func (v *VoiceSettings) View(width, height int, focused bool) string {
	if width <= 0 {
		width = 60
	}
	if width < 20 {
		width = 20
	}
	if width > 100 {
		width = 100
	}

	base := lipgloss.NewStyle().Background(voiceBgColor)
	line := func(s string) string {
		return base.MaxWidth(width).Render(s)
	}

	var out []string
	title := lipgloss.NewStyle().Bold(true).Foreground(voiceTitleColor).Background(voiceBgColor)
	out = append(out, line(title.Render(i18n.T("settings.residual_final.voice.transcription"))), "")

	// Readiness/status header.
	statusStyle := lipgloss.NewStyle().Background(voiceBgColor)
	switch v.phase {
	case VoicePhaseReady, VoicePhaseRecording, VoicePhaseTranscribing:
		statusStyle = statusStyle.Foreground(voiceGoodColor)
	case VoicePhaseFailed:
		statusStyle = statusStyle.Foreground(voiceBadColor)
	case VoicePhaseChecking, VoicePhaseDownloading, VoicePhaseStarting:
		statusStyle = statusStyle.Foreground(voiceWarnColor)
	default:
		statusStyle = statusStyle.Foreground(voiceMutedColor)
	}
	out = append(out, line(statusStyle.Render(i18n.T("settings.residual_final.common.status_value", v.StatusLine()))))

	if (v.phase == VoicePhaseDownloading || v.phase == VoicePhaseStarting) && v.progressFrac > 0 {
		out = append(out, line(statusStyle.Render(v.renderProgress(width-4))))
	}
	if v.saveErr != "" {
		errStyle := lipgloss.NewStyle().Foreground(voiceBadColor).Background(voiceBgColor)
		out = append(out, line(errStyle.Render(i18n.T("settings.residual_final.voice.save_failed", v.saveErr))))
	}
	out = append(out, "")

	if v.pickerOpen {
		out = append(out, v.renderPicker(line, width)...)
	} else if v.deviceListOpen {
		out = append(out, v.renderDeviceList(line)...)
	} else {
		out = append(out, v.renderRows(line, focused)...)
	}

	if v.advancedOpen && v.isMonitoring && !v.pickerOpen && !v.deviceListOpen {
		out = append(out, "", line(v.renderLevelBar(v.audioLevels.PeakDB, "Peak", -60, 0)),
			line(v.renderLevelBar(v.audioLevels.RMSDB, "RMS ", -60, 0)))
	}

	out = append(out, "", line(v.helpText()))
	return i18n.SettingsResidualModelsText(i18n.SettingsIntegrationsText(strings.Join(out, "\n")))
}

func (v *VoiceSettings) renderRows(line func(string) string, focused bool) []string {
	rows := v.visibleRows()
	v.clampCursor()
	labelStyle := lipgloss.NewStyle().Foreground(voiceLabelColor).Background(voiceBgColor)
	valueStyle := lipgloss.NewStyle().Foreground(voiceTextColor).Background(voiceBgColor)
	selStyle := lipgloss.NewStyle().Background(voiceSelectBg)

	var out []string
	for i, row := range rows {
		prefix := "  "
		if i == v.cursor && focused {
			prefix = "> "
		}
		text := row.label
		if row.value != "" {
			text += ": "
		}
		var rendered string
		if i == v.cursor && focused {
			rendered = selStyle.Render(prefix + text + v.rowValueDisplay(row))
		} else {
			rendered = prefix + labelStyle.Render(text) + valueStyle.Render(v.rowValueDisplay(row))
		}
		out = append(out, line(rendered))
	}
	return out
}

func (v *VoiceSettings) rowValueDisplay(row voiceRow) string {
	if v.editing != editNone && v.editingRow() == row.id {
		display := v.input
		if v.editing == editKey {
			display = strings.Repeat("•", len([]rune(v.input)))
		}
		return display + "│"
	}
	return row.value
}

func (v *VoiceSettings) editingRow() voiceRowID {
	switch v.editing {
	case editKey:
		return rowAPIKey
	case editURL:
		return rowBaseURL
	case editModel:
		return rowModel
	case editLanguage:
		return rowLanguage
	case editPort:
		return rowRuntimePort
	}
	return -1
}

func (v *VoiceSettings) renderPicker(line func(string) string, width int) []string {
	header := lipgloss.NewStyle().Bold(true).Foreground(voiceAccentColor).Background(voiceBgColor)
	descStyle := lipgloss.NewStyle().Foreground(voiceMutedColor).Background(voiceBgColor)
	selStyle := lipgloss.NewStyle().Background(voiceSelectBg)
	cur := v.currentOption()

	out := []string{line(header.Render(i18n.T("settings.residual_final.voice.select_provider"))), ""}
	for i, opt := range transcriptionOptions {
		mark := "○"
		if opt.ID == cur.ID {
			mark = "●"
		}
		prefix := "  "
		name := localizedVoiceProviderName(opt)
		text := fmt.Sprintf("%s%s %s", prefix, mark, name)
		if i == v.pickerIndex {
			text = selStyle.Render("> " + mark + " " + name)
		}
		out = append(out, line(text))
		if width >= 40 {
			out = append(out, line(descStyle.Render("    "+localizedVoiceProviderDescription(opt))))
		}
	}
	return out
}

func (v *VoiceSettings) renderDeviceList(line func(string) string) []string {
	header := lipgloss.NewStyle().Bold(true).Foreground(voiceAccentColor).Background(voiceBgColor)
	selStyle := lipgloss.NewStyle().Background(voiceSelectBg)
	out := []string{line(header.Render(i18n.T("settings.residual_final.voice.select_microphone"))), ""}
	for i, dev := range v.devices {
		mark := "○"
		if dev.DeviceID == v.settings.SelectedDevice {
			mark = "●"
		}
		name := dev.Name
		if dev.IsDefault {
			name += i18n.T("settings.residual_final.common.default_suffix")
		}
		text := "  " + mark + " " + name
		if i == v.deviceIndex {
			text = selStyle.Render("> " + mark + " " + name)
		}
		out = append(out, line(text))
	}
	return out
}

func (v *VoiceSettings) renderProgress(width int) string {
	if width < 10 {
		width = 10
	}
	barWidth := width - 8
	if barWidth > 30 {
		barWidth = 30
	}
	filled := int(float64(barWidth) * v.progressFrac)
	var bar strings.Builder
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar.WriteString("█")
		} else {
			bar.WriteString("░")
		}
	}
	label := v.progressLabel
	if label != "" {
		label = " " + label
	}
	return fmt.Sprintf("%s %3.0f%%%s", bar.String(), v.progressFrac*100, label)
}

func (v *VoiceSettings) helpText() string {
	helpStyle := lipgloss.NewStyle().Foreground(voiceMutedColor).Background(voiceBgColor)
	switch {
	case v.editing != editNone:
		return helpStyle.Render(i18n.T("settings.residual_final.voice.hint.edit"))
	case v.pickerOpen || v.deviceListOpen:
		return helpStyle.Render(i18n.T("settings.residual_final.voice.hint.picker"))
	default:
		opt := v.currentOption()
		hint := i18n.T("settings.residual_final.voice.hint.main")
		if opt.EnvHint != "" {
			hint += i18n.T("settings.residual_final.voice.env_hint", opt.EnvHint)
		}
		return helpStyle.Render(hint)
	}
}

// renderLevelBar renders an audio level bar.
func (v *VoiceSettings) renderLevelBar(db float32, label string, minDB, maxDB float32) string {
	normalized := (db - minDB) / (maxDB - minDB)
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}
	percent := int(normalized * 100)

	barWidth := 30
	filled := int(float32(barWidth) * normalized)
	var bar strings.Builder
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar.WriteString("█")
		} else {
			bar.WriteString("░")
		}
	}

	barStyle := lipgloss.NewStyle().Background(voiceBgColor)
	switch {
	case percent < 60:
		barStyle = barStyle.Foreground(voiceGoodColor)
	case percent < 80:
		barStyle = barStyle.Foreground(voiceWarnColor)
	default:
		barStyle = barStyle.Foreground(voiceBadColor)
	}
	labelStyle := lipgloss.NewStyle().Foreground(voiceLabelColor).Background(voiceBgColor)
	return fmt.Sprintf("%s %s %4.0fdB", labelStyle.Render(label), barStyle.Render(bar.String()), db)
}

// ---------------------------------------------------------------------------
// Compatibility accessors (preserved public surface)
// ---------------------------------------------------------------------------

// SelectedDevice returns the currently selected device ID.
func (v *VoiceSettings) SelectedDevice() string {
	return v.settings.SelectedDevice
}

// SetSelectedDevice sets the selected device.
func (v *VoiceSettings) SetSelectedDevice(deviceID string) {
	v.settings.SelectedDevice = deviceID
	if v.capture != nil {
		_ = v.capture.SetDevice(deviceID)
	}
}

// SelectedProvider returns the currently selected provider type.
func (v *VoiceSettings) SelectedProvider() voice.ProviderType {
	return v.settings.SelectedProvider
}

// SetSelectedProvider sets the selected provider.
func (v *VoiceSettings) SetSelectedProvider(provider voice.ProviderType) {
	v.settings.SelectedProvider = provider
	v.settings.Normalize()
	v.clampCursor()
}

// APIKey returns the API key for the given provider.
func (v *VoiceSettings) APIKey(provider voice.ProviderType) string {
	return v.settings.Providers[string(provider)].APIKey
}

// SetAPIKey sets the API key for the given provider.
func (v *VoiceSettings) SetAPIKey(provider voice.ProviderType, key string) {
	if v.settings.Providers == nil {
		v.settings.Providers = make(map[string]voice.TranscriptionProviderSettings)
	}
	p := v.settings.Providers[string(provider)]
	p.APIKey = key
	v.settings.Providers[string(provider)] = p
}

// CustomURL returns the custom URL for the given provider.
func (v *VoiceSettings) CustomURL(provider voice.ProviderType) string {
	return v.settings.Providers[string(provider)].BaseURL
}

// SetCustomURL sets the custom URL for the given provider.
func (v *VoiceSettings) SetCustomURL(provider voice.ProviderType, url string) {
	if v.settings.Providers == nil {
		v.settings.Providers = make(map[string]voice.TranscriptionProviderSettings)
	}
	p := v.settings.Providers[string(provider)]
	p.BaseURL = url
	v.settings.Providers[string(provider)] = p
}

// AllAPIKeys returns all configured API keys.
func (v *VoiceSettings) AllAPIKeys() map[voice.ProviderType]string {
	out := make(map[voice.ProviderType]string)
	for id, p := range v.settings.Providers {
		if p.APIKey != "" {
			out[voice.ProviderType(id)] = p.APIKey
		}
	}
	return out
}

// AllCustomURLs returns all configured custom URLs.
func (v *VoiceSettings) AllCustomURLs() map[voice.ProviderType]string {
	out := make(map[voice.ProviderType]string)
	for id, p := range v.settings.Providers {
		if p.BaseURL != "" {
			out[voice.ProviderType(id)] = p.BaseURL
		}
	}
	return out
}

// TranscriptionSettings returns a copy of the canonical settings.
func (v *VoiceSettings) TranscriptionSettings() voice.TranscriptionSettings {
	return v.settings
}

// Close cleans up resources.
func (v *VoiceSettings) Close() {
	v.stopMonitoring()
}
