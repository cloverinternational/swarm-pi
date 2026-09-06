package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	CurrentTranscriptionSettingsVersion = 2
	DefaultLocalTranscriptionURL        = "http://127.0.0.1:8001"
	DefaultLocalTranscriptionModel      = "nvidia/parakeet-tdt-0.6b-v3"
)

// TranscriptionProviderSettings describes one configured speech-to-text provider.
// APIKey remains for backwards compatibility with voice_config.json; callers should
// prefer CredentialID when a credential store is available.
type TranscriptionProviderSettings struct {
	BaseURL      string `json:"base_url,omitempty"`
	Model        string `json:"model,omitempty"`
	Language     string `json:"language,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	Managed      bool   `json:"managed,omitempty"`
}

// TranscriptionRuntimeSettings controls an optional locally managed runtime.
type TranscriptionRuntimeSettings struct {
	ID        string `json:"id,omitempty"`
	AutoStart bool   `json:"auto_start,omitempty"`
	Port      int    `json:"port,omitempty"`
	DataDir   string `json:"data_dir,omitempty"`
}

// TranscriptionSettings is the canonical configuration shared by SDK startup
// and the TUI settings screen.
type TranscriptionSettings struct {
	Version          int                                      `json:"version"`
	SelectedProvider ProviderType                             `json:"selected_provider"`
	Providers        map[string]TranscriptionProviderSettings `json:"providers,omitempty"`
	SelectedDevice   string                                   `json:"selected_device,omitempty"`
	Runtime          TranscriptionRuntimeSettings             `json:"runtime,omitempty"`
	LastUpdated      string                                   `json:"last_updated,omitempty"`

	extra map[string]json.RawMessage
}

// DefaultTranscriptionSettings returns a usable local-first configuration.
func DefaultTranscriptionSettings() TranscriptionSettings {
	return TranscriptionSettings{
		Version:          CurrentTranscriptionSettingsVersion,
		SelectedProvider: ProviderOpenAICompatible,
		Providers: map[string]TranscriptionProviderSettings{
			string(ProviderOpenAICompatible): {
				BaseURL:  DefaultLocalTranscriptionURL,
				Model:    DefaultLocalTranscriptionModel,
				Language: DefaultLanguage,
				Runtime:  "nemo",
				Managed:  true,
			},
		},
		Runtime: TranscriptionRuntimeSettings{ID: "nemo", Port: 8001},
	}
}

// Provider returns the selected provider's normalized settings.
func (s TranscriptionSettings) Provider() TranscriptionProviderSettings {
	p := s.Providers[string(s.SelectedProvider)]
	if p.Language == "" {
		p.Language = DefaultLanguage
	}
	if s.SelectedProvider == ProviderOpenAICompatible {
		if p.BaseURL == "" {
			p.BaseURL = DefaultLocalTranscriptionURL
		}
		if p.Model == "" {
			p.Model = DefaultLocalTranscriptionModel
		}
	}
	return p
}

// Normalize fills defaults while preserving configured providers.
func (s *TranscriptionSettings) Normalize() {
	if s.Version == 0 {
		s.Version = CurrentTranscriptionSettingsVersion
	}
	if s.Providers == nil {
		s.Providers = make(map[string]TranscriptionProviderSettings)
	}
	if s.SelectedProvider == "" {
		s.SelectedProvider = ProviderOpenAICompatible
	}
	p := s.Provider()
	if s.Runtime.ID == "nemo" && s.Runtime.Port == 0 {
		s.Runtime.Port = 8001
	}
	if p.Managed && p.Runtime == "nemo" && s.Runtime.Port > 0 {
		p.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", s.Runtime.Port)
	}
	s.Providers[string(s.SelectedProvider)] = p
}

// UnmarshalJSON migrates the original APIKeys/CustomURLs shape and keeps
// unrecognized top-level fields so a load/save round-trip is non-destructive.
func (s *TranscriptionSettings) UnmarshalJSON(data []byte) error {
	type alias TranscriptionSettings
	var current alias
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	var legacy struct {
		APIKeys    map[string]string `json:"api_keys"`
		CustomURLs map[string]string `json:"custom_urls"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	*s = TranscriptionSettings(current)
	if s.Providers == nil {
		s.Providers = make(map[string]TranscriptionProviderSettings)
	}
	for id, key := range legacy.APIKeys {
		p := s.Providers[id]
		p.APIKey = key
		s.Providers[id] = p
	}
	for id, baseURL := range legacy.CustomURLs {
		p := s.Providers[id]
		p.BaseURL = baseURL
		s.Providers[id] = p
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{"version", "selected_provider", "providers", "selected_device", "runtime", "last_updated", "api_keys", "custom_urls"} {
		delete(raw, key)
	}
	s.extra = raw
	s.Normalize()
	return nil
}

// MarshalJSON preserves unrecognized fields loaded from older/newer clients.
func (s TranscriptionSettings) MarshalJSON() ([]byte, error) {
	type alias TranscriptionSettings
	known, err := json.Marshal(alias(s))
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(known, &merged); err != nil {
		return nil, err
	}
	for key, value := range s.extra {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	return json.Marshal(merged)
}

func LoadTranscriptionSettings(path string) (TranscriptionSettings, error) {
	settings := DefaultTranscriptionSettings()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return TranscriptionSettings{}, fmt.Errorf("read transcription settings: %w", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return TranscriptionSettings{}, fmt.Errorf("parse transcription settings: %w", err)
	}
	settings.Normalize()
	return settings, nil
}

func SaveTranscriptionSettings(path string, settings TranscriptionSettings) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("transcription settings path is required")
	}
	settings.Version = CurrentTranscriptionSettingsVersion
	settings.LastUpdated = time.Now().UTC().Format(time.RFC3339)
	settings.Normalize()
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transcription settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create transcription settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".voice-config-*")
	if err != nil {
		return fmt.Errorf("create transcription settings temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure transcription settings temp file: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write transcription settings: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync transcription settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close transcription settings: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace transcription settings: %w", err)
	}
	return nil
}
