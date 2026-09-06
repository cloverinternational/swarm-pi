package models

import (
	"slices"
	"testing"
)

func TestFuzzyModelMatch(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOk bool
	}{
		// Direct canonical matches
		{
			name:   "exact canonical match",
			input:  "claude-3-5-sonnet-20241022",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},
		{
			name:   "exact canonical match with spaces",
			input:  "  claude-3-5-sonnet-20241022  ",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},

		// Simple aliases
		{
			name:   "sonnet alias",
			input:  "sonnet",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},
		{
			name:   "haiku alias",
			input:  "haiku",
			want:   "claude-3-5-haiku-20241022",
			wantOk: true,
		},
		{
			name:   "opus alias",
			input:  "opus",
			want:   "claude-3-opus-20240229",
			wantOk: true,
		},

		// Variations with spaces and dots
		{
			name:   "claude 3.5",
			input:  "claude 3.5",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},
		{
			name:   "claude-3.5",
			input:  "claude-3.5",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},
		{
			name:   "3.5 sonnet",
			input:  "3.5 sonnet",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},

		// Case insensitive
		{
			name:   "uppercase sonnet",
			input:  "SONNET",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},
		{
			name:   "mixed case",
			input:  "Claude 3.5",
			want:   "claude-3-5-sonnet-20241022",
			wantOk: true,
		},

		// GPT models
		{
			name:   "gpt4",
			input:  "gpt4",
			want:   "gpt-4-turbo-preview",
			wantOk: true,
		},
		{
			name:   "gpt 4",
			input:  "gpt 4",
			want:   "gpt-4-turbo-preview",
			wantOk: true,
		},
		{
			name:   "chatgpt",
			input:  "chatgpt",
			want:   "gpt-3.5-turbo",
			wantOk: true,
		},

		// No matches (patterns removed to avoid false matches)
		{
			name:   "pattern removed - sonnet 3.5 in sentence",
			input:  "i want sonnet 3.5 please",
			want:   "",
			wantOk: false,
		},
		{
			name:   "pattern removed - 3.5-haiku in sentence",
			input:  "use 3.5-haiku model",
			want:   "",
			wantOk: false,
		},

		// No matches
		{
			name:   "invalid model",
			input:  "claude-4-6-sonnet",
			want:   "",
			wantOk: false,
		},
		{
			name:   "empty string",
			input:  "",
			want:   "",
			wantOk: false,
		},
		{
			name:   "gibberish",
			input:  "xyz123",
			want:   "",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FuzzyModelMatch(tt.input)
			if ok != tt.wantOk {
				t.Errorf("FuzzyModelMatch() ok = %v, wantOk %v", ok, tt.wantOk)
			}
			if got != tt.want {
				t.Errorf("FuzzyModelMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsValidModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{
			name:  "valid claude model",
			model: "claude-3-5-sonnet-20241022",
			want:  true,
		},
		{
			name:  "valid gpt model",
			model: "gpt-4-turbo-preview",
			want:  true,
		},
		{
			name:  "invalid model",
			model: "claude-4-6-sonnet",
			want:  false,
		},
		{
			name:  "alias not valid as canonical",
			model: "sonnet",
			want:  false,
		},
		{
			name:  "empty string",
			model: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidModel(tt.model); got != tt.want {
				t.Errorf("IsValidModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetModelAliases(t *testing.T) {
	// Test getting aliases for claude-3-5-sonnet-20241022
	aliases := GetModelAliases("claude-3-5-sonnet-20241022")

	// Should have multiple aliases
	if len(aliases) < 5 {
		t.Errorf("Expected at least 5 aliases for claude-3-5-sonnet-20241022, got %d", len(aliases))
	}

	// Check some expected aliases are present
	expectedAliases := []string{"sonnet", "claude 3.5", "3.5 sonnet"}
	for _, expected := range expectedAliases {
		found := slices.Contains(aliases, expected)
		if !found {
			t.Errorf("Expected alias %q not found in aliases", expected)
		}
	}

	// Test model with no aliases
	noAliases := GetModelAliases("nonexistent-model")
	if len(noAliases) != 0 {
		t.Errorf("Expected no aliases for nonexistent model, got %d", len(noAliases))
	}
}

func TestGetAvailableModels(t *testing.T) {
	models := GetAvailableModels()

	// Should have a reasonable number of models
	if len(models) < 10 {
		t.Errorf("Expected at least 10 available models, got %d", len(models))
	}

	// Check some expected models are present
	expectedModels := []string{
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"gpt-4-turbo-preview",
	}

	for _, expected := range expectedModels {
		found := slices.Contains(models, expected)
		if !found {
			t.Errorf("Expected model %q not found in available models", expected)
		}
	}
}

func TestFuzzyModelMatchWithDetails(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantModel       string
		wantConfidence  string
		wantSuggestions bool
	}{
		{"exact match", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20241022", "exact", false},
		{"alias match", "sonnet", "claude-3-5-sonnet-20241022", "alias", false},
		{"no match with suggestions", "claude-99", "", "none", true},
		{"claude family", "claude something", "", "none", true},
		{"gpt family", "gpt something", "", "none", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FuzzyModelMatchWithDetails(tt.input)

			if result.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", result.Model, tt.wantModel)
			}
			if result.Confidence != tt.wantConfidence {
				t.Errorf("Confidence = %q, want %q", result.Confidence, tt.wantConfidence)
			}
			if tt.wantSuggestions && len(result.Suggestions) == 0 {
				t.Error("Expected suggestions but got none")
			}
			if !tt.wantSuggestions && len(result.Suggestions) > 0 {
				t.Errorf("Expected no suggestions but got %v", result.Suggestions)
			}
		})
	}
}

func TestValidateModelMatch(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matched string
		wantErr bool
	}{
		{"valid alias", "sonnet", "claude-3-5-sonnet-20241022", false},
		{"exact match", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20241022", false},
		{"invalid model", "claude-99", "", true},
		{"empty input", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateModelMatch(tt.input, tt.matched)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateModelMatch(%q, %q) error = %v, wantErr %v", tt.input, tt.matched, err, tt.wantErr)
			}
		})
	}
}
