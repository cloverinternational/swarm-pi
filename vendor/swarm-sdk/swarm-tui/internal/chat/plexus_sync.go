package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// syncPlexusCatalog refreshes the aliases visible to the connected Plexus key.
// It intentionally preserves the last known catalog on transient errors.
func syncPlexusCatalog(ctx context.Context) error {
	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}

	index := -1
	for i := range providers {
		if strings.EqualFold(providers[i].Name, "plexus") {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}

	auth, err := getOpenAICompatibleAuth("plexus")
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(auth.baseURL), "/")
	if endpoint == "" {
		endpoint = strings.TrimRight(strings.TrimSpace(providers[index].BaseURL), "/")
	}
	if endpoint == "" {
		return fmt.Errorf("Plexus gateway URL is not configured")
	}

	models, err := commands.FetchModelsForProvider(ctx, "openai-compatible", endpoint, auth.apiKey)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return fmt.Errorf("Plexus returned no authorized aliases")
	}

	zero := 0
	providers[index].Models = models
	providers[index].Available = true
	providers[index].HTTPMaxRetries = &zero
	return cm.SaveProviders(providers)
}
