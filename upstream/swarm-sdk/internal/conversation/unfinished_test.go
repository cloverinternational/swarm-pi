package conversation

import "testing"

// assistantMsg builds an assistant message carrying an optional stop reason in
// Metadata (the canonical Message model stores stop/finish reason there).
func assistantMsg(stopReason string) *Message {
	m := &Message{Role: RoleAssistant, Content: "reply"}
	if stopReason != "" {
		m.Metadata = map[string]any{"stop_reason": stopReason}
	}
	return m
}

func TestIsUnfinished_LiveMessages(t *testing.T) {
	tests := []struct {
		name string
		msgs []*Message
		want bool
	}{
		{
			name: "last message role user => unfinished",
			msgs: []*Message{
				assistantMsg("end_turn"),
				{Role: RoleUser, Content: "another question"},
			},
			want: true,
		},
		{
			name: "last assistant natural stop end_turn => finished",
			msgs: []*Message{
				{Role: RoleUser, Content: "hi"},
				assistantMsg("end_turn"),
			},
			want: false,
		},
		{
			name: "last assistant stop_reason max_tokens => unfinished",
			msgs: []*Message{
				{Role: RoleUser, Content: "hi"},
				assistantMsg("max_tokens"),
			},
			want: true,
		},
		{
			name: "last assistant empty stop_reason => unfinished",
			msgs: []*Message{
				{Role: RoleUser, Content: "hi"},
				assistantMsg(""),
			},
			want: true,
		},
		{
			name: "last assistant natural stop 'stop' => finished",
			msgs: []*Message{
				{Role: RoleUser, Content: "hi"},
				assistantMsg("stop"),
			},
			want: false,
		},
		{
			name: "last assistant stop_sequence => finished",
			msgs: []*Message{
				{Role: RoleUser, Content: "hi"},
				assistantMsg("stop_sequence"),
			},
			want: false,
		},
		{
			name: "last assistant with dangling tool call => unfinished",
			msgs: []*Message{
				{Role: RoleUser, Content: "hi"},
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "bash"}}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Conversation{Messages: tt.msgs}
			if got := c.IsUnfinished(); got != tt.want {
				t.Fatalf("IsUnfinished() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsUnfinished_StrippedMarkers(t *testing.T) {
	tests := []struct {
		name       string
		lastRole   string
		stopReason string
		want       bool
	}{
		{
			name:       "assistant + end_turn => finished",
			lastRole:   string(RoleAssistant),
			stopReason: "end_turn",
			want:       false,
		},
		{
			name:       "assistant + empty stop reason => unfinished",
			lastRole:   string(RoleAssistant),
			stopReason: "",
			want:       true,
		},
		{
			name:       "assistant + max_tokens => unfinished",
			lastRole:   string(RoleAssistant),
			stopReason: "max_tokens",
			want:       true,
		},
		{
			name:     "user last role => unfinished",
			lastRole: string(RoleUser),
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Conversation{
				Metadata: ConversationMetadata{
					Custom: map[string]any{
						metaLastRole:       tt.lastRole,
						metaLastStopReason: tt.stopReason,
					},
				},
			}
			if got := c.IsUnfinished(); got != tt.want {
				t.Fatalf("IsUnfinished() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsUnfinished_Empty(t *testing.T) {
	c := &Conversation{}
	if c.IsUnfinished() {
		t.Fatal("empty conversation must not be unfinished")
	}
	// Nil receiver must be safe.
	var nilConv *Conversation
	if nilConv.IsUnfinished() {
		t.Fatal("nil conversation must not be unfinished")
	}
}

// TestAddMessageRecordsMarkers verifies AddMessage persists cheap terminal
// markers so message-stripped listings can still evaluate IsUnfinished.
func TestAddMessageRecordsMarkers(t *testing.T) {
	c := &Conversation{}
	c.AddMessage(&Message{Role: RoleUser, Content: "hi"})
	c.AddMessage(assistantMsg("end_turn"))

	if got := c.Metadata.Custom[metaLastRole]; got != string(RoleAssistant) {
		t.Fatalf("last_role marker = %v, want assistant", got)
	}
	if got := c.Metadata.Custom[metaLastStopReason]; got != "end_turn" {
		t.Fatalf("last_stop_reason marker = %v, want end_turn", got)
	}

	// Simulate a message-stripped listing: markers alone must still evaluate.
	stripped := &Conversation{Metadata: c.Metadata}
	if stripped.IsUnfinished() {
		t.Fatal("assistant/end_turn markers must evaluate as finished")
	}
}
