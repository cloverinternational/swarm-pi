// Package chrome defines dependency-light contracts shared by the Chrome runtime layers.
package chrome

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ExecutionState describes how far an operation progressed before an error.
type ExecutionState string

const (
	ExecutionNotStarted    ExecutionState = "not_started"
	ExecutionFailed        ExecutionState = "failed"
	ExecutionIndeterminate ExecutionState = "indeterminate"
)

// ErrorCode is a stable Chrome runtime error discriminator.
type ErrorCode string

const (
	ErrChromeNotFound            ErrorCode = "chrome_not_found"
	ErrUnsupportedHost           ErrorCode = "unsupported_host"
	ErrDefaultProfileUnavailable ErrorCode = "default_profile_unavailable"
	ErrExtensionMissing          ErrorCode = "extension_missing"
	ErrExtensionDisabled         ErrorCode = "extension_disabled"
	ErrBridgeAuthFailed          ErrorCode = "bridge_auth_failed"
	ErrProtocolIncompatible      ErrorCode = "protocol_incompatible"
	ErrProtocolMismatch          ErrorCode = "protocol_mismatch"
	ErrRuntimeDisabledByOperator ErrorCode = "runtime_disabled_by_operator"
	ErrLaunchFailed              ErrorCode = "launch_failed"
	ErrHandshakeTimeout          ErrorCode = "handshake_timeout"
	ErrSessionLaunching          ErrorCode = "session_launching"
	ErrSessionClosing            ErrorCode = "session_closing"
	ErrSessionClosed             ErrorCode = "session_closed"
	ErrWrongOwner                ErrorCode = "wrong_owner"
	ErrStaleGeneration           ErrorCode = "stale_generation"
	ErrTabNotOwned               ErrorCode = "tab_not_owned"
	ErrTabClosed                 ErrorCode = "tab_closed"
	ErrStaleRef                  ErrorCode = "stale_ref"
	ErrNavigationChangedDocument ErrorCode = "navigation_changed_document"
	ErrInvalidPattern            ErrorCode = "invalid_pattern"
	ErrInvalidArguments          ErrorCode = "invalid_arguments"
	ErrPermissionDenied          ErrorCode = "permission_denied"
	ErrTimeoutBeforeExecution    ErrorCode = "timeout_before_execution"
	ErrExecutionFailed           ErrorCode = "execution_failed"
	ErrExecutionIndeterminate    ErrorCode = "execution_indeterminate"
	ErrExecutionJournalFull      ErrorCode = "execution_journal_full"
	ErrTransportClosed           ErrorCode = "transport_closed"
	ErrCancelled                 ErrorCode = "cancelled"
	ErrUnsupported               ErrorCode = "unsupported"
)

type errorSpec struct {
	retryable bool
	execution ExecutionState
}

var errorSpecs = map[ErrorCode]errorSpec{
	ErrChromeNotFound:            {false, ExecutionNotStarted},
	ErrUnsupportedHost:           {false, ExecutionNotStarted},
	ErrDefaultProfileUnavailable: {false, ExecutionNotStarted},
	ErrExtensionMissing:          {false, ExecutionNotStarted},
	ErrExtensionDisabled:         {true, ExecutionNotStarted},
	ErrBridgeAuthFailed:          {false, ExecutionNotStarted},
	ErrProtocolIncompatible:      {false, ExecutionNotStarted},
	ErrProtocolMismatch:          {false, ExecutionNotStarted},
	ErrRuntimeDisabledByOperator: {true, ExecutionNotStarted},
	ErrLaunchFailed:              {true, ExecutionNotStarted},
	ErrHandshakeTimeout:          {true, ExecutionNotStarted},
	ErrSessionLaunching:          {true, ExecutionNotStarted},
	ErrSessionClosing:            {true, ExecutionNotStarted},
	ErrSessionClosed:             {true, ExecutionNotStarted},
	ErrWrongOwner:                {false, ExecutionNotStarted},
	ErrStaleGeneration:           {true, ExecutionNotStarted},
	ErrTabNotOwned:               {false, ExecutionNotStarted},
	ErrTabClosed:                 {true, ExecutionNotStarted},
	ErrStaleRef:                  {true, ExecutionNotStarted},
	ErrNavigationChangedDocument: {true, ExecutionIndeterminate},
	ErrInvalidPattern:            {false, ExecutionNotStarted},
	ErrInvalidArguments:          {false, ExecutionNotStarted},
	ErrPermissionDenied:          {false, ExecutionNotStarted},
	ErrTimeoutBeforeExecution:    {true, ExecutionNotStarted},
	ErrExecutionIndeterminate:    {false, ExecutionIndeterminate},
	ErrExecutionJournalFull:      {true, ExecutionNotStarted},
	ErrUnsupported:               {false, ExecutionNotStarted},
}

// Error is the canonical serializable Chrome failure.
type Error struct {
	Code       ErrorCode      `json:"code"`
	Message    string         `json:"message"`
	Retryable  bool           `json:"retryable"`
	Execution  ExecutionState `json:"execution"`
	RequestID  string         `json:"request_id,omitempty"`
	Generation uint64         `json:"generation,omitempty"`
	Details    map[string]any `json:"details"`
}

// NewError constructs an error using the normative defaults for code.
// Context-dependent retry/state values can be overridden with NewFlexibleError.
func NewError(code ErrorCode, message string) *Error {
	spec, ok := errorSpecs[code]
	if !ok {
		switch code {
		case ErrExecutionFailed:
			return NewFlexibleError(code, message, false, ExecutionFailed)
		case ErrTransportClosed:
			return NewFlexibleError(code, message, true, ExecutionNotStarted)
		case ErrCancelled:
			return NewFlexibleError(code, message, false, ExecutionNotStarted)
		default:
			return &Error{Code: code, Message: message, Details: map[string]any{}}
		}
	}
	return &Error{
		Code: code, Message: message, Retryable: spec.retryable,
		Execution: spec.execution, Details: map[string]any{},
	}
}

// NewFlexibleError constructs one of the context-dependent taxonomy entries.
func NewFlexibleError(code ErrorCode, message string, retryable bool, execution ExecutionState) *Error {
	return &Error{
		Code: code, Message: message, Retryable: retryable,
		Execution: execution, Details: map[string]any{},
	}
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("chrome %s: %s", e.Code, e.Message)
}

// Validate checks taxonomy, execution, and required serialization fields.
func (e *Error) Validate() error {
	if e == nil {
		return fmt.Errorf("chrome: nil error")
	}
	if e.Message == "" {
		return fmt.Errorf("chrome: error message is required")
	}
	if e.Details == nil {
		return fmt.Errorf("chrome: error details are required")
	}
	switch e.Execution {
	case ExecutionNotStarted, ExecutionFailed, ExecutionIndeterminate:
	default:
		return fmt.Errorf("chrome: invalid execution state %q", e.Execution)
	}
	if spec, ok := errorSpecs[e.Code]; ok {
		if e.Retryable != spec.retryable || e.Execution != spec.execution {
			return fmt.Errorf("chrome: %s must serialize retryable=%t execution=%s", e.Code, spec.retryable, spec.execution)
		}
		return nil
	}
	switch e.Code {
	case ErrExecutionFailed:
		if e.Execution != ExecutionFailed {
			return fmt.Errorf("chrome: execution_failed execution must be failed")
		}
		return nil
	case ErrTransportClosed:
		if !e.Retryable {
			return fmt.Errorf("chrome: transport_closed must serialize retryable=true")
		}
	case ErrCancelled:
	default:
		return fmt.Errorf("chrome: unknown error code %q", e.Code)
	}
	if e.Execution != ExecutionNotStarted && e.Execution != ExecutionIndeterminate {
		return fmt.Errorf("chrome: %s execution must be not_started or indeterminate", e.Code)
	}
	return nil
}

// UnmarshalJSON rejects unknown fields and invalid taxonomy combinations.
func (e *Error) UnmarshalJSON(data []byte) error {
	type plain Error
	var decoded plain
	if err := strictDecode(data, &decoded); err != nil {
		return err
	}
	*e = Error(decoded)
	return e.Validate()
}

func strictDecode(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("chrome: trailing JSON value")
		}
		return err
	}
	return nil
}
