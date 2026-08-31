package taskstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

const legacyAuditSentinel = "legacy-audit-sentinel"

func legacyUnsafeAuditTask() Task {
	return Task{
		ID:       "1",
		Subject:  "Legacy audit",
		Status:   StatusInProgress,
		Priority: PriorityMedium,
		Active:   true,
		AuditEvents: []TaskAuditEvent{{
			Type:    "tool",
			Actor:   "main",
			Summary: "tmux send-keys -l 'password=" + legacyAuditSentinel + "'",
			Metadata: map[string]any{
				"tool":     "Bash",
				"outcome":  "success",
				"password": legacyAuditSentinel,
				"nested": map[string]any{
					"token": legacyAuditSentinel,
				},
			},
		}},
	}
}

func writeLegacyUnsafeStore(t *testing.T, cfg Config) string {
	t.Helper()
	store := New(cfg)
	store.Tasks = []Task{legacyUnsafeAuditTask()}
	store.LastUpdated = time.Now().UTC()
	store.Checksum = store.calculateChecksum()
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.MetadataDir, "task.json")
	if err := os.MkdirAll(cfg.MetadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), legacyAuditSentinel) {
		t.Fatal("legacy fixture was sanitized before migration")
	}
	return path
}

func TestLoadMigratesHistoricalAuditSecretsInMemoryAndOnDisk(t *testing.T) {
	cfg := Config{ConversationID: "legacy-audit", MetadataDir: t.TempDir()}
	path := writeLegacyUnsafeStore(t, cfg)

	store, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.GetTask("1")
	if err != nil {
		t.Fatal(err)
	}
	encoded := task.AuditEvents[0].Summary
	if strings.Contains(encoded, legacyAuditSentinel) || !strings.Contains(encoded, "[REDACTED]") {
		t.Fatalf("loaded summary was not redacted: %q", encoded)
	}
	if task.AuditEvents[0].Metadata["tool"] != "Bash" || task.AuditEvents[0].Metadata["outcome"] != "success" {
		t.Fatalf("ordinary audit metadata changed: %+v", task.AuditEvents[0].Metadata)
	}
	if task.AuditEvents[0].Metadata["password"] != "[REDACTED]" {
		t.Fatalf("secret-key metadata was not redacted: %+v", task.AuditEvents[0].Metadata)
	}
	nested, _ := task.AuditEvents[0].Metadata["nested"].(map[string]any)
	if nested["token"] != "[REDACTED]" {
		t.Fatalf("nested secret-key metadata was not redacted: %+v", nested)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), legacyAuditSentinel) {
		t.Fatal("legacy audit secret remained recoverable on disk")
	}

	reloaded, err := Load(cfg)
	if err != nil {
		t.Fatalf("second load after checksum rewrite failed: %v", err)
	}
	all := reloaded.GetAllTasks()
	if len(all) != 1 || strings.Contains(all[0].AuditEvents[0].Summary, legacyAuditSentinel) {
		t.Fatalf("second load returned unsafe audit history: %+v", all)
	}
	afterSecondLoad, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterSecondLoad) != string(onDisk) {
		t.Fatal("idempotent second load rewrote already-sanitized audit history")
	}

	manager := ii.NewTodoManager()
	if err := RestoreTodos(reloaded, manager); err != nil {
		t.Fatal(err)
	}
	if got := manager.ByID("1"); got == nil || strings.Contains(got.AuditEvents[0].Summary, legacyAuditSentinel) {
		t.Fatalf("restored runtime task leaked legacy audit history: %+v", got)
	}
	result, err := ii.NewTaskManageToolWithManager(manager).Execute(context.Background(), map[string]any{
		"operations": []any{map[string]any{"key": "legacy", "op": "get", "taskId": "1", "include_audit": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, legacyAuditSentinel) || !strings.Contains(result.Output, "[REDACTED]") {
		t.Fatalf("TaskManage returned unsafe historical audit data: %s", result.Output)
	}
}

func TestLoadFailsClosedWhenAuditMigrationCannotPersist(t *testing.T) {
	cfg := Config{ConversationID: "legacy-audit-failure", MetadataDir: t.TempDir()}
	path := writeLegacyUnsafeStore(t, cfg)
	originalWrite := writeTaskStoreFile
	writeTaskStoreFile = func(string, []byte) error {
		return errors.New("injected atomic-write failure")
	}
	t.Cleanup(func() { writeTaskStoreFile = originalWrite })

	store, err := Load(cfg)
	if err == nil || store != nil {
		t.Fatalf("Load returned an unsafe store when migration persistence failed: store=%+v err=%v", store, err)
	}
	if !strings.Contains(err.Error(), "persist redacted task audit history") {
		t.Fatalf("unexpected migration error: %v", err)
	}
	original, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(original), legacyAuditSentinel) {
		t.Fatal("failure fixture unexpectedly changed; test no longer proves fail-closed behavior")
	}
}

func TestLoadVerifiesChecksumBeforeAuditMigration(t *testing.T) {
	cfg := Config{ConversationID: "legacy-audit-corrupt", MetadataDir: t.TempDir()}
	path := writeLegacyUnsafeStore(t, cfg)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := strings.Replace(string(before), legacyAuditSentinel, legacyAuditSentinel+"-corrupt", 1)
	if err := os.WriteFile(path, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Load(cfg)
	if err == nil || store != nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Load did not reject unverified audit history: store=%+v err=%v", store, err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != corrupt {
		t.Fatal("checksum-invalid task store was rewritten")
	}
}

func TestAuditRedactionCoversTypedContainersAndEventType(t *testing.T) {
	event := TaskAuditEvent{
		Type:    "token=" + legacyAuditSentinel,
		Actor:   "password=" + legacyAuditSentinel,
		Summary: "secret=" + legacyAuditSentinel,
		Metadata: map[string]any{
			"typed_map": map[string]string{"token": legacyAuditSentinel},
			"typed_slice": []map[string]string{{
				"password": legacyAuditSentinel,
			}},
			"password_object": map[string]any{
				"value": legacyAuditSentinel,
			},
			"typed_values": map[string][]string{
				"tokens": {legacyAuditSentinel},
			},
			"raw_json": json.RawMessage(`{"token":"` + legacyAuditSentinel + `"}`),
		},
	}
	redacted, changed := redactTaskAuditEvents([]TaskAuditEvent{event})
	if !changed {
		t.Fatal("typed audit event was not reported as changed")
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), legacyAuditSentinel) {
		t.Fatalf("typed audit metadata leaked after redaction: %s", encoded)
	}
	if redacted[0].Metadata["password_object"] != "[REDACTED]" {
		t.Fatalf("sensitive composite field was not masked wholesale: %+v", redacted[0].Metadata)
	}
}

func TestAuditRedactionIsIdempotentForTruncatedText(t *testing.T) {
	event := TaskAuditEvent{
		Type:    strings.Repeat("t", 5000),
		Actor:   strings.Repeat("a", 5000),
		Summary: strings.Repeat("s", 5000),
	}
	once, changed := redactTaskAuditEvents([]TaskAuditEvent{event})
	if !changed {
		t.Fatal("first redaction did not truncate oversized text")
	}
	twice, changedAgain := redactTaskAuditEvents(once)
	if changedAgain {
		t.Fatal("second redaction changed canonical truncated text")
	}
	firstJSON, err := json.Marshal(once)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(twice)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("repeated redaction was not byte-for-byte idempotent")
	}
}

func TestMutationAndGetterPathsSanitizeAndDetachAuditData(t *testing.T) {
	cfg := Config{ConversationID: "audit-paths", MetadataDir: t.TempDir()}
	store := New(cfg)
	task := legacyUnsafeAuditTask()
	task.Metadata = map[string]any{"nested": map[string]string{"value": "original"}}
	task.DependsOn = []string{"dependency"}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}

	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.AuditEvents[0].Type = "token=" + legacyAuditSentinel
	updated.AuditEvents[0].Summary = "password=" + legacyAuditSentinel
	updated.AuditEvents[0].Metadata = map[string]any{
		"typed": map[string]string{"token": legacyAuditSentinel},
	}
	if err := store.UpdateTask(updated); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(store.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), legacyAuditSentinel) {
		t.Fatalf("mutation path persisted an audit secret: %s", onDisk)
	}

	getters := []struct {
		name string
		get  func() []Task
	}{
		{"all", store.GetAllTasks},
		{"active", store.GetActiveTasks},
		{"in_progress", store.GetInProgressTasks},
	}
	for _, getter := range getters {
		t.Run(getter.name, func(t *testing.T) {
			got := getter.get()
			if len(got) != 1 {
				t.Fatalf("got %d tasks", len(got))
			}
			got[0].DependsOn[0] = "mutated"
			got[0].Metadata["nested"].(map[string]string)["value"] = "mutated"
			got[0].AuditEvents[0].Metadata["typed"].(map[string]any)["token"] = "mutated"

			fresh, getErr := store.GetTask(task.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if fresh.DependsOn[0] != "dependency" ||
				fresh.Metadata["nested"].(map[string]string)["value"] != "original" ||
				fresh.AuditEvents[0].Metadata["typed"].(map[string]any)["token"] != "[REDACTED]" {
				t.Fatalf("getter leaked mutable internal state: %+v", fresh)
			}
		})
	}
}

func TestEveryFilteredGetterReturnsDetachedTasks(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		get    func(*Store) []Task
	}{
		{"pending", StatusPending, (*Store).GetPendingTasks},
		{"in_progress", StatusInProgress, (*Store).GetInProgressTasks},
		{"completed", StatusCompleted, (*Store).GetCompletedTasks},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := New(Config{ConversationID: test.name, MetadataDir: t.TempDir()})
			task := legacyUnsafeAuditTask()
			task.Status = test.status
			task.Metadata = map[string]any{"nested": map[string]string{"value": "original"}}
			if err := store.AddTask(task); err != nil {
				t.Fatal(err)
			}
			got := test.get(store)
			if len(got) != 1 {
				t.Fatalf("got %d tasks", len(got))
			}
			got[0].Metadata["nested"].(map[string]string)["value"] = "mutated"
			got[0].AuditEvents[0].Metadata["nested"].(map[string]any)["token"] = "mutated"

			fresh, err := store.GetTask(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Metadata["nested"].(map[string]string)["value"] != "original" ||
				fresh.AuditEvents[0].Metadata["nested"].(map[string]any)["token"] != "[REDACTED]" {
				t.Fatalf("filtered getter leaked mutable state: %+v", fresh)
			}
		})
	}
}

func TestMetadataCloneDetachesStructsAndDropsCycles(t *testing.T) {
	type structuredMetadata struct {
		Values []string `json:"values"`
	}
	original := structuredMetadata{Values: []string{"original"}}
	cycle := map[string]any{}
	cycle["self"] = cycle

	store := New(Config{ConversationID: "clone-metadata", MetadataDir: t.TempDir()})
	if err := store.AddTask(Task{
		ID:       "1",
		Subject:  "Clone metadata",
		Status:   StatusPending,
		Priority: PriorityMedium,
		Metadata: map[string]any{"struct": original, "cycle": cycle},
	}); err != nil {
		t.Fatal(err)
	}
	original.Values[0] = "caller-mutated"

	got, err := store.GetTask("1")
	if err != nil {
		t.Fatal(err)
	}
	cloned := got.Metadata["struct"].(structuredMetadata)
	if cloned.Values[0] != "original" {
		t.Fatalf("store retained caller struct alias: %+v", cloned)
	}
	cloned.Values[0] = "getter-mutated"
	fresh, err := store.GetTask("1")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Metadata["struct"].(structuredMetadata).Values[0] != "original" {
		t.Fatal("getter returned a struct with store-owned nested values")
	}
	if fresh.Metadata["cycle"] != nil {
		t.Fatalf("cyclic metadata was retained instead of safely detached: %#v", fresh.Metadata["cycle"])
	}
	if err := store.Save(); err != nil {
		t.Fatalf("sanitized cyclic metadata prevented persistence: %v", err)
	}
}
