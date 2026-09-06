package chat

import (
	"strings"
	"testing"
)

func TestAsyncStartupPreservesResumeConversationID(t *testing.T) {
	const conversationID = "conversation-to-resume"

	app := NewAsyncAppWithOptions(AppOptions{ResumeConversationID: conversationID})

	if got := app.appOptions.ResumeConversationID; got != conversationID {
		t.Fatalf("ResumeConversationID = %q, want %q", got, conversationID)
	}
}

func TestResumeConversationStartupOpensRequestedConversation(t *testing.T) {
	app := &App{}
	var openedID string

	app.resumeConversationAtStartup("  conversation-to-resume  ", func(conversationID string) bool {
		openedID = conversationID
		app.screen = ScreenChat
		return true
	})

	if openedID != "conversation-to-resume" {
		t.Fatalf("opened conversation %q, want %q", openedID, "conversation-to-resume")
	}
	if app.screen != ScreenChat {
		t.Fatalf("screen = %v, want ScreenChat", app.screen)
	}
	if len(app.notifications) != 0 {
		t.Fatalf("successful resume emitted notifications: %#v", app.notifications)
	}
}

func TestResumeConversationStartupMissingIDWarnsAndRemainsUsable(t *testing.T) {
	app := &App{screen: ScreenHome}

	app.resumeConversationAtStartup("missing-conversation", func(string) bool {
		return false
	})

	if app.screen != ScreenHome {
		t.Fatalf("screen = %v, want existing ScreenHome", app.screen)
	}
	if len(app.notifications) != 1 {
		t.Fatalf("notifications = %#v, want one warning", app.notifications)
	}
	warning := app.notifications[0]
	if warning.Kind != "warning" {
		t.Fatalf("notification kind = %q, want warning", warning.Kind)
	}
	if !strings.Contains(warning.Text, "missing-conversation") {
		t.Fatalf("warning %q does not identify the missing conversation", warning.Text)
	}
}

func TestResumeConversationStartupLoaderPanicWarnsAndRemainsUsable(t *testing.T) {
	app := &App{screen: ScreenHome}

	app.resumeConversationAtStartup("invalid-conversation", func(string) bool {
		panic("malformed conversation metadata")
	})

	if app.screen != ScreenHome {
		t.Fatalf("screen = %v, want existing ScreenHome", app.screen)
	}
	if len(app.notifications) != 1 || app.notifications[0].Kind != "warning" {
		t.Fatalf("notifications = %#v, want one warning", app.notifications)
	}
}
