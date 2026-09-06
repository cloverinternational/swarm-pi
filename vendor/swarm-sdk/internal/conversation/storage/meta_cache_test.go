package storage

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// metaConvJSON builds a conversation document with a controllable title and
// message payload so tests can vary content (and therefore file size).
func metaConvJSON(id, title, content string) []byte {
	return []byte(`{"id":"` + id + `","title":"` + title +
		`","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
		`"messages":[{"id":"m1","role":"user","content":"` + content + `"}],` +
		`"summary":{"last_model":"m"},"metadata":{"tags":["t1"]}}`)
}

// A metadata Query served from the cache must be equivalent to one served by
// reading the file. This is the property that makes the cache safe to add;
// without it the cache is a correctness regression.
func TestMetaCacheSecondQueryMatchesFirst(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	const ws = "/workspace/meta-cache"

	writePartitionedConversation(t, baseDir, ws, "c1", metaConvJSON("c1", "First", "hello"))
	writePartitionedConversation(t, baseDir, ws, "c2", metaConvJSON("c2", "Second", "world"))

	store := newTestDirectoryStorage(t, baseDir)
	filter := Filter{WorkspacePath: ws, ExcludeMessages: true}

	cold, err := store.Query(ctx, filter)
	if err != nil {
		t.Fatalf("cold Query: %v", err)
	}
	if len(cold) != 2 {
		t.Fatalf("cold Query returned %d conversations, want 2", len(cold))
	}

	// Prove the cache actually got populated, otherwise this test would pass
	// trivially even if the cache were never consulted.
	store.metaMu.RLock()
	cached := len(store.metaCache)
	store.metaMu.RUnlock()
	if cached != 2 {
		t.Fatalf("metaCache holds %d entries after cold query, want 2", cached)
	}

	warm, err := store.Query(ctx, filter)
	if err != nil {
		t.Fatalf("warm Query: %v", err)
	}
	if !reflect.DeepEqual(cold, warm) {
		t.Fatalf("cached result differs from freshly read result:\ncold=%#v\nwarm=%#v", cold, warm)
	}
}

// A rewritten file must invalidate its cache entry. Content length changes, so
// this holds even on filesystems with coarse modTime resolution.
func TestMetaCacheInvalidatesWhenFileChanges(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	const ws = "/workspace/meta-stale"

	writePartitionedConversation(t, baseDir, ws, "c1", metaConvJSON("c1", "Original", "short"))

	store := newTestDirectoryStorage(t, baseDir)
	filter := Filter{WorkspacePath: ws, ExcludeMessages: true}

	first, err := store.Query(ctx, filter)
	if err != nil {
		t.Fatalf("first Query: %v", err)
	}
	if len(first) != 1 || first[0].Title != "Original" {
		t.Fatalf("first Query = %#v, want one conversation titled Original", first)
	}

	// Rewrite the same path with a different title and a different length.
	writePartitionedConversation(t, baseDir, ws, "c1",
		metaConvJSON("c1", "Rewritten", strings.Repeat("y", 4096)))

	second, err := store.Query(ctx, filter)
	if err != nil {
		t.Fatalf("second Query: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("second Query returned %d conversations, want 1", len(second))
	}
	if second[0].Title != "Rewritten" {
		t.Fatalf("Title = %q, want Rewritten - stale metadata was served from cache", second[0].Title)
	}
}

// Entries are keyed by preview depth: a FirstMessagePreview query must not be
// answered from an ExcludeMessages entry, which carries no messages.
func TestMetaCacheKeyedByPreviewDepth(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	const ws = "/workspace/meta-preview"

	writePartitionedConversation(t, baseDir, ws, "c1", metaConvJSON("c1", "Titled", "preview-body"))

	store := newTestDirectoryStorage(t, baseDir)

	noMsgs, err := store.Query(ctx, Filter{WorkspacePath: ws, ExcludeMessages: true})
	if err != nil {
		t.Fatalf("ExcludeMessages Query: %v", err)
	}
	if len(noMsgs) != 1 || len(noMsgs[0].Messages) != 0 {
		t.Fatalf("ExcludeMessages returned messages: %#v", noMsgs)
	}

	withPreview, err := store.Query(ctx, Filter{WorkspacePath: ws, FirstMessagePreview: 1})
	if err != nil {
		t.Fatalf("FirstMessagePreview Query: %v", err)
	}
	if len(withPreview) != 1 {
		t.Fatalf("preview Query returned %d conversations, want 1", len(withPreview))
	}
	if len(withPreview[0].Messages) != 1 {
		t.Fatalf("preview Query returned %d messages, want 1 - a preview-0 cache entry was reused",
			len(withPreview[0].Messages))
	}
	if got := withPreview[0].Messages[0].Content; got != "preview-body" {
		t.Fatalf("preview message content = %q, want preview-body", got)
	}
}
