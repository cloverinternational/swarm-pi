package agent

import (
	"maps"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/google/uuid"
)

func agentDescriptorForA2A(def *Definition) a2a.AgentDescriptor {
	if def == nil {
		return a2a.AgentDescriptor{}
	}
	descriptor := a2a.AgentDescriptor{
		ID:                 def.ID,
		Name:               firstNonEmpty(def.Name, def.ID),
		Description:        def.Description,
		ProviderName:       def.Provider,
		Version:            "0.1.0",
		ToolHints:          append([]string(nil), def.ToolHints...),
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain", "application/json"},
		DocumentationURL:   stringMetadata(def.Metadata, "documentation_url"),
		IconURL:            stringMetadata(def.Metadata, "icon_url"),
		Metadata:           cloneAnyMap(def.Metadata),
	}
	if def.Capabilities != nil {
		descriptor.SupportsStreaming = def.Capabilities.SupportsStreaming
	}
	return descriptor
}

func stringMetadata(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	maps.Copy(dst, src)
	return dst
}

func ensureMessageID(id string) string {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return trimmed
	}
	return uuid.NewString()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
