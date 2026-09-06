package storage

import "testing"

// The token-anomaly detector writes full conversation snapshots into a "debug"
// directory beside the workspace dirs. It must never be scanned as a workspace:
// doing so made every all-workspaces listing read gigabytes of snapshot JSON.
func TestDebugDirIsNotAWorkspace(t *testing.T) {
	if IsWorkspaceDir("debug") {
		t.Fatal("debug/ is treated as a workspace — listings will read every anomaly snapshot in it")
	}
}

// Guard against tightening the rule into something that hides real history.
// Legacy layouts left conversation directories named with UUIDs and with
// conversation IDs; those are not base64 of a path but must still be scanned.
func TestLegacyAndEncodedWorkspaceDirsStillScan(t *testing.T) {
	mustScan := []string{
		EncodeWorkspacePath("/home/rincon"),         // current layout
		EncodeWorkspacePath("/home/rincon/Work/Sw"), // current layout
		DefaultWorkspaceDir,                         // sentinel
		"20260320-141336-xan0xi",                    // legacy conversation-id dir
		"04ed5d2f-e45d-4abb-9eff-365103345010",      // legacy uuid dir
	}
	for _, name := range mustScan {
		if !IsWorkspaceDir(name) {
			t.Errorf("IsWorkspaceDir(%q) = false, want true — real conversations would be hidden", name)
		}
	}
}

func TestReservedPrefixDirsStillExcluded(t *testing.T) {
	for _, name := range []string{"", IndexDir, "_index", "-tmp"} {
		if IsWorkspaceDir(name) {
			t.Errorf("IsWorkspaceDir(%q) = true, want false", name)
		}
	}
}
