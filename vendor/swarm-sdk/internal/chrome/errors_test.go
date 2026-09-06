package chrome

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestErrorTaxonomyAndSerialization(t *testing.T) {
	tests := []struct {
		code      ErrorCode
		retryable bool
		execution ExecutionState
	}{
		{ErrChromeNotFound, false, ExecutionNotStarted},
		{ErrExtensionDisabled, true, ExecutionNotStarted},
		{ErrNavigationChangedDocument, true, ExecutionIndeterminate},
		{ErrExecutionFailed, false, ExecutionFailed},
		{ErrExecutionIndeterminate, false, ExecutionIndeterminate},
		{ErrTransportClosed, true, ExecutionNotStarted},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			value := NewError(test.code, "safe message")
			value.RequestID = "req_fixture"
			value.Generation = 3
			if value.Retryable != test.retryable || value.Execution != test.execution {
				t.Fatalf("got retryable=%t execution=%s", value.Retryable, value.Execution)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Error
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Code != test.code || decoded.Details == nil {
				t.Fatalf("round trip = %#v", decoded)
			}
			var asError error = &decoded
			var typed *Error
			if !errors.As(asError, &typed) {
				t.Fatal("typed Chrome error was not preserved")
			}
		})
	}
}

func TestTransportClosedAllowsOnlyNormativeCombinations(t *testing.T) {
	for _, execution := range []ExecutionState{ExecutionNotStarted, ExecutionIndeterminate} {
		value := NewFlexibleError(ErrTransportClosed, "connection closed", true, execution)
		if err := value.Validate(); err != nil {
			t.Errorf("retryable transport_closed with execution=%s: %v", execution, err)
		}
	}
	for _, execution := range []ExecutionState{ExecutionNotStarted, ExecutionIndeterminate} {
		value := NewFlexibleError(ErrTransportClosed, "connection closed", false, execution)
		if err := value.Validate(); err == nil {
			t.Errorf("accepted non-retryable transport_closed with execution=%s", execution)
		}
	}
}

func TestCancelledRemainsContextDependent(t *testing.T) {
	for _, retryable := range []bool{false, true} {
		for _, execution := range []ExecutionState{ExecutionNotStarted, ExecutionIndeterminate} {
			value := NewFlexibleError(ErrCancelled, "request cancelled", retryable, execution)
			if err := value.Validate(); err != nil {
				t.Errorf("cancelled retryable=%t execution=%s: %v", retryable, execution, err)
			}
		}
	}
}

func TestErrorRejectsInvalidCombinationsAndUnknownFields(t *testing.T) {
	bad := []string{
		`{"code":"chrome_not_found","message":"x","retryable":true,"execution":"not_started","details":{}}`,
		`{"code":"transport_closed","message":"x","retryable":true,"execution":"failed","details":{}}`,
		`{"code":"transport_closed","message":"x","retryable":false,"execution":"not_started","details":{}}`,
		`{"code":"not_real","message":"x","retryable":false,"execution":"not_started","details":{}}`,
		`{"code":"chrome_not_found","message":"x","retryable":false,"execution":"not_started","details":{},"extra":1}`,
		`{"code":"chrome_not_found","message":"x","retryable":false,"execution":"not_started"}`,
	}
	for _, raw := range bad {
		var decoded Error
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
