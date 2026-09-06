package chrome

import "testing"

func TestRequestTerminalStates(t *testing.T) {
	for _, state := range []RequestState{RequestCreated, RequestQueued, RequestSent, RequestAccepted, RequestStarted} {
		if state.IsTerminal() {
			t.Errorf("%s reported terminal", state)
		}
	}
	for _, state := range []RequestState{RequestCompleted, RequestRejected, RequestFailed} {
		if !state.IsTerminal() {
			t.Errorf("%s did not report terminal", state)
		}
	}
}
