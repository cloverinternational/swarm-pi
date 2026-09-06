package chat

import (
	"strings"
	"testing"
)

// TestPrepareForceSend_MergesQueueAndInterrupts proves the Ctrl+Enter force
// message path (prepareForceSend):
//   - interrupts an in-progress streaming turn (streamingMessage -> false), and
//   - pulls any messages the user had queued while the agent was busy
//     (pendingUserMessages) into the input AHEAD of whatever is currently typed,
//     then clears the queue.
//
// No live SDK/agent is required: streamingMessage=true is enough to make the
// path treat the turn as "busy/running" and take the interrupt branch.
func newForceSendApp() *App {
	a := &App{}
	a.textInput = NewSimpleInput()
	a.textInput.Focus()
	a.loadingIndicator = NewLoadingIndicator(LoadingSpinner, "", Theme{})
	a.spinner = NewSpinner()
	return a
}

func TestPrepareForceSend_MergesQueueAndInterrupts(t *testing.T) {
	a := newForceSendApp()

	// Simulate a busy agent (streaming) with two already-queued messages and a
	// third message currently typed in the input box.
	a.streamingMessage = true
	a.pendingUserMessages = []string{"first queued", "second queued"}
	a.textInput.SetValue("typed now")

	interrupted := a.prepareForceSend()

	if !interrupted {
		t.Fatalf("expected prepareForceSend to report an interrupt, got false")
	}
	if a.streamingMessage {
		t.Errorf("expected streamingMessage to be reset to false after force send")
	}
	if len(a.pendingUserMessages) != 0 {
		t.Errorf("expected pendingUserMessages to be drained, got %d: %v",
			len(a.pendingUserMessages), a.pendingUserMessages)
	}

	got := a.textInput.GetSubmitValue()
	want := "first queued\nsecond queued\ntyped now"
	if got != want {
		t.Errorf("merged input mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestPrepareForceSend_NoQueueKeepsInput proves that with no queued messages the
// current input is left untouched and, with nothing running, no interrupt is
// reported.
func TestPrepareForceSend_NoQueueKeepsInput(t *testing.T) {
	a := newForceSendApp()
	a.textInput.SetValue("just this")

	interrupted := a.prepareForceSend()

	if interrupted {
		t.Errorf("expected no interrupt when nothing is running")
	}
	if got := a.textInput.GetSubmitValue(); got != "just this" {
		t.Errorf("input should be unchanged, got %q", got)
	}
}

// TestPrepareForceSend_QueueOnlyNoTypedInput proves queued messages are pulled
// into the input even when the user has not typed anything new.
func TestPrepareForceSend_QueueOnlyNoTypedInput(t *testing.T) {
	a := newForceSendApp()
	a.streamingMessage = true
	a.pendingUserMessages = []string{"queued only"}
	a.textInput.SetValue("")

	a.prepareForceSend()

	if got := strings.TrimSpace(a.textInput.GetSubmitValue()); got != "queued only" {
		t.Errorf("expected queued message pulled into input, got %q", got)
	}
	if len(a.pendingUserMessages) != 0 {
		t.Errorf("expected queue drained, got %v", a.pendingUserMessages)
	}
}
