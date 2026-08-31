// Package toolout carries the typed, structured outcome of a tool execution.
//
// # Why this package exists
//
// Before it, a tool's terminal facts survived only as prose inside the tool's
// text output — a sampling recovered exit codes about 22 % of the time. Any
// consumer wanting an objective answer to "did this command fail?" or "which
// file did this edit actually touch?" had to regex the tool's rendered text,
// and had to special-case every editing tool (Write/Edit carry file_path in
// their params; apply_patch buries paths inside a patch envelope).
//
// # Why it is its own package
//
// It is deliberately dependency-free (stdlib only, and in fact imports
// nothing). Both internal/tools (which produces outcomes) and internal/hooks
// (which exposes them on the AfterTool event) import it. internal/tools
// already depends transitively on internal/hooks, so internal/hooks can never
// import internal/tools; a shared leaf package is the only way to give the
// producing and consuming sides the same *typed* view without an import cycle.
//
// # Contract
//
// An Outcome is strictly observational. Producing one must never change what
// a tool does, block it, or alter its result. Every field is optional and
// every consumer must treat a nil *Outcome as "this tool does not report a
// structured outcome (yet)" — never as "the tool succeeded".
package toolout

// Status is the tool's own typed verdict on its execution. It is deliberately
// distinct from "the tool returned text that mentions an error": only the tool
// itself, at the site where it holds the real exit status or the real write
// result, may set it.
type Status string

const (
	// StatusUnknown is the zero value: the tool did not report a verdict.
	// Consumers must not read this as success.
	StatusUnknown Status = ""
	// StatusSuccess means the tool completed its work as requested.
	StatusSuccess Status = "success"
	// StatusFailure means the tool positively established a failure — a
	// non-zero exit status, a failed write, a timeout. This is the signal
	// "rung-2 evidence" is computed from.
	StatusFailure Status = "failure"
)

// FileOp classifies what a mutating tool did to one path.
type FileOp string

const (
	// FileOpCreate — the path did not exist and now does.
	FileOpCreate FileOp = "create"
	// FileOpUpdate — the path existed and its contents changed.
	FileOpUpdate FileOp = "update"
	// FileOpDelete — the path existed and no longer does.
	FileOpDelete FileOp = "delete"
	// FileOpMove — content was written to Path and removed from FromPath.
	FileOpMove FileOp = "move"
)

// FileEffect is one path a mutating tool actually touched, recorded by the
// tool at the point it performed the mutation — not inferred later from
// parameters or parsed out of the tool's prose.
type FileEffect struct {
	// Path is the absolute path that was written, created, or removed.
	Path string `json:"path"`

	// Op is what happened to Path.
	Op FileOp `json:"op"`

	// BytesWritten is the size of the content committed to Path. It is 0 for
	// FileOpDelete, where no bytes were written.
	BytesWritten int64 `json:"bytes_written,omitempty"`

	// FromPath is the previous location, set only for FileOpMove.
	FromPath string `json:"from_path,omitempty"`

	// PreBlobSHA1 / PostBlobSHA1 are git-compatible blob hashes
	// (sha1("blob "+len+"\x00"+content)) of the content before and after
	// this mutation, when the producing tool already held those bytes in
	// memory at the moment it committed the change (see
	// internal/tools/forge/apply_patch.go:committedEffects, which computes
	// both from plannedChange.oldContent/newContent — no extra file read).
	//
	// Both are optional and additive. Empty means "not established by the
	// tool" — never "empty content": an empty file has a real, non-empty
	// blob hash. PreBlobSHA1 is naturally empty for a create (there was no
	// prior content); PostBlobSHA1 is naturally empty for a delete (nothing
	// survives to hash). A consumer that needs PostBlobSHA1 for a tool that
	// did not supply one may fall back to a bounded, OFF-THE-HOT-PATH read
	// (see internal/bench, which does this only on its own background
	// writer goroutine, never synchronously on the tool-call path).
	PreBlobSHA1  string `json:"pre_blob_sha1,omitempty"`
	PostBlobSHA1 string `json:"post_blob_sha1,omitempty"`
}

// Outcome is the typed, structured result of one tool execution.
//
// The zero value reports nothing. A nil *Outcome is always valid and every
// method below is nil-safe, so consumers never need a nil check before asking
// a question.
type Outcome struct {
	// Tool is the registered tool name. Tools may leave it empty; the hook
	// adapter backfills it from the tool call, which always knows the name.
	Tool string `json:"tool,omitempty"`

	// Status is the tool's own verdict. See Status.
	Status Status `json:"status,omitempty"`

	// ExitCode is the real process exit status for command/shell tools. It is
	// a pointer on purpose: a plain int cannot distinguish "exited 0" from
	// "this tool has no exit code", and conflating those is exactly the
	// ambiguity this type exists to remove.
	ExitCode *int `json:"exit_code,omitempty"`

	// TimedOut reports that the tool's own deadline fired.
	TimedOut bool `json:"timed_out,omitempty"`

	// DurationMS is how long the execution took, in milliseconds.
	DurationMS int64 `json:"duration_ms,omitempty"`

	// Effects are the file mutations the tool actually performed, in the
	// order it performed them.
	Effects []FileEffect `json:"effects,omitempty"`
}

// Command builds the outcome of a command/shell execution. Status is derived
// from the real exit status, so a command that prints "error" but exits 0 is
// success, and a command that prints nothing but exits 1 is failure.
func Command(exitCode int, durationMS int64, timedOut bool) *Outcome {
	code := exitCode
	status := StatusSuccess
	if exitCode != 0 || timedOut {
		status = StatusFailure
	}
	return &Outcome{
		Status:     status,
		ExitCode:   &code,
		TimedOut:   timedOut,
		DurationMS: durationMS,
	}
}

// Files builds the outcome of a successful file mutation. Callers pass only
// effects that were actually committed.
func Files(effects ...FileEffect) *Outcome {
	return &Outcome{Status: StatusSuccess, Effects: effects}
}

// ExitCodeValue returns the exit code and whether one was reported.
func (o *Outcome) ExitCodeValue() (int, bool) {
	if o == nil || o.ExitCode == nil {
		return 0, false
	}
	return *o.ExitCode, true
}

// Failed reports a positively established failure. A missing outcome is not a
// failure — it is an absence of evidence, and callers computing failure rates
// must keep those two apart.
func (o *Outcome) Failed() bool {
	return o != nil && o.Status == StatusFailure
}

// Succeeded reports a positively established success.
func (o *Outcome) Succeeded() bool {
	return o != nil && o.Status == StatusSuccess
}

// Paths returns the paths this tool touched, in effect order. It returns nil
// when the tool reported no file effects, so `len(o.Paths()) == 0` is a safe
// test for "this tool touched nothing it told us about".
func (o *Outcome) Paths() []string {
	if o == nil || len(o.Effects) == 0 {
		return nil
	}
	paths := make([]string, 0, len(o.Effects))
	for _, e := range o.Effects {
		paths = append(paths, e.Path)
	}
	return paths
}

// BytesWritten sums the bytes committed across all effects.
func (o *Outcome) BytesWritten() int64 {
	if o == nil {
		return 0
	}
	var total int64
	for _, e := range o.Effects {
		total += e.BytesWritten
	}
	return total
}

// Clone returns a deep copy. Hook adapters clone before publishing an outcome
// on an event so an observing hook can never mutate the live tool result —
// observation must not be able to change execution.
func (o *Outcome) Clone() *Outcome {
	if o == nil {
		return nil
	}
	dup := *o
	if o.ExitCode != nil {
		code := *o.ExitCode
		dup.ExitCode = &code
	}
	if o.Effects != nil {
		dup.Effects = make([]FileEffect, len(o.Effects))
		copy(dup.Effects, o.Effects)
	}
	return &dup
}
