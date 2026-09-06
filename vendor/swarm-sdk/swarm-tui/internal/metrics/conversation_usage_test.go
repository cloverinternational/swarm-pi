package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanConversationUsageDeduplicatesFilesAndCountsCache(t *testing.T) {
	root := t.TempDir()
	writeConversationFixture(t, filepath.Join(root, "legacy.json"), `{
		"id":"same",
		"total_tokens":110,
		"messages":[{"role":"assistant","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":80,"output":20,"total":110,"cache_read":10}}]
	}`)
	writeConversationFixture(t, filepath.Join(root, "workspace", "copy.json"), `{
		"id":"same",
		"total_tokens":230,
		"messages":[
			{"role":"assistant","timestamp":"2026-01-01T00:00:02Z","tokens":{"input":100,"output":20,"total":130,"cache_read":10}},
			{"role":"assistant","timestamp":"2026-01-01T00:00:03Z","tokens":{"input":80,"output":20,"total":100}}
		]
	}`)
	writeConversationFixture(t, filepath.Join(root, "workspace", "other.json"), `{
		"id":"other",
		"total_tokens":55,
		"messages":[{"role":"assistant","timestamp":"2026-01-01T00:00:04Z","tokens":{"input":40,"output":5,"total":55,"cache_creation":10}}]
	}`)
	writeConversationFixture(t, filepath.Join(root, "debug", "ignored.json"), `{
		"id":"debug","total_tokens":999,"messages":[{"tokens":{"total":999}}]
	}`)

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalTokens != 285 {
		t.Fatalf("TotalTokens = %d, want 285", got.TotalTokens)
	}
	if got.InputTokens != 240 {
		t.Errorf("InputTokens = %d, want 240 (cache included once)", got.InputTokens)
	}
	if got.OutputTokens != 45 {
		t.Errorf("OutputTokens = %d, want 45", got.OutputTokens)
	}
	if got.ConversationCount != 2 {
		t.Errorf("ConversationCount = %d, want 2", got.ConversationCount)
	}
	if got.ResponseCount != 3 {
		t.Errorf("ResponseCount = %d, want 3", got.ResponseCount)
	}
	if got.DuplicateFiles != 1 {
		t.Errorf("DuplicateFiles = %d, want 1", got.DuplicateFiles)
	}
}

func TestScanConversationUsageIgnoresAggregateEstimates(t *testing.T) {
	root := t.TempDir()
	writeConversationFixture(t, filepath.Join(root, "compacted.json"), `{
		"id":"compacted","total_tokens":1234,"messages":[]
	}`)

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalTokens != 0 || got.ConversationCount != 0 || got.ResponseCount != 0 {
		t.Fatalf("estimated aggregate should not count as provider usage: %+v", got)
	}
}

func TestScanConversationUsageDeduplicatesClonedResponsesAcrossLineage(t *testing.T) {
	root := t.TempDir()
	writeConversationFixture(t, filepath.Join(root, "parent.json"), `{
		"id":"parent","messages":[
			{"id":"provider-message","role":"assistant","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":100,"output":10,"total":110}},
			{"role":"assistant","timestamp":"2026-01-01T00:00:02Z","tokens":{"input":200,"output":20,"total":220}}
		]
	}`)
	writeConversationFixture(t, filepath.Join(root, "fork.json"), `{
		"id":"fork","metadata":{"custom":{"forked_from":"parent"}},"messages":[
			{"id":"provider-message","role":"assistant","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":100,"output":10,"total":110}},
			{"role":"assistant","timestamp":"2026-01-01T00:00:02Z","tokens":{"input":200,"output":20,"total":220}},
			{"role":"assistant","timestamp":"2026-01-01T00:00:03Z","tokens":{"input":300,"output":30,"total":330}}
		]
	}`)

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalTokens != 660 || got.ResponseCount != 3 {
		t.Fatalf("cloned responses counted more than once: %+v", got)
	}
	if got.DuplicateResponses != 2 {
		t.Errorf("DuplicateResponses = %d, want 2", got.DuplicateResponses)
	}
	if got.ConversationCount != 2 {
		t.Errorf("ConversationCount = %d, want 2", got.ConversationCount)
	}
}

func TestScanConversationUsageDoesNotDeduplicateUnrelatedTimestamps(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"one", "two"} {
		writeConversationFixture(t, filepath.Join(root, id+".json"), `{
			"id":"`+id+`","messages":[
				{"role":"assistant","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":100,"output":10,"total":110}}
			]
		}`)
	}

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalTokens != 220 || got.ResponseCount != 2 || got.DuplicateResponses != 0 {
		t.Fatalf("unrelated responses were deduplicated: %+v", got)
	}
}

func TestScanConversationUsagePrefersDetailedEqualTotalCopy(t *testing.T) {
	root := t.TempDir()
	writeConversationFixture(t, filepath.Join(root, "one.json"), `{
		"id":"same","messages":[{"role":"assistant","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":90,"output":10,"total":100}}]
	}`)
	writeConversationFixture(t, filepath.Join(root, "two.json"), `{
		"id":"same","messages":[
			{"role":"assistant","timestamp":"2026-01-01T00:00:02Z","tokens":{"input":40,"output":10,"total":50}},
			{"role":"assistant","timestamp":"2026-01-01T00:00:03Z","tokens":{"input":40,"output":10,"total":50}}
		]
	}`)

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalTokens != 100 || got.ResponseCount != 2 {
		t.Fatalf("did not prefer more complete equal-total copy: %+v", got)
	}
}

func TestScanConversationUsageCountsOnlyAssistantResponses(t *testing.T) {
	root := t.TempDir()
	writeConversationFixture(t, filepath.Join(root, "mixed.json"), `{
		"id":"mixed","messages":[
			{"role":"system","timestamp":"2026-01-01T00:00:01Z","tokens":{"total":1000}},
			{"role":"user","timestamp":"2026-01-01T00:00:02Z","tokens":{"total":2000}},
			{"role":"assistant","timestamp":"2026-01-01T00:00:03Z","tokens":{"input":90,"output":10,"total":100}}
		]
	}`)

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalTokens != 100 || got.ResponseCount != 1 {
		t.Fatalf("non-assistant token estimates counted as provider usage: %+v", got)
	}
}

func TestScanConversationUsageUsesCanonicalTotalAsLowerBound(t *testing.T) {
	root := t.TempDir()
	writeConversationFixture(t, filepath.Join(root, "cache.json"), `{
		"id":"cache","messages":[
			{"role":"assistant","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":100,"cache_read":50,"output":10,"total":110}}
		]
	}`)

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != 150 || got.OutputTokens != 10 || got.TotalTokens != 160 {
		t.Fatalf("canonical total lower bound not applied: %+v", got)
	}
}

func TestScanConversationUsageReportsMalformedConversation(t *testing.T) {
	root := t.TempDir()
	writeConversationFixture(t, filepath.Join(root, "broken.json"), `{"id":"broken","messages":[`)

	got, err := ScanConversationUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.UnreadableFiles != 1 {
		t.Fatalf("UnreadableFiles = %d, want 1", got.UnreadableFiles)
	}
}

func TestScanConversationUsageHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ScanConversationUsage(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func writeConversationFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
