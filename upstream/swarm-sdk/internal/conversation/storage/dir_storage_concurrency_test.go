package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestDirectoryFileStorageConcurrentLoadsAreCorrectAndIndependent(t *testing.T) {
	t.Parallel()

	const (
		conversationCount = 12
		goroutineCount    = 48
		loadsPerGoroutine = 20
		workspacePath     = "/workspace/concurrent-correctness"
	)

	ctx := context.Background()
	baseDir := t.TempDir()
	for i := 0; i < conversationCount; i++ {
		id := fmt.Sprintf("conversation-%02d", i)
		conv := &conversation.Conversation{
			ID:            id,
			Title:         "title-" + id,
			WorkspacePath: workspacePath,
			Status:        conversation.StatusActive,
			Messages: []*conversation.Message{
				{ID: "message-" + id, Content: "content-" + id},
			},
			Metadata: conversation.ConversationMetadata{
				Custom: map[string]any{
					"nested": map[string]any{"value": "metadata-" + id},
				},
			},
		}
		data, err := json.Marshal(conv)
		if err != nil {
			t.Fatalf("marshal %s: %v", id, err)
		}
		writePartitionedConversation(t, baseDir, workspacePath, id, data)
	}

	store := newTestDirectoryStorage(t, baseDir)
	var wg sync.WaitGroup
	errs := make(chan error, goroutineCount)
	for goroutine := 0; goroutine < goroutineCount; goroutine++ {
		wg.Add(1)
		go func(goroutine int) {
			defer wg.Done()
			for load := 0; load < loadsPerGoroutine; load++ {
				id := fmt.Sprintf("conversation-%02d", (goroutine+load)%conversationCount)
				got, err := store.Load(ctx, id)
				if err != nil {
					errs <- fmt.Errorf("Load(%s): %w", id, err)
					return
				}
				if got.ID != id || got.Title != "title-"+id || got.WorkspacePath != workspacePath {
					errs <- fmt.Errorf("Load(%s) returned incomplete conversation: %#v", id, got)
					return
				}
				if len(got.Messages) != 1 || got.Messages[0].ID != "message-"+id || got.Messages[0].Content != "content-"+id {
					errs <- fmt.Errorf("Load(%s) returned incomplete messages: %#v", id, got.Messages)
					return
				}
				nested, ok := got.Metadata.Custom["nested"].(map[string]any)
				if !ok || nested["value"] != "metadata-"+id {
					errs <- fmt.Errorf("Load(%s) returned incomplete metadata: %#v", id, got.Metadata.Custom)
					return
				}

				got.Title = "mutated"
				got.Messages[0].Content = "mutated"
				nested["value"] = "mutated"

				again, err := store.Load(ctx, id)
				if err != nil {
					errs <- fmt.Errorf("second Load(%s): %w", id, err)
					return
				}
				againNested, ok := again.Metadata.Custom["nested"].(map[string]any)
				if again.Title != "title-"+id ||
					len(again.Messages) != 1 ||
					again.Messages[0].Content != "content-"+id ||
					!ok ||
					againNested["value"] != "metadata-"+id {
					errs <- fmt.Errorf("caller mutation corrupted cached %s: %#v", id, again)
					return
				}
			}
		}(goroutine)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestDirectoryFileStorageLoadReleasesLockAcrossFileRead pins the property that
// actually changed: Load performs its file read with d.mu released.
//
// Every concurrent Load blocks inside its own file read until all of them have
// arrived there. Under the previous implementation, which held d.mu from the
// top of Load through the read and the decode, only the first goroutine could
// ever reach its read and the rest would be parked on the mutex, so the barrier
// could never open and this test would hang until barrierTimeout. Under the
// current implementation all of them arrive and it completes immediately.
//
// This is a liveness assertion, not a stopwatch. An earlier version compared
// concurrent against sequential wall time and failed on a 2-vCPU CI runner,
// where CPU-bound decodes cannot overlap however the locking is written:
// available parallelism is a property of the machine, whereas lock scope is the
// property under test. Timing thresholds cannot tell those two apart.
func TestDirectoryFileStorageLoadReleasesLockAcrossFileRead(t *testing.T) {
	t.Parallel()

	const (
		conversationCount = 4
		workspacePath     = "/workspace/concurrent-read"
		// The failure mode is a barrier that never opens, so this only bounds
		// how long a genuine regression takes to report. It is deliberately
		// generous: a passing run never waits on it.
		barrierTimeout = 30 * time.Second
	)

	ctx := context.Background()
	baseDir := t.TempDir()
	ids := make([]string, conversationCount)
	for i := range ids {
		id := fmt.Sprintf("barrier-conversation-%02d", i)
		ids[i] = id
		conv := &conversation.Conversation{
			ID:            id,
			Title:         "title-" + id,
			WorkspacePath: workspacePath,
			Messages: []*conversation.Message{
				{ID: "message-" + id, Content: "content-" + id},
			},
		}
		data, err := json.Marshal(conv)
		if err != nil {
			t.Fatalf("marshal %s: %v", id, err)
		}
		writePartitionedConversation(t, baseDir, workspacePath, id, data)
	}

	store := newTestDirectoryStorage(t, baseDir)
	arrived := make(chan struct{}, conversationCount)
	release := make(chan struct{})
	store.readConversationFile = func(path string) ([]byte, error) {
		arrived <- struct{}{}
		<-release
		return os.ReadFile(path)
	}

	errs := make(chan error, conversationCount)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			loaded, err := store.Load(ctx, id)
			if err != nil {
				errs <- fmt.Errorf("Load(%s): %w", id, err)
				return
			}
			if loaded.ID != id || len(loaded.Messages) != 1 || loaded.Messages[0].Content != "content-"+id {
				errs <- fmt.Errorf("Load(%s) returned incomplete data: %#v", id, loaded)
			}
		}(id)
	}

	deadline := time.After(barrierTimeout)
	for reached := 0; reached < conversationCount; reached++ {
		select {
		case <-arrived:
		case <-deadline:
			// Do not close(release) here: the goroutines are blocked on the
			// mutex rather than on the barrier, and reporting the real failure
			// matters more than a tidy shutdown of a broken build.
			t.Fatalf("only %d of %d concurrent loads reached the file read within %s; Load is holding its lock across the read",
				reached, conversationCount, barrierTimeout)
		}
	}
	close(release)

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestDirectoryFileStorageCloseDuringLoadPreventsCacheInsert(t *testing.T) {
	const (
		id            = "close-during-load"
		workspacePath = "/workspace/close-during-load"
	)

	baseDir := t.TempDir()
	data, err := json.Marshal(&conversation.Conversation{
		ID:            id,
		Title:         "must-not-be-cached",
		WorkspacePath: workspacePath,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writePartitionedConversation(t, baseDir, workspacePath, id, data)

	store := newTestDirectoryStorage(t, baseDir)
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	store.readConversationFile = func(path string) ([]byte, error) {
		close(readStarted)
		<-releaseRead
		return os.ReadFile(path)
	}

	loadResult := make(chan error, 1)
	go func() {
		_, err := store.Load(context.Background(), id)
		loadResult <- err
	}()
	<-readStarted

	closeResult := make(chan error, 1)
	go func() {
		closeResult <- store.Close()
	}()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked while Load was reading without needing the storage lock")
	}

	close(releaseRead)
	if err := <-loadResult; !errors.Is(err, ErrStorageClosed) {
		t.Fatalf("Load error = %v, want %v", err, ErrStorageClosed)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if !store.closed {
		t.Fatal("storage remained open")
	}
	if store.cache != nil {
		t.Fatalf("closed storage cache was repopulated: %#v", store.cache)
	}
}
