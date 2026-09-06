package chat

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
)

func TestConversationSegmentsMapsMessagesToolCallsAndResults(t *testing.T) {
	timestamp := time.Date(2026, 8, 4, 12, 30, 0, 0, time.UTC)
	conv := &conversation.Conversation{
		ID:            "conv-1",
		WorkspacePath: "/workspace",
		Messages: []*conversation.Message{
			{
				ID: "message-1", Role: conversation.RoleAssistant, Timestamp: timestamp,
				Content: "I will inspect the file.",
				ToolCalls: []conversation.ToolCall{
					{ID: "call-1", Name: "Read", Parameters: map[string]any{"path": "/tmp/input.go"}},
				},
				ToolResults: []conversation.ToolResult{
					{CallID: "call-1", Name: "Read", Output: "package example"},
					{CallID: "call-2", Name: "Bash", Output: "command failed", Error: &conversation.ToolError{Type: "exit", Message: "1"}},
				},
			},
		},
	}

	segments := conversationSegments(conv)
	if len(segments) != 4 {
		t.Fatalf("len(segments) = %d, want 4: %#v", len(segments), segments)
	}
	wantKinds := []string{
		searchindex.SegmentMessage,
		searchindex.SegmentToolCall,
		searchindex.SegmentToolResult,
		searchindex.SegmentToolResult,
	}
	wantNames := []string{"", "Read", "Read", "Bash"}
	wantCallIDs := []string{"", "call-1", "call-1", "call-2"}
	wantFailed := []bool{false, false, false, true}
	for index, segment := range segments {
		if segment.Ordinal != index {
			t.Errorf("segment %d ordinal = %d", index, segment.Ordinal)
		}
		if segment.Kind != wantKinds[index] || segment.ToolName != wantNames[index] ||
			segment.CallID != wantCallIDs[index] || segment.Failed != wantFailed[index] {
			t.Errorf("segment %d = %#v", index, segment)
		}
		if segment.ConversationID != conv.ID || segment.WorkspacePath != conv.WorkspacePath ||
			segment.MessageID != "message-1" || segment.Role != string(conversation.RoleAssistant) ||
			!segment.Timestamp.Equal(timestamp) {
			t.Errorf("segment %d provenance = %#v", index, segment)
		}
	}
	if !strings.Contains(segments[1].Text, "Read") ||
		!strings.Contains(segments[1].Text, "path=/tmp/input.go") {
		t.Fatalf("tool call text = %q", segments[1].Text)
	}
}

func TestConversationSegmentsToolResultFailureFlags(t *testing.T) {
	conv := &conversation.Conversation{ID: "conv", Messages: []*conversation.Message{{
		Role: conversation.RoleAssistant,
		ToolResults: []conversation.ToolResult{
			{Name: "Bash", CallID: "ok", Output: "ok"},
			{Name: "Bash", CallID: "bad", Output: "bad", Error: &conversation.ToolError{Message: "failed"}},
		},
	}}}
	segments := conversationSegments(conv)
	if len(segments) != 2 {
		t.Fatalf("len(segments) = %d, want 2", len(segments))
	}
	if segments[0].Failed {
		t.Errorf("successful result marked failed: %#v", segments[0])
	}
	if !segments[1].Failed {
		t.Errorf("errored result not marked failed: %#v", segments[1])
	}
}

func TestConversationSegmentsMarksRuntimeWithSharedHistoryPredicate(t *testing.T) {
	conv := &conversation.Conversation{ID: "conv", Messages: []*conversation.Message{
		{
			ID:      "runtime",
			Role:    conversation.RoleUser,
			Content: "[SCHEDULED] refresh index\n<system-reminder",
		},
		{
			ID:      "human",
			Role:    conversation.RoleUser,
			Content: "Please refresh the index.",
		},
	}}
	segments := conversationSegments(conv)
	if len(segments) != 2 {
		t.Fatalf("len(segments) = %d, want 2: %#v", len(segments), segments)
	}
	if !segments[0].Runtime {
		t.Errorf("runtime-injected user message not marked Runtime: %#v", segments[0])
	}
	if segments[1].Runtime {
		t.Errorf("real user message marked Runtime: %#v", segments[1])
	}
}

func TestConversationSegmentsNeverIndexesBinaryContentBlockData(t *testing.T) {
	const binarySecret = "binary-secret-must-not-be-searchable"
	conv := &conversation.Conversation{ID: "conv", Messages: []*conversation.Message{{
		Role: conversation.RoleAssistant,
		ToolResults: []conversation.ToolResult{{
			Name: "Read", CallID: "call", Output: "visible output",
			Content: []conversation.ContentBlock{
				{Type: "text", Text: "visible text block"},
				{Type: "image", Data: []byte(binarySecret), MimeType: "image/png"},
			},
		}},
	}}}
	segments := conversationSegments(conv)
	if len(segments) != 1 {
		t.Fatalf("len(segments) = %d, want 1", len(segments))
	}
	if strings.Contains(segments[0].Text, binarySecret) {
		t.Fatalf("binary data leaked into segment: %q", segments[0].Text)
	}
	if !strings.Contains(segments[0].Text, "visible output") ||
		!strings.Contains(segments[0].Text, "visible text block") {
		t.Fatalf("text content missing from segment: %q", segments[0].Text)
	}
}

func TestConversationSegmentsTruncatesParametersAndOutputsOnRuneBoundaries(t *testing.T) {
	oversized := strings.Repeat("界", searchindex.MaxSegmentTextBytes)
	conv := &conversation.Conversation{ID: "conv", Messages: []*conversation.Message{{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			Name: "Write", ID: "call", Parameters: map[string]any{"content": oversized},
		}},
		ToolResults: []conversation.ToolResult{{
			Name: "Write", CallID: "call", Output: oversized,
		}},
	}}}
	segments := conversationSegments(conv)
	if len(segments) != 2 {
		t.Fatalf("len(segments) = %d, want 2", len(segments))
	}
	for _, segment := range segments {
		if len(segment.Text) > searchindex.MaxSegmentTextBytes {
			t.Errorf("%s text is %d bytes, max %d", segment.Kind, len(segment.Text), searchindex.MaxSegmentTextBytes)
		}
		if !utf8.ValidString(segment.Text) {
			t.Errorf("%s text is not valid UTF-8", segment.Kind)
		}
	}
	if len(segments[0].Text) >= len(oversized) {
		t.Errorf("parameter value was not truncated: %d bytes", len(segments[0].Text))
	}
	if len(segments[1].Text) != searchindex.MaxSegmentTextBytes-1 {
		// 64 KiB is not divisible by the three-byte rune width, so the largest
		// valid prefix is one byte below the limit.
		t.Errorf("output length = %d, want %d", len(segments[1].Text), searchindex.MaxSegmentTextBytes-1)
	}
}

func TestConversationSegmentsEnforcesConversationCapAsPrefix(t *testing.T) {
	messages := make([]*conversation.Message, searchindex.MaxSegmentsPerConversation+1)
	for index := range messages {
		messages[index] = &conversation.Message{
			ID: "message", Role: conversation.RoleUser, Content: "human text",
		}
	}
	segments := conversationSegments(&conversation.Conversation{ID: "conv", Messages: messages})
	if len(segments) != searchindex.MaxSegmentsPerConversation {
		t.Fatalf("len(segments) = %d, want %d", len(segments), searchindex.MaxSegmentsPerConversation)
	}
	if segments[len(segments)-1].Ordinal != searchindex.MaxSegmentsPerConversation-1 {
		t.Fatalf("last segment = %#v", segments[len(segments)-1])
	}
}

func TestStreamingConversationSegmentsDecodesMessagesIncrementally(t *testing.T) {
	path := t.TempDir() + "/oversized.json"
	stored := map[string]any{
		"id": "stored-id",
		"messages": []*conversation.Message{{
			ID: "message", Role: conversation.RoleAssistant,
			ToolResults: []conversation.ToolResult{{Name: "Bash", CallID: "call", Output: "streamed needle"}},
		}},
		// Deliberately serialized after messages by constructing the JSON below:
		// SourceFile workspace must be available while messages are emitted.
		"workspace_path": "/stored/workspace",
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	segments, err := streamingConversationSegments(context.Background(), searchindex.SourceFile{
		ID: "source-id", Path: path, WorkspacePath: "/source/workspace",
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(segments) != 1 || segments[0].Text != "streamed needle" {
		t.Fatalf("segments = %#v", segments)
	}
	// map JSON key order is deterministic but lexical, so workspace_path follows
	// messages; emitted segments intentionally use SourceFile provenance.
	if segments[0].ConversationID != "stored-id" ||
		segments[0].WorkspacePath != "/source/workspace" {
		t.Fatalf("provenance = %#v", segments[0])
	}
}

func TestStreamingConversationSegmentsMatchesFullDecode(t *testing.T) {
	timestamp := time.Date(2026, 8, 5, 2, 3, 4, 5, time.UTC)
	conv := &conversation.Conversation{
		ID:            "parity",
		WorkspacePath: "/workspace",
		Messages: []*conversation.Message{
			{
				ID: "user", Role: conversation.RoleUser, Timestamp: timestamp,
				Content: "[SCHEDULED] runtime prompt",
			},
			{
				ID: "assistant", Role: conversation.RoleAssistant, Timestamp: timestamp.Add(time.Second),
				Content: "answer",
				ToolCalls: []conversation.ToolCall{{
					ID: "call", Name: "Read", Parameters: map[string]any{
						"path": "/tmp/input", "nested": []any{true, float64(42)},
					},
				}},
				ToolResults: []conversation.ToolResult{{
					CallID: "call", Name: "Read", Output: "visible",
					Content: []conversation.ContentBlock{{Type: "text", Text: "block"}},
					Error:   &conversation.ToolError{Type: "read", Message: "failed"},
				}},
			},
		},
	}
	encoded, err := json.Marshal(conv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := t.TempDir() + "/conversation.json"
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := streamingConversationSegments(context.Background(), searchindex.SourceFile{
		ID: conv.ID, Path: path, WorkspacePath: conv.WorkspacePath,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	want := conversationSegments(conv)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("streaming segments differ from full decode\ngot:  %#v\nwant: %#v", got, want)
	}
}
