package client

import (
	"errors"
	"strings"
	"testing"
)

// TestIsValidCaseInsensitive is the regression test for the headless failure
//
//	CONTRACT VIOLATION: Unknown provider: "ClaudeCode"
//
// triggered by `swarm -p '...'` when the active profile carries a canonically-
// cased provider name (e.g. "ClaudeCode"). ParseProvider lower-cases its input
// before lookup, but Provider.IsValid did not — so a name that PASSED
// ParseProvider would still be rejected by client.New's IsValid gate.
//
// IsValid and ParseProvider must agree: any name ParseProvider accepts must
// also pass IsValid, regardless of case. This is the contract callers rely on
// when they round-trip a parsed Provider back through validation.
func TestIsValidCaseInsensitive(t *testing.T) {
	cases := []string{
		"ClaudeCode",
		"CLAUDECODE",
		"Anthropic",
		"OpenAI",
		"Gemini",
		"Google",
		"XAI",
		"Fireworks",
		"GLM",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseProvider(name); err != nil {
				t.Skipf("ParseProvider rejects %q — skipping (this test guards the round-trip only)", name)
			}
			if !Provider(name).IsValid() {
				t.Errorf("Provider(%q).IsValid() = false, but ParseProvider(%q) succeeded — IsValid and ParseProvider must agree on the same input", name, name)
			}
		})
	}
}

// TestClientNewAcceptsMixedCaseProvider is the end-to-end regression: passing a
// mixed-case provider name through WithProviderString (the TUI's actual path
// from `~/.swarm/config/agent_profiles.json`) must not trip the contract-violation
// gate in client.New. Before the fix this returned the "Unknown provider:
// \"ClaudeCode\"" ContractViolation that aborted every headless run whose
// profile stored the name as "ClaudeCode" rather than "claudecode".
func TestClientNewAcceptsMixedCaseProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key-not-used")
	// Use a temp HOME so the test does not depend on the developer's real
	// ~/.swarm/config/providers.json — IsValid must succeed purely from the
	// in-memory validProviders table.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	_, err := New(
		WithProviderString("ClaudeCode", "claude-sonnet-4-5"),
		WithWorkspace(tmpHome),
		WithoutAutoConfig(),
		WithoutIndexMd(),
		WithClientType(ClientTypeTUI),
	)
	if err == nil {
		return
	}
	var cv *ContractViolation
	if errors.As(err, &cv) && strings.Contains(cv.Violation, "Unknown provider") {
		t.Fatalf("client.New rejected mixed-case provider name: %v", err)
	}
	// Any other construction error (e.g. storage, missing creds) is fine — the
	// only failure mode this test guards against is the validation gate.
}
