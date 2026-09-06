package chat

import (
	"strings"
	"testing"
	"time"
)

func TestSplitPlayableWords(t *testing.T) {
	words := splitPlayableWords(" hello\nworld\tagain ")
	want := []string{"hello", "world", "again"}
	if len(words) != len(want) {
		t.Fatalf("len(words)=%d want %d (%v)", len(words), len(want), words)
	}
	for i := range want {
		if words[i] != want[i] {
			t.Fatalf("words[%d]=%q want %q", i, words[i], want[i])
		}
	}
}

func TestLatestPlayableAgentMessageIndex(t *testing.T) {
	app := NewApp()
	app.messages = []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "first answer"},
		{Role: "system", Content: "status"},
		{Role: "assistant", Content: ""},
	}
	if got := app.latestPlayableAgentMessageIndex(); got != 1 {
		t.Fatalf("latestPlayableAgentMessageIndex()=%d want 1", got)
	}
}

func TestAdvanceWordPlaybackHonorsIntervalAndStops(t *testing.T) {
	app := NewApp()
	app.messages = []Message{{Role: "assistant", Content: "one two"}}
	app.wordPlayback = wordPlaybackState{
		Active:       true,
		MessageIdx:   0,
		WordIdx:      0,
		Words:        []string{"one", "two"},
		LastStepAt:   time.Unix(10, 0),
		StepInterval: time.Second,
	}
	if app.advanceWordPlayback(time.Unix(10, int64(500*time.Millisecond))) {
		t.Fatal("advanceWordPlayback advanced before interval elapsed")
	}
	if app.wordPlayback.WordIdx != 0 {
		t.Fatalf("WordIdx=%d want 0", app.wordPlayback.WordIdx)
	}
	if !app.advanceWordPlayback(time.Unix(11, 0)) {
		t.Fatal("advanceWordPlayback did not advance after interval")
	}
	if app.wordPlayback.WordIdx != 1 || !app.wordPlayback.Active {
		t.Fatalf("after first advance: idx=%d active=%v, want idx=1 active=true", app.wordPlayback.WordIdx, app.wordPlayback.Active)
	}
	if !app.advanceWordPlayback(time.Unix(12, 0)) {
		t.Fatal("advanceWordPlayback did not report final change")
	}
	if app.wordPlayback.Active {
		t.Fatal("word playback should stop at end")
	}
}

func TestRenderWordPlaybackLineForTargetMessage(t *testing.T) {
	app := NewApp()
	app.wordPlayback = wordPlaybackState{
		Active:     true,
		MessageIdx: 0,
		WordIdx:    1,
		Words:      []string{"hello", "world"},
	}
	msg := Message{Role: "assistant", Content: "hello world"}
	lines := app.renderMessageListWithContext([]Message{msg}, app.NewSingleMessageContext(0, 120, false))
	rendered := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(rendered, "Reading 2/2") || !strings.Contains(rendered, "world") || !strings.Contains(rendered, "Alt+R pause") {
		t.Fatalf("expected playback line in rendered output, got:\n%s", rendered)
	}
}

func TestRenderWordPlaybackLineOnlyForTargetMessage(t *testing.T) {
	app := NewApp()
	app.wordPlayback = wordPlaybackState{
		Active:     true,
		MessageIdx: 1,
		WordIdx:    0,
		Words:      []string{"target"},
	}
	msg := Message{Role: "assistant", Content: "not target"}
	lines := app.renderMessageListWithContext([]Message{msg}, app.NewSingleMessageContext(0, 120, false))
	rendered := stripANSI(strings.Join(lines, "\n"))
	if strings.Contains(rendered, "Reading") {
		t.Fatalf("did not expect playback line for non-target message, got:\n%s", rendered)
	}
}

func TestPlayableMessageTextUsesOnlyContentBlocks(t *testing.T) {
	msg := &Message{
		Role:    "assistant",
		Content: "legacy fallback should not be used when ordered blocks exist",
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Primera parte del asistente."},
			{Type: "tool_call", Content: "NO reproducir tool call"},
			{Type: "tool_result", Content: "NO reproducir tool result"},
			{Type: "thinking", Content: "NO reproducir thinking"},
			{Type: "content", Content: "Segunda parte del asistente."},
		},
	}

	got := playableMessageText(msg)
	if !strings.Contains(got, "Primera parte del asistente.") || !strings.Contains(got, "Segunda parte del asistente.") {
		t.Fatalf("playableMessageText()=%q, want assistant content blocks", got)
	}
	for _, forbidden := range []string{"tool call", "tool result", "thinking", "legacy fallback"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("playableMessageText()=%q includes forbidden %q", got, forbidden)
		}
	}
}
