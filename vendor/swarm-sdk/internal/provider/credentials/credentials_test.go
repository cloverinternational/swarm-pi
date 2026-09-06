package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/xai"
)

// isolateHome points HOME at a fresh temp dir and clears every credential env
// var this package consults, so tests observe only their own fixtures.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", filepath.Join(home, ".swarm"))
	for _, v := range []string{
		"ANTHROPIC_API_KEY", "CLAUDE_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY",
		"XAI_API_KEY", "ZHIPU_API_KEY", "OPENROUTER_API_KEY", "CEREBRAS_API_KEY",
		"GROQ_API_KEY", "FIREWORKS_API_KEY", "DEEPSEEK_API_KEY", "PERPLEXITY_API_KEY",
		"TOGETHER_API_KEY", "ZAI_API_KEY", "CURSOR_API_KEY",
	} {
		t.Setenv(v, "")
	}
	return home
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHasStoredNoCredentials(t *testing.T) {
	isolateHome(t)
	for _, name := range []string{"ClaudeCode", "Anthropic", "OpenAI", "codex", "Google", "xai", "xai-api", "Cursor", "GLM", "some-custom"} {
		if HasStored(name) {
			t.Errorf("HasStored(%q) = true with no credentials anywhere", name)
		}
	}
}

func TestHasStoredOpenAIOAuthLightsUpCodexOnly(t *testing.T) {
	isolateHome(t)
	if err := openai.StoreOAuthToken(&openai.OAuthToken{AccessToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	if !HasStored("codex") {
		t.Error("codex should be credentialed after OpenAI OAuth token stored")
	}
	if HasStored("OpenAI") {
		t.Error("OpenAI (API key) must NOT be credentialed by an OAuth token")
	}
}

func TestHasStoredEnvKeyLightsUpAPIKeyEntryOnly(t *testing.T) {
	isolateHome(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	if !HasStored("OpenAI") {
		t.Error("OpenAI should be credentialed via OPENAI_API_KEY")
	}
	if HasStored("codex") {
		t.Error("codex (OAuth) must NOT be credentialed by OPENAI_API_KEY")
	}
	t.Setenv("XAI_API_KEY", "xk-test")
	if !HasStored("xai-api") {
		t.Error("xai-api should be credentialed via XAI_API_KEY")
	}
	if HasStored("xai") {
		t.Error("xai (OAuth) must NOT be credentialed by XAI_API_KEY")
	}
}

func TestHasStoredOAuthTokens(t *testing.T) {
	isolateHome(t)
	if err := anthropic.StoreOAuthToken(&anthropic.OAuthToken{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := xai.StoreOAuthToken(&xai.OAuthToken{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	if !HasStored("ClaudeCode") {
		t.Error("ClaudeCode should be credentialed after anthropic OAuth token stored")
	}
	if !HasStored("xai") {
		t.Error("xai should be credentialed after xAI OAuth token stored")
	}
}

func TestHasStoredGeminiOAuthFile(t *testing.T) {
	home := isolateHome(t)
	writeJSON(t, filepath.Join(home, ".config", "gemini-cli", "oauth_credentials.json"),
		map[string]string{"access_token": "g"})
	if !HasStored("Google") {
		t.Error("Google should be credentialed after gemini OAuth token stored")
	}
}

func TestHasStoredCursorCLIConfig(t *testing.T) {
	home := isolateHome(t)
	writeJSON(t, filepath.Join(home, ".cursor", "cli-config.json"),
		map[string]string{"accessToken": "c"})
	if !HasStored("Cursor") {
		t.Error("Cursor should be credentialed via ~/.cursor/cli-config.json")
	}
}

func TestHasStoredAccountsRegistryMatchesWireFamily(t *testing.T) {
	home := isolateHome(t)
	// The TUI historically stores OpenAI OAuth accounts under Provider "OpenAI";
	// they must count for the codex row (same wire family, OAuth accounts only).
	writeJSON(t, filepath.Join(home, ".swarm", "tui_accounts.json"),
		map[string]any{"accounts": []map[string]any{{"id": "1", "provider": "OpenAI", "is_active": true}}})
	if !HasStored("codex") {
		t.Error("codex should be credentialed via an OpenAI account in tui_accounts.json")
	}
}

func TestHasStoredCredentialsJSONForCustom(t *testing.T) {
	home := isolateHome(t)
	writeJSON(t, filepath.Join(home, ".swarm", "config", "credentials.json"),
		map[string]any{"providers": map[string]any{"Cerebras": map[string]string{"api_key": "ck"}}})
	if !HasStored("cerebras") {
		t.Error("cerebras should be credentialed via credentials.json (case-insensitive)")
	}
	if HasStored("groq") {
		t.Error("groq should not be credentialed")
	}
}

func TestHasStoredCustomEnvAliases(t *testing.T) {
	isolateHome(t)
	t.Setenv("ZAI_API_KEY", "z")
	if !HasStored("z.ai") {
		t.Error("z.ai should be credentialed via ZAI_API_KEY alias")
	}
	t.Setenv("TOGETHER_API_KEY", "tg")
	if !HasStored("together-ai") {
		t.Error("together-ai should be credentialed via TOGETHER_API_KEY alias")
	}
}

func TestHasStoredGenericEnvFallbackForCustom(t *testing.T) {
	isolateHome(t)
	t.Setenv("MYLOCAL_API_KEY", "k")
	if !HasStored("mylocal") {
		t.Error("custom provider should be credentialed via generic <NAME>_API_KEY")
	}
}

func TestHasAnyForFamilyIsLoose(t *testing.T) {
	isolateHome(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	// Startup auto-selection semantics: "is there ANY way to drive this wire
	// family" — an env key or an OAuth token both count, for both spellings.
	if !HasAnyForFamily("openai") || !HasAnyForFamily("codex") {
		t.Error("HasAnyForFamily should accept env key for the openai family")
	}
	if HasAnyForFamily("xai") {
		t.Error("xai family has no credentials")
	}
}
