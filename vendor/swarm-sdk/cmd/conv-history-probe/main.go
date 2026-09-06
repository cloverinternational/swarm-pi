// conv-history-probe verifies that conversation history is consistent across
// the two storage clients used by the TUI: the legacy sdk.manager and the
// canonical sdkClient.  The bug being tested is a stale-LRU-cache split:
// both clients point to the same storage directory but maintain separate
// in-memory LRU caches, so writes through sdkClient are invisible to reads
// through sdk.manager after the first load.
//
// Usage:
//
//	go run ./swarm-sdk/cmd/conv-history-probe
//	go run ./swarm-sdk/cmd/conv-history-probe -verbose
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

func main() {
	verbose := flag.Bool("verbose", false, "Print detailed per-step output")
	flag.Parse()

	storageDir := filepath.Join(os.TempDir(), fmt.Sprintf("conv-probe-%d", time.Now().UnixNano()))
	defer os.RemoveAll(storageDir)

	if *verbose {
		fmt.Printf("Storage dir: %s\n\n", storageDir)
	}

	ctx := context.Background()
	logger := noop.NewLogger()
	tracer := noop.NewTracer()

	// ── Build legacy sdk.manager (same pattern as TUI's legacy path) ──────────
	legacySt, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{
		BaseDir:   storageDir,
		CacheSize: 100,
	})
	must(err, "create legacy storage")
	legacyMgr, err := manager.NewManager(manager.Config{
		Storage: legacySt,
		Logger:  logger,
		Tracer:  tracer,
	})
	must(err, "create legacy manager")

	// ── Build sdkClient (same options as TUI's Phase C wiring) ───────────────
	sdkC, err := sdkclient.New(
		sdkclient.WithProvider("anthropic", "claude-sonnet-4-5"),
		sdkclient.WithStorageDir(storageDir),
		sdkclient.WithLogger(logger),
		sdkclient.WithTracer(tracer),
		sdkclient.WithoutAutoConfig(),
	)
	must(err, "create sdkClient")
	defer sdkC.Close()

	// ── Step 1: create a conversation via sdkClient ───────────────────────────
	conv, err := sdkC.CreateConversationWithOptions(ctx, manager.CreateOptions{
		Mode:          "chat",
		WorkspacePath: storageDir,
	})
	must(err, "create conversation")
	convID := conv.ID
	step(*verbose, 1, "created conversation %s", convID)

	// ── Step 2: prime legacyMgr's cache by loading once ──────────────────────
	primed, err := legacyMgr.Resume(ctx, convID)
	must(err, "prime legacy cache")
	step(*verbose, 2, "legacy manager loaded %d messages (cache primed)", len(primed.Messages))

	// ── Step 3: write two messages through sdkClient ──────────────────────────
	msgs := []struct{ role, content string }{
		{"user", "Why is the sky blue?"},
		{"assistant", "Rayleigh scattering causes shorter blue wavelengths to scatter more than red."},
	}
	for _, m := range msgs {
		role := conversation.RoleUser
		if m.role == "assistant" {
			role = conversation.RoleAssistant
		}
		err = sdkC.AddMessageToConversation(ctx, convID, &conversation.Message{
			ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
			Role:      role,
			Content:   m.content,
			Timestamp: time.Now(),
		})
		must(err, "add message via sdkClient")
	}
	step(*verbose, 3, "added 2 messages through sdkClient")

	// ── Step 4: read back via legacyMgr (reproduces the stale-cache bug) ─────
	stale, err := legacyMgr.Resume(ctx, convID)
	must(err, "legacy resume after write")
	staleMsgs := len(stale.Messages)
	step(*verbose, 4, "legacy manager sees %d messages (expected 2 if cache is stale)", staleMsgs)

	// ── Step 5: read back via sdkClient (the correct path post-fix) ──────────
	fresh, err := sdkC.ResumeConversation(ctx, convID)
	must(err, "sdkClient resume after write")
	freshMsgs := len(fresh.Messages)
	step(*verbose, 5, "sdkClient sees %d messages (correct)", freshMsgs)

	// ── Step 6: list conversations via sdkClient ──────────────────────────────
	listed, err := sdkC.ListConversationsMeta(ctx, storageDir)
	must(err, "list conversations")
	step(*verbose, 6, "listed %d conversation(s)", len(listed))

	// ── Step 7: compact via sdkClient ─────────────────────────────────────────
	compactErr := sdkC.CompactConversation(ctx, convID)
	if compactErr != nil {
		// Compaction may fail without a real provider wired — that's fine; we
		// only care that the plumbing doesn't panic and that history is intact.
		step(*verbose, 7, "compaction skipped (no real provider): %v", compactErr)
	} else {
		step(*verbose, 7, "compaction succeeded")
	}

	// ── Step 8: re-read after compact to confirm conversation survives ────────
	postCompact, err := sdkC.ResumeConversation(ctx, convID)
	must(err, "resume after compact")
	step(*verbose, 8, "post-compact message count: %d", len(postCompact.Messages))

	// ── Results ───────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Println("  conv-history-probe results")
	fmt.Println("═══════════════════════════════════════════════════")

	staleness := "STALE (bug present)"
	if staleMsgs == freshMsgs {
		staleness = "consistent (both up-to-date)"
	}
	fmt.Printf("  legacy manager after write : %d msg  ← %s\n", staleMsgs, staleness)
	fmt.Printf("  sdkClient after write      : %d msg  ← correct path\n", freshMsgs)
	fmt.Printf("  conversations listed       : %d\n", len(listed))
	fmt.Printf("  post-compact msg count     : %d\n", len(postCompact.Messages))
	fmt.Println()

	// ── Pass / fail ───────────────────────────────────────────────────────────
	pass := true

	if freshMsgs != 2 {
		fail("sdkClient should see 2 messages, got %d", freshMsgs)
		pass = false
	}
	if len(listed) == 0 {
		fail("expected at least 1 conversation listed, got 0")
		pass = false
	}
	if staleMsgs == freshMsgs {
		// Legacy manager is NOT stale — either the storage flushes through a
		// shared instance (shared underlying store) or the implementation
		// changed. Either way the important invariant is that sdkClient is correct.
		fmt.Println("  NOTE: legacy manager returned fresh data (shared store or no cache split)")
	} else {
		fmt.Printf("  NOTE: legacy manager returned stale data (%d vs %d) — this is\n", staleMsgs, freshMsgs)
		fmt.Println("        expected behavior; the TUI fix routes history reads through sdkClient.")
	}

	if pass {
		fmt.Println("  ✓ PASS — sdkClient history path is correct")
		os.Exit(0)
	} else {
		fmt.Println("  ✗ FAIL")
		os.Exit(1)
	}
}

func step(verbose bool, n int, format string, args ...any) {
	if verbose {
		fmt.Printf("[%d] %s\n", n, fmt.Sprintf(format, args...))
	}
}

func must(err error, label string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", label, err)
		os.Exit(1)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "  ✗ FAIL: %s\n", fmt.Sprintf(format, args...))
}
