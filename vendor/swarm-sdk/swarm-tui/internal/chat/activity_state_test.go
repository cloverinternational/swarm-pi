package chat

import "testing"

func TestDefaultToolActivityDescriberCoreToolLabels(t *testing.T) {
	d := NewDefaultToolActivityDescriber()
	tests := []struct {
		name   string
		tool   string
		params map[string]any
		want   string
	}{
		{name: "Bash capital", tool: "Bash", params: map[string]any{"command": "go test ./..."}, want: "Running go"},
		{name: "bash lower", tool: "bash", params: map[string]any{"command": "npm install"}, want: "Running npm"},
		{name: "Grep capital", tool: "Grep", params: map[string]any{"pattern": "ActivityStateManager"}, want: "Searching for \"ActivityStateManager\""},
		{name: "grep lower", tool: "grep", params: map[string]any{"pattern": "activeToolActivities"}, want: "Searching for \"activeToolActivities\""},
		{name: "Read", tool: "Read", params: map[string]any{"file_path": "/tmp/app_update.go"}, want: "Reading app_update.go"},
		{name: "Write", tool: "Write", params: map[string]any{"file_path": "/tmp/activity_state.go"}, want: "Writing activity_state.go"},
		{name: "Edit", tool: "Edit", params: map[string]any{"file_path": "/tmp/app.go"}, want: "Editing app.go"},
		{name: "semantic rename", tool: "semantic_rename", params: map[string]any{"old_name": "Old", "new_name": "New"}, want: "Renaming Old to New"},
		{name: "websearch", tool: "websearch", params: map[string]any{"query": "current docs"}, want: "Searching web for \"current docs\""},
		{name: "Context7 docs", tool: "mcp_context7_query-docs", params: map[string]any{"libraryId": "/vercel/next.js"}, want: "Reading docs for /vercel/next.js"},
		{name: "A2A send", tool: "a2a_send_message", params: nil, want: "Messaging peer agent"},
		{name: "vault exec", tool: "vault_exec", params: nil, want: "Running with credential"},
		{name: "browser", tool: "browser_click", params: nil, want: "Using browser"},
		{name: "history", tool: "HistorySearch", params: nil, want: "Searching history"},
		{name: "task manage", tool: "TaskManage", params: nil, want: "Managing tasks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.Describe(tt.tool, tt.params); got != tt.want {
				t.Fatalf("Describe(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestDefaultToolActivityDescriberPrefixFallbacks(t *testing.T) {
	d := NewDefaultToolActivityDescriber()
	tests := []struct {
		name   string
		tool   string
		params map[string]any
		want   string
	}{
		{name: "Terraform run", tool: "mcp_terraform_create_run", params: map[string]any{"workspace_name": "prod-app"}, want: "Starting Terraform run for prod-app"},
		{name: "Terraform provider", tool: "mcp_terraform_get_provider_details", params: map[string]any{"provider_name": "aws"}, want: "Reading Terraform provider aws"},
		{name: "Unknown MCP", tool: "mcp_custom_tool", params: nil, want: "Using MCP tool"},
		{name: "Unknown A2A", tool: "a2a_custom_action", params: nil, want: "Working with peer agent"},
		{name: "Unknown search", tool: "custom_search_tool", params: nil, want: "Searching"},
		{name: "Unknown read", tool: "custom_read_tool", params: nil, want: "Reading custom read tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.Describe(tt.tool, tt.params); got != tt.want {
				t.Fatalf("Describe(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestActivityStateKeepsToolUseWhileGenericDeltasArrive(t *testing.T) {
	m := NewActivityStateManager(nil)
	m.BeginTool("tc_1", "Bash", map[string]any{"command": "node server.js"})

	m.SetPhase(ActivityPhaseResponding, "")
	if got := m.Snapshot(); got.Phase != ActivityPhaseToolUse || got.Label != "Running node" {
		t.Fatalf("responding delta should not hide active tool: phase=%q label=%q", got.Phase, got.Label)
	}

	m.SetPhase(ActivityPhaseThinking, "Reasoning")
	if got := m.Snapshot(); got.Phase != ActivityPhaseToolUse || got.Label != "Running node" {
		t.Fatalf("thinking delta should not hide active tool: phase=%q label=%q", got.Phase, got.Label)
	}

	m.EndTool("tc_1")
	if got := m.Snapshot(); got.Phase != ActivityPhaseThinking {
		t.Fatalf("ending last tool should return to thinking, got %q", got.Phase)
	}
}
