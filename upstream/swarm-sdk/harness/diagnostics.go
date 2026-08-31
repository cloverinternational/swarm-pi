package harness

import (
	"sort"
	"strings"
)

// Severity classifies a diagnostic. Phase 1 only emits errors, but the field is
// modeled so warnings can be added without breaking the wire shape.
type Severity string

const (
	// SeverityError marks a fatal validation/resolution problem.
	SeverityError Severity = "error"
)

// Diagnostic is a single structured problem with a compiled harness document.
//
// A Diagnostic is safe to print, log, and serialize: it carries a stable machine
// Code, a human Message, and the SourcePath (which manifest file) plus FieldPath
// (which logical field) it concerns. It NEVER contains a resolved secret value
// or resolved prompt text — only field identifiers and non-sensitive context.
type Diagnostic struct {
	Severity   Severity `json:"severity"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	SourcePath string   `json:"sourcePath,omitempty"`
	FieldPath  string   `json:"fieldPath,omitempty"`
}

// Error implements error for a single diagnostic.
func (d Diagnostic) Error() string {
	var b strings.Builder
	b.WriteString(string(d.Severity))
	b.WriteString(": ")
	if d.SourcePath != "" {
		b.WriteString(d.SourcePath)
		b.WriteString(": ")
	}
	if d.FieldPath != "" {
		b.WriteString(d.FieldPath)
		b.WriteString(": ")
	}
	b.WriteString(d.Message)
	if d.Code != "" {
		b.WriteString(" [")
		b.WriteString(d.Code)
		b.WriteString("]")
	}
	return b.String()
}

// newDiag builds an error-severity diagnostic. Callers pass only non-secret
// text; there is intentionally no parameter that could carry a resolved value.
func newDiag(code, fieldPath, message, sourcePath string) Diagnostic {
	return Diagnostic{
		Severity:   SeverityError,
		Code:       code,
		Message:    message,
		SourcePath: sourcePath,
		FieldPath:  fieldPath,
	}
}

// Diagnostics is an ordered collection of diagnostics that itself implements
// error, so compile entry points can return it directly.
type Diagnostics []Diagnostic

// HasErrors reports whether any error-severity diagnostic is present.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Error implements error by joining every diagnostic on its own line.
func (ds Diagnostics) Error() string {
	switch len(ds) {
	case 0:
		return "no diagnostics"
	case 1:
		return ds[0].Error()
	}
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, d.Error())
	}
	return strings.Join(parts, "\n")
}

// sorted returns a deterministically ordered copy (by field path, then code)
// so tests and goldens do not depend on discovery order.
func (ds Diagnostics) sorted() Diagnostics {
	out := make(Diagnostics, len(ds))
	copy(out, ds)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FieldPath != out[j].FieldPath {
			return out[i].FieldPath < out[j].FieldPath
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// AsDiagnostics returns the Diagnostics carried by err, if any.
func AsDiagnostics(err error) (Diagnostics, bool) {
	switch v := err.(type) {
	case Diagnostics:
		return v, true
	case Diagnostic:
		return Diagnostics{v}, true
	default:
		return nil, false
	}
}
