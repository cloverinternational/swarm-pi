package builtin

import (
	"strings"
	"testing"
)

// TestBuildContextPrefix_IncludesReportingDirective verifies the sub-agent task
// prefix carries the strong reporting contract (modeled on Claude Code's
// buildChildMessage). The directive must ride in the task PREFIX — not just the
// system prompt — because OAuth sub-agents get an empty system prompt, so the
// task message is the only place instructions reliably reach them.
func TestBuildContextPrefix_IncludesReportingDirective(t *testing.T) {
	prefix := buildContextPrefix("research-agent")

	mustContain := []string{
		"[REPORTING DIRECTIVE]",
		"[/REPORTING DIRECTIVE]",
		"report ONCE at the end",
		"only deliverable",
		"under 500 words",
		// Tool-call hygiene (doom-loop guard) must still be present.
		"[TOOL CALL HYGIENE]",
		"SAME error twice",
		// Task marker terminates the prefix.
		"[TASK]",
	}
	for _, s := range mustContain {
		if !strings.Contains(prefix, s) {
			t.Errorf("buildContextPrefix missing %q\n---\n%s", s, prefix)
		}
	}

	// The reporting directive must come before the [TASK] marker so it frames
	// the task rather than trailing it.
	if di, ti := strings.Index(prefix, "[REPORTING DIRECTIVE]"), strings.Index(prefix, "[TASK]"); di < 0 || ti < 0 || di > ti {
		t.Errorf("expected [REPORTING DIRECTIVE] before [TASK] (directive=%d, task=%d)", di, ti)
	}
}
