package catalogmerge

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
)

func boolPtr(value bool) *bool {
	v := value
	return &v
}

func TestMergeProvidersFromCatalogFillsMissingFields(t *testing.T) {
	local := []ProviderUIConfig{
		{
			ID:          "openai",
			Name:        "OpenAI",
			DisplayName: "",
			Source:      "local",
			Models: []ModelUIConfig{
				{ID: "gpt-4o"},
			},
		},
	}

	catalog := &cloud.CatalogDocument{
		Providers: []cloud.CatalogProvider{
			{
				ID:          "openai",
				Name:        "OpenAI",
				DisplayName: "OpenAI (OAuth)",
				Color:       "#19C37D",
				Type:        "oauth",
				APIType:     "openai",
				BaseURL:     "https://api.openai.com/v1",
				Models: []cloud.CatalogModel{
					{
						ID:                      "gpt-4o",
						DisplayName:             "GPT-4o",
						Context:                 "128K",
						ContextWindow:           128000,
						Description:             "Model",
						SupportsReasoningEffort: boolPtr(true),
						ReasoningEfforts:        []string{"low", "medium", "high"},
					},
				},
			},
		},
	}

	merged, changed := MergeProvidersFromCatalog(local, catalog)
	if !changed {
		t.Fatalf("expected changes to be detected")
	}
	if len(merged) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(merged))
	}

	provider := merged[0]
	if provider.DisplayName != "OpenAI (OAuth)" {
		t.Fatalf("expected displayName from catalog, got %q", provider.DisplayName)
	}
	if provider.Color != "#19C37D" || provider.Type != "oauth" || provider.APIType != "openai" {
		t.Fatalf("expected catalog fields to be applied, got color=%q type=%q apiType=%q", provider.Color, provider.Type, provider.APIType)
	}
	if provider.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("expected baseUrl from catalog, got %q", provider.BaseURL)
	}
	if len(provider.Models) != 1 || provider.Models[0].DisplayName != "GPT-4o" {
		t.Fatalf("expected catalog model details to be applied")
	}
	if provider.Models[0].SupportsReasoningEffort == nil || !*provider.Models[0].SupportsReasoningEffort {
		t.Fatalf("expected reasoning support metadata to be applied from catalog")
	}
	if len(provider.Models[0].ReasoningEfforts) != 3 {
		t.Fatalf("expected reasoning efforts from catalog, got %v", provider.Models[0].ReasoningEfforts)
	}
}

func TestMergeProvidersFromCatalogAppendsLocalModels(t *testing.T) {
	local := []ProviderUIConfig{
		{
			ID:     "openai",
			Name:   "OpenAI",
			Source: "local",
			Models: []ModelUIConfig{
				{ID: "local-only", DisplayName: "Local Only"},
			},
		},
	}

	catalog := &cloud.CatalogDocument{
		Providers: []cloud.CatalogProvider{
			{
				ID:   "openai",
				Name: "OpenAI",
				Models: []cloud.CatalogModel{
					{ID: "gpt-4o"},
				},
			},
		},
	}

	merged, changed := MergeProvidersFromCatalog(local, catalog)
	if !changed {
		t.Fatalf("expected changes to be detected")
	}
	if len(merged) != 1 || len(merged[0].Models) != 2 {
		t.Fatalf("expected merged models to include catalog and local entries")
	}
	if merged[0].Models[0].ID != "gpt-4o" || merged[0].Models[1].ID != "local-only" {
		t.Fatalf("expected catalog models first, local models appended")
	}
}

func TestMergeProvidersFromCatalog_PreservesLocalReasoningOverrides(t *testing.T) {
	local := []ProviderUIConfig{
		{
			ID:     "openai",
			Name:   "OpenAI",
			Source: "local",
			Models: []ModelUIConfig{
				{
					ID:                      "gpt-5.1",
					DisplayName:             "GPT-5.1",
					SupportsReasoningEffort: boolPtr(false),
				},
			},
		},
	}

	catalog := &cloud.CatalogDocument{
		Providers: []cloud.CatalogProvider{
			{
				ID:   "openai",
				Name: "OpenAI",
				Models: []cloud.CatalogModel{
					{
						ID:                      "gpt-5.1",
						DisplayName:             "GPT-5.1",
						SupportsReasoningEffort: boolPtr(true),
						ReasoningEfforts:        []string{"minimal", "medium", "high"},
					},
				},
			},
		},
	}

	merged, changed := MergeProvidersFromCatalog(local, catalog)
	if !changed {
		t.Fatalf("expected changes to be detected")
	}
	if len(merged) != 1 || len(merged[0].Models) != 1 {
		t.Fatalf("expected merged provider/model output")
	}

	model := merged[0].Models[0]
	if model.SupportsReasoningEffort == nil || *model.SupportsReasoningEffort {
		t.Fatalf("expected local supports_reasoning_effort=false override to win")
	}
	if model.ReasoningEfforts != nil {
		t.Fatalf("expected no local reasoning_efforts override, got %v", model.ReasoningEfforts)
	}
}

func TestMergeProvidersFromCatalogSkipsEmptyIDs(t *testing.T) {
	local := []ProviderUIConfig{
		{
			ID:     "",
			Name:   "Invalid",
			Source: "local",
			Models: []ModelUIConfig{{ID: "local"}},
		},
		{
			ID:     "openai",
			Name:   "OpenAI",
			Source: "local",
			Models: []ModelUIConfig{{ID: ""}},
		},
	}

	catalog := &cloud.CatalogDocument{
		Providers: []cloud.CatalogProvider{
			{
				ID:   "",
				Name: "Invalid",
				Models: []cloud.CatalogModel{
					{ID: "ignored"},
				},
			},
			{
				ID:   "openai",
				Name: "OpenAI",
				Models: []cloud.CatalogModel{
					{ID: ""},
				},
			},
		},
	}

	merged, _ := MergeProvidersFromCatalog(local, catalog)
	if len(merged) != 1 {
		t.Fatalf("expected only valid providers to be merged, got %d", len(merged))
	}
	if len(merged[0].Models) != 0 {
		t.Fatalf("expected empty model IDs to be skipped")
	}
}
