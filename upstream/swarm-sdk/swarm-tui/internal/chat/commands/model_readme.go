package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ModelReadmeEntry stores the resolved description/readme summary for a model.
type ModelReadmeEntry struct {
	Text    string
	Source  string
	Err     string
	Loading bool
}

// ModelReadmeMsg is sent when a readme fetch completes.
type ModelReadmeMsg struct {
	Key          string
	ProviderName string
	ModelID      string
	Text         string
	Source       string
	Err          string
}

// ModelReadmeKey returns a stable cache key for a provider/model pair.
func ModelReadmeKey(providerName string, modelID string) string {
	var normalizedProvider string = strings.ToLower(strings.TrimSpace(providerName))
	return normalizedProvider + ":" + modelID
}

// FetchModelReadmeCmd returns a Bubble Tea command that resolves a model description.
func FetchModelReadmeCmd(provider Provider, model ModelInfo) tea.Cmd {
	var providerName string = provider.Name
	var modelID string = model.ID
	var description string = strings.TrimSpace(model.Description)

	return func() tea.Msg {
		var msg ModelReadmeMsg
		msg.Key = ModelReadmeKey(providerName, modelID)
		msg.ProviderName = providerName
		msg.ModelID = modelID

		if description == "" {
			msg.Err = i18n.T("commands_b.model.description_not_available")
			return msg
		}

		msg.Text = normalizeSummary(description)
		msg.Source = readmeSourceLabel(provider)
		return msg
	}
}

func readmeSourceLabel(provider Provider) string {
	var source string = strings.ToLower(strings.TrimSpace(provider.Source))
	switch source {
	case "cloud":
		return i18n.T("commands_b.model.source_cloud_catalog")
	case "local", "user":
		return i18n.T("commands_b.model.source_config")
	default:
		if strings.EqualFold(provider.Name, "openrouter") {
			return "OpenRouter"
		}
		return i18n.T("commands_b.model.source_config")
	}
}

func normalizeSummary(text string) string {
	var cleaned string = strings.Join(strings.Fields(text), " ")
	if len(cleaned) > 900 {
		cleaned = cleaned[:897] + "..."
	}
	return cleaned
}
