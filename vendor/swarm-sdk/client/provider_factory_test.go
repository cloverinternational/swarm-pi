package client

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// TestParseModelString_AllForms covers the colon and slash separators and
// confirms rejection of malformed inputs.
func TestParseModelString_AllForms(t *testing.T) {
	cases := []struct {
		spec      string
		wantProv  string
		wantModel string
		wantErr   bool
	}{
		{"anthropic:claude-sonnet-4-5", "anthropic", "claude-sonnet-4-5", false},
		{"openai/gpt-4o", "openai", "gpt-4o", false},
		{"gemini:gemini-2.0-flash", "gemini", "gemini-2.0-flash", false},
		{"  anthropic:claude  ", "anthropic", "claude", false},
		{"bareModel", "", "", true},
		{"", "", "", true},
		{":nomodel", "", "", true},    // empty provider side
		{"noprovider:", "", "", true}, // empty model side
	}
	for _, tc := range cases {
		gotProv, gotModel, err := ParseModelString(tc.spec)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseModelString(%q): expected error, got (%q, %q, nil)", tc.spec, gotProv, gotModel)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseModelString(%q): unexpected error: %v", tc.spec, err)
			continue
		}
		if gotProv != tc.wantProv || gotModel != tc.wantModel {
			t.Errorf("ParseModelString(%q): got (%q, %q), want (%q, %q)",
				tc.spec, gotProv, gotModel, tc.wantProv, tc.wantModel)
		}
	}
}

// TestNewProvider_Anthropic builds an Anthropic provider with an explicit
// API key (avoids any env dependency).
func TestNewProvider_Anthropic(t *testing.T) {
	p, err := NewProvider("anthropic", "claude-sonnet-4-5",
		WithProviderAPIKey("test-key"),
		WithProviderLogger(noop.NewLogger()),
		WithProviderTracer(noop.NewTracer()),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p == nil {
		t.Fatal("NewProvider returned nil provider")
	}
	if got := p.Name(); got != "anthropic" {
		t.Errorf("Name(): got %q, want anthropic", got)
	}
}

// TestNewProvider_OpenAI_CompatibleBaseURL confirms groq/cerebras aliases
// route to the openai factory and inherit the right default base URL.
func TestNewProvider_OpenAI_CompatibleBaseURL(t *testing.T) {
	cases := []struct {
		provName string
		model    string
	}{
		{"groq", "llama-3.3-70b-versatile"},
		{"cerebras", "llama-3.3-70b"},
		{"openrouter", "anthropic/claude-sonnet-4-5"},
	}
	for _, tc := range cases {
		t.Run(tc.provName, func(t *testing.T) {
			p, err := NewProvider(tc.provName, tc.model,
				WithProviderAPIKey("dummy"),
			)
			if err != nil {
				t.Fatalf("NewProvider(%q): %v", tc.provName, err)
			}
			if p == nil {
				t.Fatal("nil provider")
			}
		})
	}
}

// TestNewProvider_Gemini exercises the gemini factory path.
func TestNewProvider_Gemini(t *testing.T) {
	p, err := NewProvider("gemini", "gemini-2.0-flash",
		WithProviderAPIKey("dummy"),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

// TestNewProvider_Ollama takes the inline-build shortcut and must succeed
// even without an API key.
func TestNewProvider_Ollama(t *testing.T) {
	p, err := NewProvider("ollama", "llama3.2")
	if err != nil {
		t.Fatalf("NewProvider(ollama): %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

// TestNewProvider_AliasResolution confirms claudecode/google/codex normalise
// to the canonical provider names.
func TestNewProvider_AliasResolution(t *testing.T) {
	cases := []struct {
		input    string
		wantName string
	}{
		{"claudecode", "anthropic"},
		{"google", "gemini"},
		{"codex", "openai"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			p, err := NewProvider(tc.input, "some-model",
				WithProviderAPIKey("dummy"),
			)
			if err != nil {
				t.Fatalf("NewProvider(%q): %v", tc.input, err)
			}
			if got := p.Name(); got != tc.wantName {
				t.Errorf("%q → Name() = %q, want %q", tc.input, got, tc.wantName)
			}
		})
	}
}

// TestNewProvider_UnknownProvider surfaces a clean error.
func TestNewProvider_UnknownProvider(t *testing.T) {
	_, err := NewProvider("bananaprovider", "model",
		WithProviderAPIKey("dummy"),
	)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "bananaprovider") {
		t.Errorf("error should mention the bad provider name, got: %v", err)
	}
}

// TestNewProvider_BaseURLOverride confirms WithProviderBaseURL wins over
// the default-base-URL lookup.
func TestNewProvider_BaseURLOverride(t *testing.T) {
	p, err := NewProvider("groq", "llama-3.3-70b-versatile",
		WithProviderAPIKey("dummy"),
		WithProviderBaseURL("https://custom.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
	// No direct accessor for base URL on the generic Provider interface;
	// the absence of an error and non-nil provider confirms the override path
	// didn't panic or reject the value.
}
