package provider

import "testing"

// TestBuiltinProvidersIncludesClaudeCodeOAuth locks in the canonical source of
// truth: the Anthropic "Claude Code" OAuth login must always be a built-in
// provider. Its absence here was the root cause of it vanishing from the TUI
// auth screen.
func TestBuiltinProvidersIncludesClaudeCodeOAuth(t *testing.T) {
	cc, ok := LookupBuiltinProvider("ClaudeCode")
	if !ok {
		t.Fatal("ClaudeCode missing from BuiltinProviders catalog")
	}
	if !cc.IsOAuth() {
		t.Errorf("ClaudeCode AuthType = %q, want %q", cc.AuthType, AuthOAuth)
	}
	if cc.APIType != "anthropic" {
		t.Errorf("ClaudeCode APIType = %q, want anthropic", cc.APIType)
	}
	if cc.DisplayName == "" {
		t.Error("ClaudeCode DisplayName must not be empty")
	}
}

// TestBuiltinOAuthProvidersAllOAuth ensures the OAuth filter never leaks an
// API-key provider onto a login screen, and is non-empty.
func TestBuiltinOAuthProvidersAllOAuth(t *testing.T) {
	oauth := BuiltinOAuthProviders()
	if len(oauth) == 0 {
		t.Fatal("BuiltinOAuthProviders returned no providers")
	}
	for _, p := range oauth {
		if !p.IsOAuth() {
			t.Errorf("provider %q in OAuth list has AuthType %q", p.Name, p.AuthType)
		}
	}
	// ClaudeCode, Google and xai are the expected OAuth providers.
	for _, want := range []string{"ClaudeCode", "Google", "xai"} {
		found := false
		for _, p := range oauth {
			if p.Name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected OAuth provider %q not found", want)
		}
	}
}

// TestBuiltinProvidersIncludesCodexOAuth locks in the OpenAI Codex OAuth entry.
// Without it, a completed OpenAI device-flow login stores a token that no
// visible provider ever picks up (the original "signed in but OpenAI (OAuth)
// is nowhere" bug).
func TestBuiltinProvidersIncludesCodexOAuth(t *testing.T) {
	cx, ok := LookupBuiltinProvider("codex")
	if !ok {
		t.Fatal("codex missing from BuiltinProviders catalog")
	}
	if !cx.IsOAuth() {
		t.Errorf("codex AuthType = %q, want %q", cx.AuthType, AuthOAuth)
	}
	if cx.APIType != "openai" {
		t.Errorf("codex APIType = %q, want openai", cx.APIType)
	}
	// codex is not catwalk-backed: RegistryID must NOT resolve to the plain
	// OpenAI API model list (those models are not served by the codex backend).
	if cx.RegistryID != "codex" {
		t.Errorf("codex RegistryID = %q, want codex", cx.RegistryID)
	}
	if cx.DisplayName == "" {
		t.Error("codex DisplayName must not be empty")
	}
	// It must appear on login screens.
	found := false
	for _, p := range BuiltinOAuthProviders() {
		if p.Name == "codex" {
			found = true
		}
	}
	if !found {
		t.Error("codex not returned by BuiltinOAuthProviders")
	}
}

// TestBuiltinProvidersDeclareEnvVars ensures API-key built-ins declare the env
// vars that count as credentials, so availability can be derived from the
// catalog instead of per-provider hardcoded checks.
func TestBuiltinProvidersDeclareEnvVars(t *testing.T) {
	want := map[string][]string{
		"Anthropic": {"ANTHROPIC_API_KEY", "CLAUDE_API_KEY"},
		"OpenAI":    {"OPENAI_API_KEY"},
		"xai-api":   {"XAI_API_KEY"},
		"GLM":       {"ZHIPU_API_KEY"},
	}
	for name, envs := range want {
		p, ok := LookupBuiltinProvider(name)
		if !ok {
			t.Errorf("builtin %q missing", name)
			continue
		}
		if len(p.EnvVars) != len(envs) {
			t.Errorf("%s EnvVars = %v, want %v", name, p.EnvVars, envs)
			continue
		}
		for i := range envs {
			if p.EnvVars[i] != envs[i] {
				t.Errorf("%s EnvVars = %v, want %v", name, p.EnvVars, envs)
				break
			}
		}
	}
	// OAuth entries must NOT claim API-key env vars: an exported OPENAI_API_KEY
	// must never light up the codex OAuth entry.
	cx, _ := LookupBuiltinProvider("codex")
	if len(cx.EnvVars) != 0 {
		t.Errorf("codex EnvVars = %v, want none (OAuth-only entry)", cx.EnvVars)
	}
}

// TestBuiltinProvidersReturnsCopy verifies callers cannot mutate the canonical
// list through the returned slice.
func TestBuiltinProvidersReturnsCopy(t *testing.T) {
	a := BuiltinProviders()
	if len(a) == 0 {
		t.Fatal("BuiltinProviders returned empty")
	}
	a[0].Name = "MUTATED"
	b := BuiltinProviders()
	if b[0].Name == "MUTATED" {
		t.Error("BuiltinProviders leaked a reference to the backing array")
	}
}
