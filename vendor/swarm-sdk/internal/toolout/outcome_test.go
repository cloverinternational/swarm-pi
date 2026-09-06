package toolout

import "testing"

// TestCommandDerivesStatusFromExitCodeOnly pins the core distinction: the
// verdict comes from the process exit status, never from output text.
func TestCommandDerivesStatusFromExitCodeOnly(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		timedOut bool
		want     Status
	}{
		{"zero exit is success", 0, false, StatusSuccess},
		{"non-zero exit is failure", 1, false, StatusFailure},
		{"unusual exit code is failure", 127, false, StatusFailure},
		{"timeout is failure even at exit 0", 0, true, StatusFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Command(tc.exitCode, 12, tc.timedOut)
			if got.Status != tc.want {
				t.Fatalf("status = %q, want %q", got.Status, tc.want)
			}
			code, ok := got.ExitCodeValue()
			if !ok {
				t.Fatal("command outcome must always report an exit code")
			}
			if code != tc.exitCode {
				t.Fatalf("exit code = %d, want %d", code, tc.exitCode)
			}
			if got.DurationMS != 12 {
				t.Fatalf("duration = %d, want 12", got.DurationMS)
			}
		})
	}
}

// TestNilOutcomeIsAbsenceNotSuccess is the invariant every consumer depends
// on: a tool that reports nothing must never be counted as a pass or a fail.
func TestNilOutcomeIsAbsenceNotSuccess(t *testing.T) {
	var o *Outcome
	if o.Succeeded() {
		t.Fatal("nil outcome reported success")
	}
	if o.Failed() {
		t.Fatal("nil outcome reported failure")
	}
	if _, ok := o.ExitCodeValue(); ok {
		t.Fatal("nil outcome reported an exit code")
	}
	if got := o.Paths(); got != nil {
		t.Fatalf("nil outcome reported paths %v", got)
	}
	if got := o.BytesWritten(); got != 0 {
		t.Fatalf("nil outcome reported %d bytes written", got)
	}
	if got := o.Clone(); got != nil {
		t.Fatalf("cloning nil outcome produced %+v", got)
	}
}

// TestFileOutcomeExposesPathsAndBytes covers the accessors consumers use
// instead of walking Effects by hand.
func TestFileOutcomeExposesPathsAndBytes(t *testing.T) {
	o := Files(
		FileEffect{Path: "/w/a.go", Op: FileOpCreate, BytesWritten: 10},
		FileEffect{Path: "/w/b.go", Op: FileOpUpdate, BytesWritten: 32},
		FileEffect{Path: "/w/c.go", Op: FileOpDelete},
	)
	if !o.Succeeded() {
		t.Fatalf("status = %q, want %q", o.Status, StatusSuccess)
	}
	want := []string{"/w/a.go", "/w/b.go", "/w/c.go"}
	got := o.Paths()
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if o.BytesWritten() != 42 {
		t.Fatalf("bytes written = %d, want 42", o.BytesWritten())
	}
	if _, ok := o.ExitCodeValue(); ok {
		t.Fatal("a file outcome must not report an exit code")
	}
}

// TestCloneIsDeep guards the "observation cannot change execution" contract:
// the hook adapter publishes a clone, so mutating the published copy must
// leave the original untouched — including through the ExitCode pointer and
// the Effects backing array.
func TestCloneIsDeep(t *testing.T) {
	original := Command(3, 100, false)
	original.Effects = []FileEffect{{Path: "/w/a.go", Op: FileOpUpdate, BytesWritten: 5}}

	clone := original.Clone()
	*clone.ExitCode = 99
	clone.Status = StatusSuccess
	clone.Effects[0].Path = "/w/hijacked.go"
	clone.Effects = append(clone.Effects, FileEffect{Path: "/w/extra.go"})

	if code, _ := original.ExitCodeValue(); code != 3 {
		t.Fatalf("original exit code mutated to %d, want 3", code)
	}
	if !original.Failed() {
		t.Fatalf("original status mutated to %q", original.Status)
	}
	if original.Effects[0].Path != "/w/a.go" {
		t.Fatalf("original effect path mutated to %q", original.Effects[0].Path)
	}
	if len(original.Effects) != 1 {
		t.Fatalf("original effects grew to %d", len(original.Effects))
	}
}
