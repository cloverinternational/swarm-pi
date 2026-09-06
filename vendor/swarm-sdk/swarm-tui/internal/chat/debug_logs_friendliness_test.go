package chat

import "testing"

// TestLogsFollowDefault verifies the logs view follows the newest logs by
// default (the common case), so users don't have to press G on every open.
func TestLogsFollowDefault(t *testing.T) {
	elv := NewEnhancedLogsView(1000)
	if !elv.FollowActive() {
		t.Fatal("expected autoFollow to be enabled by default")
	}
}

// TestCycleSeverity verifies the one-key severity cycle walks
// ALL -> INFO+ -> WARN+ -> ERROR+ -> ALL and enables exactly the right levels.
func TestCycleSeverity(t *testing.T) {
	elv := NewEnhancedLogsView(1000)

	if got := elv.SeverityLabel(); got != "ALL" {
		t.Fatalf("initial severity label = %q, want ALL", got)
	}

	type want struct {
		label                                 string
		trace, debug, info, warn, errr, fatal bool
	}
	steps := []want{
		{"INFO+", false, false, true, true, true, true},
		{"WARN+", false, false, false, true, true, true},
		{"ERROR+", false, false, false, false, true, true},
		{"ALL", true, true, true, true, true, true},
	}
	for i, w := range steps {
		elv.CycleSeverity()
		f := elv.filter
		if elv.SeverityLabel() != w.label {
			t.Fatalf("step %d: label = %q, want %q", i, elv.SeverityLabel(), w.label)
		}
		got := []bool{
			f.EnabledLevels[LogLevelTrace],
			f.EnabledLevels[LogLevelDebug],
			f.EnabledLevels[LogLevelInfo],
			f.EnabledLevels[LogLevelWarn],
			f.EnabledLevels[LogLevelError],
			f.EnabledLevels[LogLevelFatal],
		}
		exp := []bool{w.trace, w.debug, w.info, w.warn, w.errr, w.fatal}
		for j := range got {
			if got[j] != exp[j] {
				t.Fatalf("step %d (%s): level[%d] = %v, want %v", i, w.label, j, got[j], exp[j])
			}
		}
		// Unparsed lines must never be hidden by the severity cycle.
		if !f.EnabledLevels[LogLevelUnknown] {
			t.Fatalf("step %d (%s): unknown level must stay enabled", i, w.label)
		}
	}

	// ResetSeverity returns to ALL.
	elv.CycleSeverity() // -> INFO+
	elv.ResetSeverity()
	if elv.SeverityLabel() != "ALL" {
		t.Fatalf("after ResetSeverity label = %q, want ALL", elv.SeverityLabel())
	}
}

// TestLogSearchNavigation verifies '/' search match discovery and n/N cycling.
func TestLogSearchNavigation(t *testing.T) {
	elv := NewEnhancedLogsView(1000)
	elv.buffer.Add(LogEntry{Level: LogLevelInfo, Raw: "apple one"})   // idx 0
	elv.buffer.Add(LogEntry{Level: LogLevelInfo, Raw: "banana two"})  // idx 1
	elv.buffer.Add(LogEntry{Level: LogLevelInfo, Raw: "APPLE three"}) // idx 2 (case-insensitive)
	elv.buffer.Add(LogEntry{Level: LogLevelInfo, Raw: "cherry four"}) // idx 3

	elv.searchQuery = "apple"
	elv.recomputeMatches()

	if cur, total := elv.MatchInfo(); cur != 1 || total != 2 {
		t.Fatalf("MatchInfo = (%d,%d), want (1,2)", cur, total)
	}
	if len(elv.matches) != 2 || elv.matches[0] != 0 || elv.matches[1] != 2 {
		t.Fatalf("matches = %v, want [0 2]", elv.matches)
	}

	elv.NextMatch()
	if cur, _ := elv.MatchInfo(); cur != 2 {
		t.Fatalf("after NextMatch cur = %d, want 2", cur)
	}
	elv.NextMatch() // wraps
	if cur, _ := elv.MatchInfo(); cur != 1 {
		t.Fatalf("after wrap cur = %d, want 1", cur)
	}
	elv.PrevMatch() // wraps back
	if cur, _ := elv.MatchInfo(); cur != 2 {
		t.Fatalf("after PrevMatch cur = %d, want 2", cur)
	}

	elv.ClearSearch()
	if cur, total := elv.MatchInfo(); cur != 0 || total != 0 {
		t.Fatalf("after ClearSearch MatchInfo = (%d,%d), want (0,0)", cur, total)
	}
	if elv.SearchQuery() != "" || elv.SearchActive() {
		t.Fatal("ClearSearch must reset query and searching state")
	}
}

// TestSearchRespectsLevelFilter verifies match indices line up with the
// displayed (level-filtered) list, not raw buffer positions.
func TestSearchRespectsLevelFilter(t *testing.T) {
	elv := NewEnhancedLogsView(1000)
	elv.buffer.Add(LogEntry{Level: LogLevelDebug, Raw: "target debug"}) // hidden at WARN+
	elv.buffer.Add(LogEntry{Level: LogLevelWarn, Raw: "target warn"})   // shown, display idx 0
	elv.buffer.Add(LogEntry{Level: LogLevelError, Raw: "target error"}) // shown, display idx 1

	// Cycle to WARN+ so the debug line is filtered out of the display.
	elv.severityMode = 1
	elv.CycleSeverity() // -> WARN+
	if elv.SeverityLabel() != "WARN+" {
		t.Fatalf("severity = %q, want WARN+", elv.SeverityLabel())
	}

	elv.searchQuery = "target"
	elv.recomputeMatches()
	if len(elv.matches) != 2 || elv.matches[0] != 0 || elv.matches[1] != 1 {
		t.Fatalf("matches = %v, want [0 1] (display-order, excluding filtered debug line)", elv.matches)
	}
}
