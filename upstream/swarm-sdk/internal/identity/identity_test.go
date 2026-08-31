package identity

import (
	"strings"
	"testing"
)

// uniquenessIterations is the number of constructor calls exercised by
// each uniqueness test. It comfortably exceeds the 10,000 iteration
// minimum required to give strong statistical confidence that the
// crypto/rand-backed generator is not producing collisions or falling
// back to a predictable sequence.
const uniquenessIterations = 20000

func TestNewDaemonInstanceID_Unique(t *testing.T) {
	seen := make(map[DaemonInstanceID]struct{}, uniquenessIterations)
	for i := 0; i < uniquenessIterations; i++ {
		id := NewDaemonInstanceID()
		if id.IsZero() {
			t.Fatalf("iteration %d: NewDaemonInstanceID returned zero value", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("iteration %d: NewDaemonInstanceID produced duplicate value %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewClientSessionID_Unique(t *testing.T) {
	seen := make(map[ClientSessionID]struct{}, uniquenessIterations)
	for i := 0; i < uniquenessIterations; i++ {
		id := NewClientSessionID()
		if id.IsZero() {
			t.Fatalf("iteration %d: NewClientSessionID returned zero value", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("iteration %d: NewClientSessionID produced duplicate value %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewExecutionID_Unique(t *testing.T) {
	seen := make(map[ExecutionID]struct{}, uniquenessIterations)
	for i := 0; i < uniquenessIterations; i++ {
		id := NewExecutionID()
		if id.IsZero() {
			t.Fatalf("iteration %d: NewExecutionID returned zero value", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("iteration %d: NewExecutionID produced duplicate value %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewAttemptID_Unique(t *testing.T) {
	seen := make(map[AttemptID]struct{}, uniquenessIterations)
	for i := 0; i < uniquenessIterations; i++ {
		id := NewAttemptID()
		if id.IsZero() {
			t.Fatalf("iteration %d: NewAttemptID returned zero value", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("iteration %d: NewAttemptID produced duplicate value %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestDaemonInstanceID_RoundTrip(t *testing.T) {
	for i := 0; i < 100; i++ {
		orig := NewDaemonInstanceID()
		parsed, err := ParseDaemonInstanceID(orig.String())
		if err != nil {
			t.Fatalf("ParseDaemonInstanceID(%q) unexpected error: %v", orig.String(), err)
		}
		if parsed != orig {
			t.Fatalf("round-trip mismatch: original %q, parsed %q", orig, parsed)
		}
		if parsed.String() != orig.String() {
			t.Fatalf("String() round-trip mismatch: original %q, parsed %q", orig.String(), parsed.String())
		}
	}
}

func TestClientSessionID_RoundTrip(t *testing.T) {
	for i := 0; i < 100; i++ {
		orig := NewClientSessionID()
		parsed, err := ParseClientSessionID(orig.String())
		if err != nil {
			t.Fatalf("ParseClientSessionID(%q) unexpected error: %v", orig.String(), err)
		}
		if parsed != orig {
			t.Fatalf("round-trip mismatch: original %q, parsed %q", orig, parsed)
		}
	}
}

func TestExecutionID_RoundTrip(t *testing.T) {
	for i := 0; i < 100; i++ {
		orig := NewExecutionID()
		parsed, err := ParseExecutionID(orig.String())
		if err != nil {
			t.Fatalf("ParseExecutionID(%q) unexpected error: %v", orig.String(), err)
		}
		if parsed != orig {
			t.Fatalf("round-trip mismatch: original %q, parsed %q", orig, parsed)
		}
	}
}

func TestAttemptID_RoundTrip(t *testing.T) {
	for i := 0; i < 100; i++ {
		orig := NewAttemptID()
		parsed, err := ParseAttemptID(orig.String())
		if err != nil {
			t.Fatalf("ParseAttemptID(%q) unexpected error: %v", orig.String(), err)
		}
		if parsed != orig {
			t.Fatalf("round-trip mismatch: original %q, parsed %q", orig, parsed)
		}
	}
}

func TestParseDaemonInstanceID_RejectsEmpty(t *testing.T) {
	id, err := ParseDaemonInstanceID("")
	if err == nil {
		t.Fatalf("expected error for empty string, got nil (id=%q)", id)
	}
	if err != ErrEmptyIdentity {
		t.Fatalf("expected ErrEmptyIdentity, got %v", err)
	}
	if !id.IsZero() {
		t.Fatalf("expected zero value on error, got %q", id)
	}
}

func TestParseDaemonInstanceID_RejectsGarbage(t *testing.T) {
	cases := []string{
		"not-a-valid-id",
		"daemon_",
		"daemon_tooShort",
		"session_" + strings.Repeat("a", randomTokenHexLen),  // wrong tag
		strings.Repeat("g", randomTokenHexLen),               // no tag, non-hex-ish
		"daemon_" + strings.Repeat("z", randomTokenHexLen),   // right length, non-hex chars
		"daemon_" + strings.Repeat("a", randomTokenHexLen+2), // too long
		"DAEMON_" + strings.Repeat("a", randomTokenHexLen),   // wrong case tag
		"  ",
	}
	for _, c := range cases {
		id, err := ParseDaemonInstanceID(c)
		if err == nil {
			t.Fatalf("ParseDaemonInstanceID(%q): expected error, got nil (id=%q)", c, id)
		}
		if !id.IsZero() {
			t.Fatalf("ParseDaemonInstanceID(%q): expected zero value on error, got %q", c, id)
		}
	}
}

func TestParseClientSessionID_RejectsEmptyAndGarbage(t *testing.T) {
	if _, err := ParseClientSessionID(""); err != ErrEmptyIdentity {
		t.Fatalf("expected ErrEmptyIdentity for empty string, got %v", err)
	}
	if _, err := ParseClientSessionID("garbage"); err != ErrInvalidIdentity {
		t.Fatalf("expected ErrInvalidIdentity for garbage, got %v", err)
	}
	// Cross-type confusion: a valid ExecutionID string must not parse as
	// a ClientSessionID.
	exec := NewExecutionID()
	if _, err := ParseClientSessionID(exec.String()); err != ErrInvalidIdentity {
		t.Fatalf("expected ErrInvalidIdentity when parsing ExecutionID as ClientSessionID, got %v", err)
	}
}

func TestParseExecutionID_RejectsEmptyAndGarbage(t *testing.T) {
	if _, err := ParseExecutionID(""); err != ErrEmptyIdentity {
		t.Fatalf("expected ErrEmptyIdentity for empty string, got %v", err)
	}
	if _, err := ParseExecutionID("garbage"); err != ErrInvalidIdentity {
		t.Fatalf("expected ErrInvalidIdentity for garbage, got %v", err)
	}
	session := NewClientSessionID()
	if _, err := ParseExecutionID(session.String()); err != ErrInvalidIdentity {
		t.Fatalf("expected ErrInvalidIdentity when parsing ClientSessionID as ExecutionID, got %v", err)
	}
}

func TestParseAttemptID_RejectsEmptyAndGarbage(t *testing.T) {
	if _, err := ParseAttemptID(""); err != ErrEmptyIdentity {
		t.Fatalf("expected ErrEmptyIdentity for empty string, got %v", err)
	}
	if _, err := ParseAttemptID("garbage"); err != ErrInvalidIdentity {
		t.Fatalf("expected ErrInvalidIdentity for garbage, got %v", err)
	}
	daemon := NewDaemonInstanceID()
	if _, err := ParseAttemptID(daemon.String()); err != ErrInvalidIdentity {
		t.Fatalf("expected ErrInvalidIdentity when parsing DaemonInstanceID as AttemptID, got %v", err)
	}
}

func TestZeroValuesAreInvalid(t *testing.T) {
	var (
		d DaemonInstanceID
		c ClientSessionID
		e ExecutionID
		a AttemptID
	)
	if !d.IsZero() {
		t.Fatalf("zero DaemonInstanceID.IsZero() = false, want true")
	}
	if !c.IsZero() {
		t.Fatalf("zero ClientSessionID.IsZero() = false, want true")
	}
	if !e.IsZero() {
		t.Fatalf("zero ExecutionID.IsZero() = false, want true")
	}
	if !a.IsZero() {
		t.Fatalf("zero AttemptID.IsZero() = false, want true")
	}

	if _, err := ParseDaemonInstanceID(d.String()); err != ErrEmptyIdentity {
		t.Fatalf("parsing zero DaemonInstanceID.String(): expected ErrEmptyIdentity, got %v", err)
	}
	if _, err := ParseClientSessionID(c.String()); err != ErrEmptyIdentity {
		t.Fatalf("parsing zero ClientSessionID.String(): expected ErrEmptyIdentity, got %v", err)
	}
	if _, err := ParseExecutionID(e.String()); err != ErrEmptyIdentity {
		t.Fatalf("parsing zero ExecutionID.String(): expected ErrEmptyIdentity, got %v", err)
	}
	if _, err := ParseAttemptID(a.String()); err != ErrEmptyIdentity {
		t.Fatalf("parsing zero AttemptID.String(): expected ErrEmptyIdentity, got %v", err)
	}
}

func TestNonZeroConstructedValuesAreNotZero(t *testing.T) {
	if NewDaemonInstanceID().IsZero() {
		t.Fatalf("NewDaemonInstanceID() produced a zero value")
	}
	if NewClientSessionID().IsZero() {
		t.Fatalf("NewClientSessionID() produced a zero value")
	}
	if NewExecutionID().IsZero() {
		t.Fatalf("NewExecutionID() produced a zero value")
	}
	if NewAttemptID().IsZero() {
		t.Fatalf("NewAttemptID() produced a zero value")
	}
}
