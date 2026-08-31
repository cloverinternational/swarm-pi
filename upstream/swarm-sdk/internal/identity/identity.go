// Package identity defines the canonical execution identity value types
// shared across the swarm-attach architecture: DaemonInstanceID,
// ClientSessionID, ExecutionID, and AttemptID.
//
// This package is a dependency-free leaf: it imports only the standard
// library and MUST NOT import any other package in this module or
// workspace. Every other package that needs a stable, opaque, uniquely
// constructed identity value depends on this package instead of inventing
// its own ad-hoc string identifiers.
//
// Zero values are invalid. Each type's zero value (the empty string) does
// not represent a valid identity; callers MUST construct identities via
// the New*ID constructors (which generate cryptographically random,
// collision-resistant values) or via the Parse*ID functions (which
// validate a previously-serialized value produced by this package). Use
// the IsZero method to detect the invalid zero value before use.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrEmptyIdentity is returned by the Parse*ID functions when given an
// empty string.
var ErrEmptyIdentity = errors.New("identity: value must not be empty")

// ErrInvalidIdentity is returned by the Parse*ID functions when given a
// non-empty string that does not match this package's canonical identity
// format (a type-tag prefix followed by a fixed-length lowercase
// hex-encoded random token).
var ErrInvalidIdentity = errors.New("identity: value is not a valid identity")

// randomTokenBytes is the number of raw random bytes used to build each
// identity's random component. 16 bytes (128 bits) of cryptographically
// random data makes accidental collision practically impossible.
const randomTokenBytes = 16

// randomTokenHexLen is the length, in characters, of the hex-encoded
// random component of every identity value produced by this package.
const randomTokenHexLen = randomTokenBytes * 2

// newRandomToken returns a lowercase hex-encoded string of
// randomTokenBytes bytes of cryptographically random data. It panics if
// the system CSPRNG cannot be read, which indicates a fatally broken
// runtime environment; identity generation must never silently fall back
// to a weaker source of randomness.
func newRandomToken() string {
	buf := make([]byte, randomTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("identity: failed to read crypto/rand: %v", err))
	}
	return hex.EncodeToString(buf)
}

// parseTagged validates that s consists of the exact prefix tag followed
// by randomTokenHexLen lowercase hex characters, and returns the
// underlying value. It rejects empty input, mismatched prefixes, wrong
// lengths, and non-hex characters.
func parseTagged(tag, s string) (string, error) {
	if s == "" {
		return "", ErrEmptyIdentity
	}
	rest, ok := strings.CutPrefix(s, tag)
	if !ok {
		return "", ErrInvalidIdentity
	}
	if len(rest) != randomTokenHexLen {
		return "", ErrInvalidIdentity
	}
	if _, err := hex.DecodeString(rest); err != nil {
		return "", ErrInvalidIdentity
	}
	return s, nil
}

// ---------------------------------------------------------------------
// DaemonInstanceID identifies a single running daemon process instance.
// ---------------------------------------------------------------------

// daemonInstanceTag prefixes every DaemonInstanceID value produced by
// this package, distinguishing it from the other identity kinds and
// guarding ParseDaemonInstanceID against cross-type confusion.
const daemonInstanceTag = "daemon_"

// DaemonInstanceID is an opaque, unique identifier for a single running
// daemon process instance. The zero value is invalid; construct via
// NewDaemonInstanceID or ParseDaemonInstanceID.
type DaemonInstanceID string

// NewDaemonInstanceID generates a new, cryptographically random,
// collision-resistant DaemonInstanceID.
func NewDaemonInstanceID() DaemonInstanceID {
	return DaemonInstanceID(daemonInstanceTag + newRandomToken())
}

// String returns the canonical string representation of id.
func (id DaemonInstanceID) String() string {
	return string(id)
}

// IsZero reports whether id is the invalid zero value.
func (id DaemonInstanceID) IsZero() bool {
	return id == ""
}

// ParseDaemonInstanceID validates s as a previously-serialized
// DaemonInstanceID produced by this package. It returns ErrEmptyIdentity
// for an empty string and ErrInvalidIdentity for any non-empty string
// that does not match the canonical DaemonInstanceID format.
func ParseDaemonInstanceID(s string) (DaemonInstanceID, error) {
	v, err := parseTagged(daemonInstanceTag, s)
	if err != nil {
		return "", err
	}
	return DaemonInstanceID(v), nil
}

// ---------------------------------------------------------------------
// ClientSessionID identifies a single connected client session.
// ---------------------------------------------------------------------

// clientSessionTag prefixes every ClientSessionID value produced by this
// package.
const clientSessionTag = "session_"

// ClientSessionID is an opaque, unique identifier for a single connected
// client session. The zero value is invalid; construct via
// NewClientSessionID or ParseClientSessionID.
type ClientSessionID string

// NewClientSessionID generates a new, cryptographically random,
// collision-resistant ClientSessionID.
func NewClientSessionID() ClientSessionID {
	return ClientSessionID(clientSessionTag + newRandomToken())
}

// String returns the canonical string representation of id.
func (id ClientSessionID) String() string {
	return string(id)
}

// IsZero reports whether id is the invalid zero value.
func (id ClientSessionID) IsZero() bool {
	return id == ""
}

// ParseClientSessionID validates s as a previously-serialized
// ClientSessionID produced by this package. It returns ErrEmptyIdentity
// for an empty string and ErrInvalidIdentity for any non-empty string
// that does not match the canonical ClientSessionID format.
func ParseClientSessionID(s string) (ClientSessionID, error) {
	v, err := parseTagged(clientSessionTag, s)
	if err != nil {
		return "", err
	}
	return ClientSessionID(v), nil
}

// ---------------------------------------------------------------------
// ExecutionID identifies a single unit of work (an execution).
// ---------------------------------------------------------------------

// executionTag prefixes every ExecutionID value produced by this
// package.
const executionTag = "exec_"

// ExecutionID is an opaque, unique identifier for a single execution
// (unit of work). The zero value is invalid; construct via
// NewExecutionID or ParseExecutionID.
type ExecutionID string

// NewExecutionID generates a new, cryptographically random,
// collision-resistant ExecutionID.
func NewExecutionID() ExecutionID {
	return ExecutionID(executionTag + newRandomToken())
}

// String returns the canonical string representation of id.
func (id ExecutionID) String() string {
	return string(id)
}

// IsZero reports whether id is the invalid zero value.
func (id ExecutionID) IsZero() bool {
	return id == ""
}

// ParseExecutionID validates s as a previously-serialized ExecutionID
// produced by this package. It returns ErrEmptyIdentity for an empty
// string and ErrInvalidIdentity for any non-empty string that does not
// match the canonical ExecutionID format.
func ParseExecutionID(s string) (ExecutionID, error) {
	v, err := parseTagged(executionTag, s)
	if err != nil {
		return "", err
	}
	return ExecutionID(v), nil
}

// ---------------------------------------------------------------------
// AttemptID identifies a single attempt at executing a unit of work.
// ---------------------------------------------------------------------

// attemptTag prefixes every AttemptID value produced by this package.
const attemptTag = "attempt_"

// AttemptID is an opaque, unique identifier for a single attempt of an
// execution. The zero value is invalid; construct via NewAttemptID or
// ParseAttemptID.
type AttemptID string

// NewAttemptID generates a new, cryptographically random,
// collision-resistant AttemptID.
func NewAttemptID() AttemptID {
	return AttemptID(attemptTag + newRandomToken())
}

// String returns the canonical string representation of id.
func (id AttemptID) String() string {
	return string(id)
}

// IsZero reports whether id is the invalid zero value.
func (id AttemptID) IsZero() bool {
	return id == ""
}

// ParseAttemptID validates s as a previously-serialized AttemptID
// produced by this package. It returns ErrEmptyIdentity for an empty
// string and ErrInvalidIdentity for any non-empty string that does not
// match the canonical AttemptID format.
func ParseAttemptID(s string) (AttemptID, error) {
	v, err := parseTagged(attemptTag, s)
	if err != nil {
		return "", err
	}
	return AttemptID(v), nil
}
