package commands

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/catalogmerge"
)

func formatContextWindow(tokens int) string {
	return catalogmerge.FormatContextWindow(tokens)
}

func catalogProviderName(provider cloud.CatalogProvider) string {
	if provider.Name != "" {
		return provider.Name
	}
	if provider.ID != "" {
		return provider.ID
	}
	return provider.DisplayName
}
