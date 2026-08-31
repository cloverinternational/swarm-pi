package agent

import (
	"sync"
	"testing"
)

// TestSnapshotToolParametersIsolatesFromLaterMutation proves the fix for
// issue #189: a fatal "concurrent map read and map write" crash
// (runtime/internal/maps.fatal) in swarm-tui's tool activity describer
// (chat.stringParam -> chat.(*DefaultToolActivityDescriber).Describe ->
// chat.(*ActivityStateManager).BeginTool).
//
// Root cause: ToolCallUpdate.Parameters used to alias the exact
// toolCall.Parameters map that a tool implementation (or a before/after
// hook) goes on to mutate in place immediately after the update is handed
// off asynchronously to the UI. This test proves the map handed to a
// ToolCallUpdate consumer is now a snapshot: mutating the ORIGINAL map
// after snapshotting must never be visible through the snapshot, which is
// exactly the property that removes the shared-mutable-map race.
func TestSnapshotToolParametersIsolatesFromLaterMutation(t *testing.T) {
	original := map[string]any{"path": "/a/b.txt", "recursive": true}

	snapshot := snapshotToolParameters(original)

	// Simulate what a tool implementation or hook does today: mutate the
	// caller's params map in place after the ToolCallUpdate has already
	// been emitted (e.g. filling a default or attaching parsed args).
	original["path"] = "/mutated/after/snapshot.txt"
	original["new_key_added_during_execution"] = "should not leak into snapshot"
	delete(original, "recursive")

	if snapshot["path"] != "/a/b.txt" {
		t.Fatalf("snapshot leaked a later mutation: path = %v, want unchanged %q", snapshot["path"], "/a/b.txt")
	}
	if _, ok := snapshot["new_key_added_during_execution"]; ok {
		t.Fatalf("snapshot leaked a key added to the original map after snapshotting")
	}
	if _, ok := snapshot["recursive"]; !ok {
		t.Fatalf("snapshot lost a key that was deleted from the original map after snapshotting")
	}
}

// TestSnapshotToolParametersConcurrentReadWriteIsRaceFree is the same
// scenario driven concurrently under `go test -race`: one goroutine reads
// every key of the snapshot repeatedly (standing in for the UI's activity
// describer) while another goroutine mutates the ORIGINAL map repeatedly
// (standing in for a tool/hook execution) -- because the reader only ever
// touches the independent snapshot copy, the race detector must find
// nothing.
func TestSnapshotToolParametersConcurrentReadWriteIsRaceFree(t *testing.T) {
	original := map[string]any{"a": "1", "b": "2", "c": "3"}
	snapshot := snapshotToolParameters(original)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			for k := range snapshot {
				_ = snapshot[k]
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			original["a"] = i
			original["d"] = i
			delete(original, "d")
		}
	}()

	wg.Wait()
}

// TestSnapshotToolParametersNil covers the nil-map input, which every
// ToolCallUpdate emission site can legitimately pass (tools with no
// parameters).
func TestSnapshotToolParametersNil(t *testing.T) {
	if got := snapshotToolParameters(nil); got != nil {
		t.Fatalf("snapshotToolParameters(nil) = %#v, want nil", got)
	}
}
