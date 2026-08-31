package main

import (
	"flag"
	"strings"
	"testing"
)

func TestResumeFlagRegistered(t *testing.T) {
	resumeFlag := flag.Lookup("r")
	if resumeFlag == nil {
		t.Fatal("expected -r flag to be registered")
	}
	if resumeFlag.DefValue != "" {
		t.Fatalf("expected -r default to be empty, got %q", resumeFlag.DefValue)
	}
	if !strings.Contains(strings.ToLower(resumeFlag.Usage), "resume") {
		t.Fatalf("expected -r help to explain resume behavior, got %q", resumeFlag.Usage)
	}
}

func TestResolveHeadlessResumeConversationID(t *testing.T) {
	tests := []struct {
		name           string
		resumeID       string
		conversationID string
		want           string
		wantErr        string
	}{
		{name: "short flag only", resumeID: "conv-short", want: "conv-short"},
		{name: "long flag only", conversationID: "conv-long", want: "conv-long"},
		{name: "matching flags", resumeID: "conv-same", conversationID: "conv-same", want: "conv-same"},
		{name: "surrounding whitespace", resumeID: "  conv-trimmed  ", want: "conv-trimmed"},
		{
			name:           "conflicting flags",
			resumeID:       "conv-short",
			conversationID: "conv-long",
			wantErr:        "conflicting conversation IDs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveHeadlessConversationID(test.resumeID, test.conversationID)
			if test.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", test.wantErr)
				}
				if !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected error containing %q, got %q", test.wantErr, err)
				}
				if !strings.Contains(err.Error(), "-r") || !strings.Contains(err.Error(), "--conversation-id") {
					t.Fatalf("expected conflict error to name both flags, got %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveHeadlessConversationID() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("resolveHeadlessConversationID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCurrentAppOptionsIncludesResumeConversationID(t *testing.T) {
	previous := *resumeConversationFlag
	t.Cleanup(func() {
		*resumeConversationFlag = previous
	})

	*resumeConversationFlag = "  conv-tui  "
	options := currentAppOptions(nil, "test-peer")
	if options.ResumeConversationID != "conv-tui" {
		t.Fatalf("ResumeConversationID = %q, want %q", options.ResumeConversationID, "conv-tui")
	}
}
