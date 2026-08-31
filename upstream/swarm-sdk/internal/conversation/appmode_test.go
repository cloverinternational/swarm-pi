package conversation_test

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestAppModeTag(t *testing.T) {
	tests := []struct {
		mode conversation.AppMode
		want string
	}{
		{conversation.AppModeChat, "app_mode:chat"},
		{conversation.AppModeWork, "app_mode:work"},
		{conversation.AppModeCode, "app_mode:code"},
	}
	for _, tt := range tests {
		got := conversation.AppModeTag(tt.mode)
		if got != tt.want {
			t.Errorf("AppModeTag(%q) = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestParseAppModeTag(t *testing.T) {
	tests := []struct {
		tag      string
		wantMode conversation.AppMode
		wantOK   bool
	}{
		{"app_mode:chat", conversation.AppModeChat, true},
		{"app_mode:work", conversation.AppModeWork, true},
		{"app_mode:code", conversation.AppModeCode, true},
		{"app_mode:unknown", "", false},
		{"unrelated_tag", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		mode, ok := conversation.ParseAppModeTag(tt.tag)
		if ok != tt.wantOK || mode != tt.wantMode {
			t.Errorf("ParseAppModeTag(%q) = (%q, %v), want (%q, %v)",
				tt.tag, mode, ok, tt.wantMode, tt.wantOK)
		}
	}
}

func TestGetAppMode(t *testing.T) {
	tests := []struct {
		tags []string
		want conversation.AppMode
	}{
		{[]string{"app_mode:chat"}, conversation.AppModeChat},
		{[]string{"app_mode:work"}, conversation.AppModeWork},
		{[]string{"app_mode:code"}, conversation.AppModeCode},
		{[]string{"other", "app_mode:code"}, conversation.AppModeCode},
		{[]string{"other"}, conversation.AppModeChat}, // default
		{nil, conversation.AppModeChat},               // default
	}
	for _, tt := range tests {
		got := conversation.GetAppMode(tt.tags)
		if got != tt.want {
			t.Errorf("GetAppMode(%v) = %q, want %q", tt.tags, got, tt.want)
		}
	}
}

func TestSetAppMode(t *testing.T) {
	// Setting on empty tags
	tags := conversation.SetAppMode(nil, conversation.AppModeWork)
	if got := conversation.GetAppMode(tags); got != conversation.AppModeWork {
		t.Errorf("after SetAppMode(nil, work): got %q, want %q", got, conversation.AppModeWork)
	}

	// Replacing existing tag
	tags = conversation.SetAppMode([]string{"app_mode:chat", "other"}, conversation.AppModeCode)
	if got := conversation.GetAppMode(tags); got != conversation.AppModeCode {
		t.Errorf("after replace: got %q, want %q", got, conversation.AppModeCode)
	}
	// "other" tag must be preserved
	found := false
	for _, t2 := range tags {
		if t2 == "other" {
			found = true
		}
	}
	if !found {
		t.Error("SetAppMode must preserve non-app_mode tags")
	}
	// Exactly one app_mode tag
	count := 0
	for _, t2 := range tags {
		if len(t2) >= len(conversation.AppModeTagPrefix) && t2[:len(conversation.AppModeTagPrefix)] == conversation.AppModeTagPrefix {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 app_mode tag, got %d in %v", count, tags)
	}
}

func TestIsValidAppMode(t *testing.T) {
	valid := []string{"chat", "work", "code"}
	for _, s := range valid {
		if !conversation.IsValidAppMode(s) {
			t.Errorf("IsValidAppMode(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "task", "auto", "Chat", "WORK"}
	for _, s := range invalid {
		if conversation.IsValidAppMode(s) {
			t.Errorf("IsValidAppMode(%q) = true, want false", s)
		}
	}
}
