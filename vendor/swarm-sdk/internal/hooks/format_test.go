package hooks

import (
	"strings"
	"testing"
)

func TestWrapReminderCanonicalEnvelope(t *testing.T) {
	got := WrapReminder(`hook"one`, "nudge", 7, "  do work  ")
	want := `<system-reminder source="hook&#34;one" kind="nudge" seq="7">do work</system-reminder>`
	if got != want {
		t.Fatalf("WrapReminder() = %q, want %q", got, want)
	}
}

func TestFormatHookContextNormalizesLegacyEnvelope(t *testing.T) {
	got := FormatHookContext("legacy-hook", "<system-reminder>hello</system-reminder>")
	for _, fragment := range []string{`source="legacy-hook"`, `kind="context"`, `seq="`, ">hello</system-reminder>"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("FormatHookContext() = %q, missing %q", got, fragment)
		}
	}
}
