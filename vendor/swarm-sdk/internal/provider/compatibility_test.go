package provider

import "testing"

func TestNormalizeProviderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Anthropic
		{"claudecode", "anthropic"},
		{"ClaudeCode", "anthropic"},
		{"anthropic", "anthropic"},
		{"ANTHROPIC", "anthropic"},
		{"claude", "anthropic"},

		// OpenAI
		{"openai", "openai"},
		{"codex", "openai"},
		{"OpenAI", "openai"},

		// Gemini / Google
		{"gemini", "gemini"},
		{"google", "gemini"},
		{"Google", "gemini"},
		{"gemini-code-assist", "gemini"},

		// Cerebras
		{"cerebras", "cerebras"},

		// OpenRouter
		{"openrouter", "openrouter"},

		// Z.AI / GLM
		{"z.ai", "z.ai"},
		{"zai", "z.ai"},
		{"glm", "z.ai"},

		// Fireworks
		{"fireworks", "fireworks"},

		// Groq
		{"groq", "groq"},

		// Together
		{"together", "together"},
		{"together-ai", "together"},
		{"togetherai", "together"},
		{"Together AI", "together"},

		// DeepSeek
		{"deepseek", "deepseek"},
		{"DeepSeek", "deepseek"},

		// Perplexity
		{"perplexity", "perplexity"},

		// Mistral
		{"mistral", "mistral"},

		// Moonshot
		{"moonshot", "moonshot"},

		// Replicate
		{"replicate", "replicate"},

		// Chutes
		{"chutes", "chutes"},

		// Qwen
		{"qwen", "qwen"},

		// Kimi
		{"kimi", "kimi"},

		// XAI / Grok
		{"xai", "xai"},
		{"grok", "xai"},

		// Meta / Llama
		{"meta", "meta"},
		{"llama", "meta"},

		// Local / Ollama
		{"local", "local"},
		{"ollama", "local"},

		// Zhipu
		{"zhipu", "zhipu"},

		// MiniMax
		{"minimax", "minimax"},

		// Wafer
		{"wafer", "wafer.ai"},
		{"wafer.ai", "wafer.ai"},
		{"WAFER.AI", "wafer.ai"},

		// Unknown / custom — falls back to lowercased input
		{"MyFireworks", "myfireworks"},
		{"CustomProvider", "customprovider"},

		// Edge cases
		{"", ""},
		{"  openai  ", "openai"},
		{"OPENAI", "openai"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeProviderName(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeProviderName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
