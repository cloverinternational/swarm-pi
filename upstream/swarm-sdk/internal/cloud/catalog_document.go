package cloud

import (
	"encoding/json"
	"fmt"
)

// CatalogDocument represents the catalog payload returned by the cloud API.
type CatalogDocument struct {
	Version   string            `json:"version"`
	Providers []CatalogProvider `json:"providers"`
	Aliases   []CatalogAlias    `json:"aliases,omitempty"`
}

// CatalogProvider describes a provider entry in the catalog payload.
type CatalogProvider struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Color       string         `json:"color"`
	Type        string         `json:"type"`
	APIType     string         `json:"api_type"`
	BaseURL     string         `json:"base_url"`
	Models      []CatalogModel `json:"models"`
}

// CatalogModel describes a model entry in the catalog payload.
type CatalogModel struct {
	ID                      string   `json:"id"`
	DisplayName             string   `json:"display_name"`
	Context                 string   `json:"context"`
	ContextWindow           int      `json:"context_window"`
	Description             string   `json:"description,omitempty"`
	ReadmeKey               string   `json:"readme_key,omitempty"`
	SupportsReasoningEffort *bool    `json:"supports_reasoning_effort,omitempty"`
	ReasoningEfforts        []string `json:"reasoning_efforts,omitempty"`
}

// CatalogAlias describes an alias mapping to provider-specific models.
type CatalogAlias struct {
	Name        string                `json:"name"`
	DisplayName string                `json:"display_name,omitempty"`
	Variants    []CatalogAliasVariant `json:"variants"`
}

// CatalogAliasVariant links an alias to a provider/model pair.
type CatalogAliasVariant struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// LoadCatalogDocument reads the cached catalog body and unmarshals it.
func LoadCatalogDocument() (*CatalogDocument, *CatalogCache, error) {
	var cacheManager *CatalogCacheManager
	var err error
	cacheManager, err = NewCatalogCacheManager()
	if err != nil {
		return nil, nil, err
	}

	var cache *CatalogCache
	cache, err = cacheManager.LoadCache()
	if err != nil {
		return nil, nil, err
	}
	if cache == nil {
		return nil, nil, nil
	}
	if len(cache.Body) == 0 {
		return nil, cache, nil
	}

	var document CatalogDocument
	if err = json.Unmarshal(cache.Body, &document); err != nil {
		return nil, cache, fmt.Errorf("failed to parse catalog body: %w", err)
	}

	return &document, cache, nil
}
