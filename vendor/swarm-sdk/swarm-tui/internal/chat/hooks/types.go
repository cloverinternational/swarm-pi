package hooks

import "time"

// HooksAgentHookExecution represents a hook that ran during tool execution
type HooksAgentHookExecution struct {
	HookName string `json:"hook_name"`
	Phase    string `json:"phase"` // "pre" or "post"
	Success  bool   `json:"success"`
	Blocked  bool   `json:"blocked"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// HooksAgentToolCall represents a tool call with its result for the hooks agent
type HooksAgentToolCall struct {
	Name      string                    `json:"name"`
	Input     map[string]any            `json:"input"`
	InputJSON string                    `json:"input_json"` // Pretty-printed JSON for display
	Result    string                    `json:"result"`
	Success   bool                      `json:"success"`
	Duration  time.Duration             `json:"duration"`
	PreHooks  []HooksAgentHookExecution `json:"pre_hooks,omitempty"`
	PostHooks []HooksAgentHookExecution `json:"post_hooks,omitempty"`
}

// HooksAgentResult contains the full result of a hooks agent execution
type HooksAgentResult struct {
	Content    string               `json:"content"`     // Final assistant response
	ToolCalls  []HooksAgentToolCall `json:"tool_calls"`  // All tool calls made
	TotalTurns int                  `json:"total_turns"` // Number of LLM turns
}
