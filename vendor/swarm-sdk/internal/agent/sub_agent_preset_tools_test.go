package agent

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestPresetConfig_ReadCapablePresetsHaveTools is a regression guard for the bug
// where every legacy preset declared Tools: []string{} (an empty allow-list).
// An empty allow-list matches NO tools (copyAll is true only for ["*"]), so the
// preset sub-agent launched with zero tools and the model hallucinated tool
// names (shell/bash/read_file) and looped until timeout.
//
// Presets that need to inspect the filesystem (question_answerer, data_validator,
// error_analyzer) MUST carry a non-empty read-only allow-list. The pure
// text-transformation presets (code_formatter, text_summarizer) operate on text
// passed in the task and intentionally stay tool-light.
func TestPresetConfig_ReadCapablePresetsHaveTools(t *testing.T) {
	cfg := provider.Config{Name: "mock", Model: "mock-model"}

	// These read/inspect the filesystem and must NOT be tool-starved.
	readCapable := []PresetSubAgentType{
		PresetQuestionAnswerer,
		PresetDataValidator,
		PresetErrorAnalyzer,
	}
	// The read-only tool names we expect (both registry conventions).
	wantAny := map[string]bool{
		"Read": true, "Grep": true,
		"file_read": true, "grep": true, "list_dir": true,
		"semantic_grep": true,
	}
	// Mutation/escape tools must never appear in a preset allow-list.
	forbidden := map[string]bool{
		"bash": true, "Shell": true,
		"file_write": true, "Write": true,
		"str_replace": true, "Edit": true,
	}

	for _, p := range readCapable {
		c := getPresetConfig(p, cfg)
		if len(c.Tools) == 0 {
			t.Errorf("preset %q has an empty Tools allow-list — it will be tool-starved and loop", p)
			continue
		}
		for _, tool := range c.Tools {
			if forbidden[tool] {
				t.Errorf("preset %q exposes forbidden mutation tool %q", p, tool)
			}
			if !wantAny[tool] {
				t.Errorf("preset %q exposes unexpected tool %q (not in the read-only set)", p, tool)
			}
		}
	}

	// Text-only presets remain tool-light by design.
	for _, p := range []PresetSubAgentType{PresetCodeFormatter, PresetTextSummarizer} {
		c := getPresetConfig(p, cfg)
		if len(c.Tools) != 0 {
			t.Errorf("preset %q is expected to be tool-light (text transformation), got tools: %v", p, c.Tools)
		}
	}
}
