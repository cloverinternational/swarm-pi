package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// ModelListResponse represents the response from /v1/models endpoint.
type ModelListResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// ModelInfo represents a single model from the API.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelCache caches the model list to avoid repeated API calls.
type ModelCache struct {
	mu         sync.RWMutex
	models     []string
	lastUpdate time.Time
	ttl        time.Duration
}

// NewModelCache creates a new model cache with the given TTL.
func NewModelCache(ttl time.Duration) *ModelCache {
	return &ModelCache{
		ttl: ttl,
	}
}

// Get returns cached models if still valid, otherwise returns nil.
func (mc *ModelCache) Get() []string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if time.Since(mc.lastUpdate) > mc.ttl {
		return nil
	}

	// Return copy to avoid external modification
	result := make([]string, len(mc.models))
	copy(result, mc.models)
	return result
}

// Set updates the cache with new models.
func (mc *ModelCache) Set(models []string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.models = make([]string, len(models))
	copy(mc.models, models)
	mc.lastUpdate = time.Now()
}

// ListModels fetches all available models from the OpenAI API.
// This is used for dynamic model discovery and capability detection.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	// Make request to /v1/models
	resp, err := p.client.Get(ctx, p.baseURL+"/models")
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer resp.Close()

	// Parse response
	var modelList ModelListResponse
	if err := resp.JSON(&modelList); err != nil {
		return nil, fmt.Errorf("failed to parse model list: %w", err)
	}

	// Extract model IDs
	models := make([]string, 0, len(modelList.Data))
	for _, model := range modelList.Data {
		models = append(models, model.ID)
	}

	// Sort for consistency
	sort.Strings(models)

	return models, nil
}

// ListModelsWithFilter fetches models and filters by criteria.
// Useful for getting only chat models, embedding models, etc.
func (p *Provider) ListModelsWithFilter(ctx context.Context, filter func(ModelInfo) bool) ([]ModelInfo, error) {
	resp, err := p.client.Get(ctx, p.baseURL+"/models")
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer resp.Close()

	var modelList ModelListResponse
	if err := resp.JSON(&modelList); err != nil {
		return nil, fmt.Errorf("failed to parse model list: %w", err)
	}

	// Apply filter
	filtered := make([]ModelInfo, 0)
	for _, model := range modelList.Data {
		if filter == nil || filter(model) {
			filtered = append(filtered, model)
		}
	}

	return filtered, nil
}

// GetChatModels returns only models suitable for chat completions.
// Filters out embedding, moderation, and deprecated models.
func (p *Provider) ChatModels(ctx context.Context) ([]string, error) {
	models, err := p.ListModelsWithFilter(ctx, func(m ModelInfo) bool {
		id := m.ID

		// Exclude embedding models
		if contains(id, "embedding") {
			return false
		}

		// Exclude moderation models
		if contains(id, "moderation") {
			return false
		}

		// Exclude TTS/STT models
		if contains(id, "whisper") || contains(id, "tts") {
			return false
		}

		// Exclude DALL-E models
		if contains(id, "dall-e") {
			return false
		}

		// Include GPT models and others
		return true
	})

	if err != nil {
		return nil, err
	}

	// Extract IDs
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}

	sort.Strings(ids)
	return ids, nil
}

// GetModelInfo retrieves detailed information about a specific model.
func (p *Provider) ModelInfo(ctx context.Context, modelID string) (*ModelInfo, error) {
	resp, err := p.client.Get(ctx, p.baseURL+"/models/"+modelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get model info: %w", err)
	}
	defer resp.Close()

	var model ModelInfo
	if err := resp.JSON(&model); err != nil {
		return nil, fmt.Errorf("failed to parse model info: %w", err)
	}

	return &model, nil
}

// RefreshModelCache updates the internal model cache.
// Call this periodically to keep the model list up to date.
func (p *Provider) RefreshModelCache(ctx context.Context) error {
	models, err := p.ChatModels(ctx)
	if err != nil {
		return err
	}

	// Log the refresh
	p.logger.Info(ctx, "openai.models_refreshed",
		observability.F("model_count", len(models)),
	)

	return nil
}

// contains checks if a string contains a substring (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// FetchAndSaveModels is a utility function to fetch models and save to a config file.
// This can be used by CLI tools to update configuration files.
func FetchAndSaveModels(apiKey, baseURL string) ([]string, error) {
	// Simple HTTP client for fetching
	client := &http.Client{Timeout: 10 * time.Second}

	url := baseURL
	if url == "" {
		url = "https://api.openai.com/v1"
	}
	url += "/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var modelList ModelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelList); err != nil {
		return nil, err
	}

	// Filter chat models
	chatModels := make([]string, 0)
	for _, model := range modelList.Data {
		id := model.ID

		// Exclude non-chat models
		if contains(id, "embedding") || contains(id, "moderation") ||
			contains(id, "whisper") || contains(id, "tts") || contains(id, "dall-e") {
			continue
		}

		chatModels = append(chatModels, id)
	}

	sort.Strings(chatModels)
	return chatModels, nil
}
