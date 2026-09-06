// Command history-backfill-probe proves — against the REAL SDK client and REAL
// on-disk storage (no mocks) — that the two seams behind the "titles/summaries
// not working" bug actually work:
//
//  1. GetConversationMessages returns the messages that were added
//     (the root cause was code that saw 0 messages).
//  2. SetConversationTitle persists a Title that survives a reload from disk.
//  3. SetConversationSummary persists Summary.Recap that survives a reload.
//
// It uses a tempdir for storage and prints the real bytes read back so a human
// can see the values, not a "saved successfully" log line.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
)

func main() {
	dir := flag.String("dir", "", "storage dir (default: a fresh temp dir)")
	flag.Parse()

	storageDir := *dir
	if storageDir == "" {
		d, err := os.MkdirTemp("", "history-backfill-probe-*")
		if err != nil {
			fatal("mkdtemp: %v", err)
		}
		storageDir = d
	}
	fmt.Fprintf(os.Stderr, "[probe] storage_dir=%s\n", storageDir)

	ctx := context.Background()

	c, err := client.New(
		client.WithoutAutoConfig(),
		client.WithProvider("anthropic", "claude-sonnet-4-5"),
		client.WithAPIKey("dummy"),
		client.WithStorageDir(storageDir),
	)
	if err != nil {
		fatal("client.New: %v", err)
	}

	conv, err := c.NewConversation(ctx)
	if err != nil {
		fatal("NewConversation: %v", err)
	}
	fmt.Fprintf(os.Stderr, "[probe] conv_id=%s initial_title=%q\n", conv.ID, conv.Title)

	// Add a realistic 4-message exchange (mirrors the >=4 summary trigger).
	msgs := []struct {
		role    conversation.Role
		content string
	}{
		{conversation.RoleUser, "Consolidate the two history menus and fix the chat titles."},
		{conversation.RoleAssistant, "There are two surfaces: the Home HISTORY tab and the two-pane ScreenChats view."},
		{conversation.RoleUser, "Also I need to see the summary of each conversation."},
		{conversation.RoleAssistant, "I'll add an LLM recap persisted to ConversationSummary.Recap and render it in a summary panel."},
	}
	for i, m := range msgs {
		if err := c.AddMessageToConversation(ctx, conv.ID, &conversation.Message{
			ID:        fmt.Sprintf("m%d", i),
			Timestamp: time.Now(),
			Role:      m.role,
			Content:   m.content,
		}); err != nil {
			fatal("AddMessageToConversation[%d]: %v", i, err)
		}
	}

	// SEAM 1: the exact call the generators now use (a.sdk.GetMessages ->
	// GetConversationMessages). This was returning 0 before the fix.
	got, err := c.GetConversationMessages(ctx, conv.ID, manager.GetMessagesOptions{})
	if err != nil {
		fatal("GetConversationMessages: %v", err)
	}
	fmt.Println("==== SEAM 1: GetConversationMessages ====")
	fmt.Printf("message_count=%d (want 4)\n", len(got))
	for _, mm := range got {
		fmt.Printf("  - %s: %.60s\n", mm.Role, mm.Content)
	}
	pass := len(got) == 4

	// SEAM 2: persist a title through the real setter, then reload from disk.
	const wantTitle = "Consolidate history menus"
	if err := c.SetConversationTitle(ctx, conv.ID, wantTitle); err != nil {
		fatal("SetConversationTitle: %v", err)
	}

	// SEAM 3: persist a recap through the real setter, then reload from disk.
	const wantRecap = "User asked to merge the two history menus and improve chat titles, and to surface a per-conversation summary. Plan: add an LLM recap and a summary panel."
	if err := c.SetConversationSummary(ctx, conv.ID, wantRecap); err != nil {
		fatal("SetConversationSummary: %v", err)
	}

	// Reload from storage (NOT the in-memory object) to prove persistence.
	reloaded, err := c.LoadConversation(ctx, conv.ID)
	if err != nil {
		fatal("LoadConversation: %v", err)
	}

	fmt.Println("==== SEAM 2: persisted Title (reloaded from disk) ====")
	fmt.Printf("title=%q\n", reloaded.Title)
	titleOK := reloaded.Title == wantTitle
	pass = pass && titleOK

	fmt.Println("==== SEAM 3: persisted Summary.Recap (reloaded from disk) ====")
	gotRecap := ""
	if reloaded.Summary != nil {
		gotRecap = reloaded.Summary.Recap
	}
	fmt.Printf("recap=%q\n", gotRecap)
	recapOK := gotRecap == wantRecap
	pass = pass && recapOK

	fmt.Println("==== RESULT ====")
	fmt.Printf("messages_ok=%v  title_ok=%v  recap_ok=%v\n", len(got) == 4, titleOK, recapOK)
	if pass {
		fmt.Println("PROBE PASS: message load + title persist + recap persist all work end-to-end")
		return
	}
	fmt.Println("PROBE FAIL")
	os.Exit(1)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[probe] "+format+"\n", args...)
	os.Exit(2)
}
