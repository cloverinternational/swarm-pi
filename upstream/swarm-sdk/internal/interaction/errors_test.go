package interaction

import (
	"errors"
	"testing"
)

func TestErrorOutcomeIsMachineReadable(t *testing.T) {
	cause := errors.New("send failed")
	err := NewError(OutcomeDeliveryFailure, cause)

	if got := OutcomeOf(err); got != OutcomeDeliveryFailure {
		t.Fatalf("OutcomeOf() = %q, want %q", got, OutcomeDeliveryFailure)
	}
	if !errors.Is(err, cause) {
		t.Fatal("interaction error must preserve its cause")
	}
}
