package cloudsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

type valueKind int

const (
	kindString valueKind = iota
	kindBool
	kindInt
	kindFloat
)

var settingsAllowlist = map[string]valueKind{
	"defaultProvider":        kindString,
	"defaultModel":           kindString,
	"defaultMode":            kindString,
	"theme":                  kindString,
	"showThinking":           kindBool,
	"showTokenCount":         kindBool,
	"showToolOutput":         kindBool,
	"compactMode":            kindBool,
	"maxOutputLines":         kindInt,
	"syntaxHighlighting":     kindBool,
	"autoSaveConversations":  kindBool,
	"confirmBeforeExit":      kindBool,
	"enableCompaction":       kindBool,
	"compactionThreshold":    kindInt,
	"preserveRecentMessages": kindInt,
	"maxConcurrentTools":     kindInt,
	"toolTimeout":            kindInt,
	"enableCache":            kindBool,
	"cacheMaxSizeMB":         kindInt,
	"cacheExpiryDays":        kindInt,
}

var renderAllowlist = map[string]valueKind{
	"theme":                 kindString,
	"syntaxTheme":           kindString,
	"showLineNumbers":       kindBool,
	"showGutter":            kindBool,
	"wordWrap":              kindBool,
	"animateStreaming":      kindBool,
	"showTimestamps":        kindBool,
	"messageSpacing":        kindInt,
	"collapsedToolsDefault": kindBool,
}

var profileAllowlist = map[string]valueKind{
	"name":         kindString,
	"description":  kindString,
	"systemPrompt": kindString,
	"temperature":  kindFloat,
	"maxTokens":    kindInt,
}

type SettingsPayload struct {
	Raw            map[string]any
	Config         map[string]any
	RenderSettings map[string]any
}

type ProfilesPayload struct {
	ActiveProfile string
	Profiles      []core.AgentProfile
}

func BuildSettingsPayload(cfg *core.Config, render *core.RenderSettings) SettingsPayload {
	payload := SettingsPayload{
		Config: map[string]any{
			"defaultProvider":        cfg.DefaultProvider,
			"defaultModel":           cfg.DefaultModel,
			"defaultMode":            cfg.DefaultMode,
			"theme":                  cfg.Theme,
			"showThinking":           cfg.ShowThinking,
			"showTokenCount":         cfg.ShowTokenCount,
			"showToolOutput":         cfg.ShowToolOutput,
			"compactMode":            cfg.CompactMode,
			"maxOutputLines":         cfg.MaxOutputLines,
			"syntaxHighlighting":     cfg.SyntaxHighlighting,
			"autoSaveConversations":  cfg.AutoSaveConversations,
			"confirmBeforeExit":      cfg.ConfirmBeforeExit,
			"enableCompaction":       cfg.EnableCompaction,
			"compactionThreshold":    cfg.CompactionThreshold,
			"preserveRecentMessages": cfg.PreserveRecentMessages,
			"maxConcurrentTools":     cfg.MaxConcurrentTools,
			"toolTimeout":            cfg.ToolTimeout,
			"enableCache":            cfg.EnableCache,
			"cacheMaxSizeMB":         cfg.CacheMaxSizeMB,
			"cacheExpiryDays":        cfg.CacheExpiryDays,
		},
		RenderSettings: map[string]any{
			"theme":                 render.Theme,
			"syntaxTheme":           render.SyntaxTheme,
			"showLineNumbers":       render.ShowLineNumbers,
			"showGutter":            render.ShowGutter,
			"wordWrap":              render.WordWrap,
			"animateStreaming":      render.AnimateStreaming,
			"showTimestamps":        render.ShowTimestamps,
			"messageSpacing":        render.MessageSpacing,
			"collapsedToolsDefault": render.CollapsedToolsDefault,
		},
	}
	payload.Raw = map[string]any{
		"config":         payload.Config,
		"renderSettings": payload.RenderSettings,
	}
	return payload
}

func BuildProfilesPayload(profiles []core.AgentProfile, activeProfile string) ProfilesPayload {
	filtered := make([]core.AgentProfile, 0, len(profiles))
	for _, profile := range profiles {
		filtered = append(filtered, core.AgentProfile{
			Name:         profile.Name,
			Description:  profile.Description,
			SystemPrompt: profile.SystemPrompt,
			Temperature:  profile.Temperature,
			MaxTokens:    profile.MaxTokens,
		})
	}

	return ProfilesPayload{
		ActiveProfile: activeProfile,
		Profiles:      filtered,
	}
}

func ParseSettingsPayload(raw json.RawMessage) (*SettingsPayload, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}

	payloadMap, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("settings payload must be object")
	}

	result := &SettingsPayload{
		Raw:            map[string]any{},
		Config:         map[string]any{},
		RenderSettings: map[string]any{},
	}

	for key, rawValue := range payloadMap {
		switch key {
		case "config":
			section, err := validateSection(rawValue, settingsAllowlist)
			if err != nil {
				return nil, err
			}
			result.Config = section
			result.Raw[key] = section
		case "renderSettings":
			section, err := validateSection(rawValue, renderAllowlist)
			if err != nil {
				return nil, err
			}
			result.RenderSettings = section
			result.Raw[key] = section
		default:
			return nil, fmt.Errorf("unexpected key %s", key)
		}
	}

	return result, nil
}

func ParseProfilesPayload(raw json.RawMessage) (*ProfilesPayload, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}

	payloadMap, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("profiles payload must be object")
	}

	result := &ProfilesPayload{}
	for key, rawValue := range payloadMap {
		switch key {
		case "activeProfile":
			active, ok := rawValue.(string)
			if !ok {
				return nil, errors.New("activeProfile must be string")
			}
			result.ActiveProfile = active
		case "profiles":
			profiles, ok := rawValue.([]any)
			if !ok {
				return nil, errors.New("profiles must be array")
			}
			for _, item := range profiles {
				profileMap, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("profile entry must be object")
				}
				profile, err := parseProfile(profileMap)
				if err != nil {
					return nil, err
				}
				result.Profiles = append(result.Profiles, profile)
			}
		default:
			return nil, fmt.Errorf("unexpected key %s", key)
		}
	}

	if result.Profiles == nil {
		return nil, errors.New("profiles required")
	}

	return result, nil
}

func parseProfile(profileMap map[string]any) (core.AgentProfile, error) {
	result := core.AgentProfile{}
	for field, value := range profileMap {
		kind, ok := profileAllowlist[field]
		if !ok {
			return result, errors.New("profile contains unknown field")
		}
		parsed, err := parseValue(kind, value)
		if err != nil {
			return result, err
		}
		switch field {
		case "name":
			result.Name = parsed.(string)
		case "description":
			result.Description = parsed.(string)
		case "systemPrompt":
			result.SystemPrompt = parsed.(string)
		case "temperature":
			result.Temperature = parsed.(float64)
		case "maxTokens":
			result.MaxTokens = int(parsed.(int64))
		}
	}

	if strings.TrimSpace(result.Name) == "" {
		return result, errors.New("profile name required")
	}

	return result, nil
}

func validateSection(raw any, allowlist map[string]valueKind) (map[string]any, error) {
	sectionMap, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("section must be object")
	}

	result := map[string]any{}
	for key, value := range sectionMap {
		kind, ok := allowlist[key]
		if !ok {
			return nil, errors.New("unknown key")
		}
		parsed, err := parseValue(kind, value)
		if err != nil {
			return nil, err
		}
		result[key] = parsed
	}

	return result, nil
}

func parseValue(kind valueKind, value any) (any, error) {
	switch kind {
	case kindString:
		stringValue, ok := value.(string)
		if !ok {
			return nil, errors.New("expected string")
		}
		return stringValue, nil
	case kindBool:
		boolValue, ok := value.(bool)
		if !ok {
			return nil, errors.New("expected bool")
		}
		return boolValue, nil
	case kindInt:
		intValue, ok := parseInt(value)
		if !ok {
			return nil, errors.New("expected int")
		}
		return intValue, nil
	case kindFloat:
		floatValue, ok := parseFloat(value)
		if !ok {
			return nil, errors.New("expected float")
		}
		return floatValue, nil
	default:
		return nil, errors.New("unsupported value kind")
	}
}

func parseInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case float64:
		if typed != float64(int64(typed)) {
			return 0, false
		}
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

func parseFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func decodeJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("unexpected data")
	}

	return value, nil
}
