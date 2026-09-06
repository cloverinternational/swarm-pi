// Package main — conversations_cli.go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
)

func runConversationsCLI(args []string) error {
	if len(args) == 0 {
		printConversationsUsage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "retitle", "backfill-titles":
		return runConversationsRetitle(args[1:], os.Stdout, os.Stderr)
	case "backfill-summaries", "resummarize":
		return runConversationsResummarize(args[1:], os.Stdout, os.Stderr)
	case "-h", "--help", "help":
		printConversationsUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown conversations sub-command %q (try: retitle)", args[0])
	}
}

func printConversationsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: swarmos conversations <subcommand> [options]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  retitle    Generate AI-powered titles for untitled conversations")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags (retitle):")
	fmt.Fprintln(w, "  --dry-run         Preview without AI calls (shows first-message fallback title)")
	fmt.Fprintln(w, "  --provider NAME   Provider override (default: from active profile)")
	fmt.Fprintln(w, "  --model MODEL     Model override (default: from active profile)")
	fmt.Fprintln(w, "  --workspace PATH  Scope to a specific workspace directory (default: all)")
	fmt.Fprintln(w, "  --limit N         Maximum conversations to process (default: 0 = unlimited)")
	fmt.Fprintln(w, "  --concurrency N   Parallel AI calls (default: 3)")
	fmt.Fprintln(w, "  --storage-dir DIR Storage directory (default: ~/.swarmos/conversations)")
	fmt.Fprintln(w, "  --debug           Show internal debug logs")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  swarmos conversations retitle --dry-run")
	fmt.Fprintln(w, "  swarmos conversations retitle")
	fmt.Fprintln(w, "  swarmos conversations retitle --limit 100 --concurrency 5")
	fmt.Fprintln(w, "  swarmos conversations retitle --provider anthropic --model claude-haiku-4-5")
}

func runConversationsRetitle(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("conversations retitle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "Preview without AI calls")
	provider := fs.String("provider", "", "Provider override")
	model := fs.String("model", "", "Model override")
	workspace := fs.String("workspace", "", "Scope to workspace path")
	limit := fs.Int("limit", 0, "Max conversations to process (0 = all)")
	concurrency := fs.Int("concurrency", 3, "Parallel AI calls")
	debug := fs.Bool("debug", false, "Show internal debug logs")

	homeDir, _ := os.UserHomeDir()
	defaultStorageDir := ""
	if homeDir != "" {
		defaultStorageDir = homeDir + "/.swarmos/conversations"
	}
	storageDir := fs.String("storage-dir", defaultStorageDir, "Conversation storage directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *storageDir == "" {
		return fmt.Errorf("--storage-dir is required (or set HOME)")
	}

	// debug logger — only prints when --debug is set
	dbg := func(format string, a ...any) {
		if *debug {
			fmt.Fprintf(stdout, "[debug] "+format+"\n", a...)
		}
	}

	// ── Resolve provider/model: active profile → flag overrides ──────────────
	resolvedProvider, resolvedModel := activeProfileMainModel()
	dbg("active profile resolved: provider=%q model=%q", resolvedProvider, resolvedModel)
	if *provider != "" {
		resolvedProvider = *provider
		dbg("provider overridden by flag: %q", resolvedProvider)
	}
	if *model != "" {
		resolvedModel = *model
		dbg("model overridden by flag: %q", resolvedModel)
	}

	// ── Build SDK client (same path as conductor / TUI) ──────────────────────
	var llm *sdkclient.Client
	if !*dryRun {
		var clientOpts []sdkclient.Option
		clientOpts = append(clientOpts, sdkclient.WithClientType(sdkclient.ClientTypeHeadless))
		if resolvedProvider != "" && resolvedModel != "" {
			clientOpts = append(clientOpts, sdkclient.WithProvider(sdkclient.Provider(resolvedProvider), resolvedModel))
		} else if resolvedProvider != "" {
			clientOpts = append(clientOpts, sdkclient.WithProvider(sdkclient.Provider(resolvedProvider), ""))
		}

		var err error
		llm, err = sdkclient.New(clientOpts...)
		if err != nil {
			return fmt.Errorf("create client: %w (check ~/.swarmos/config.json or pass --provider/--model)", err)
		}
		defer llm.Stop(context.Background())
		info := llm.ProviderInfo()
		fmt.Fprintf(stdout, "Using model  : %s / %s\n", info.Name, info.Model)
	} else {
		fmt.Fprintf(stdout, "Dry-run mode : no AI calls, showing first-message preview\n")
	}

	fmt.Fprintf(stdout, "Storage      : %s\n", *storageDir)

	// ── Phase 1: metadata-only scan ──────────────────────────────────────────
	dbg("opening scan storage")
	scanStore, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{
		BaseDir: *storageDir,
	})
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}

	ctx := context.Background()
	dbg("querying all conversations (ExcludeMessages=true)")
	allConvs, err := scanStore.Query(ctx, storage.Filter{
		WorkspacePath:   *workspace,
		ExcludeMessages: true,
		SortBy:          "updated_at",
		SortOrder:       storage.SortDescending,
	})
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}

	total := len(allConvs)
	fmt.Fprintf(stdout, "Total convs  : %d\n", total)

	var needRetitle []string
	for _, conv := range allConvs {
		if !conversationHasMeaningfulTitle(conv) {
			needRetitle = append(needRetitle, conv.ID)
		}
	}
	fmt.Fprintf(stdout, "Need title   : %d\n", len(needRetitle))

	if len(needRetitle) == 0 {
		fmt.Fprintln(stdout, "\nNothing to do — all conversations already have titles.")
		return nil
	}

	if *limit > 0 && len(needRetitle) > *limit {
		needRetitle = needRetitle[:*limit]
		fmt.Fprintf(stdout, "Limit        : %d (--limit)\n", *limit)
	}

	toProcess := len(needRetitle)
	fmt.Fprintf(stdout, "\nProcessing %d conversations at concurrency %d...\n\n", toProcess, *concurrency)

	// ── Phase 2: load + generate + save ──────────────────────────────────────
	// Fresh storage instance to avoid cache pollution from Phase 1.
	dbg("opening save storage (separate instance from scan store)")
	saveStore, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{
		BaseDir: *storageDir,
	})
	if err != nil {
		return fmt.Errorf("open save storage: %w", err)
	}

	type result struct {
		id    string
		title string
		err   error
		skip  bool
	}

	sem := make(chan struct{}, *concurrency)
	results := make(chan result, toProcess)
	start := time.Now()

	for _, convID := range needRetitle {
		sem <- struct{}{}
		go func(id string) {
			defer func() { <-sem }()

			dbg("loading conv %s", id)
			conv, err := saveStore.Load(ctx, id)
			if err != nil {
				dbg("load error %s: %v", id, err)
				results <- result{id: id, err: fmt.Errorf("load: %w", err)}
				return
			}

			if *dryRun {
				title := extractTitleFromConversation(conv)
				if title == "" {
					dbg("no extractable title for %s (dry-run)", id)
					results <- result{id: id, skip: true}
					return
				}
				results <- result{id: id, title: "[preview] " + title}
				return
			}

			prompt := buildTitlePrompt(conv)
			if prompt == "" {
				dbg("no usable messages for %s — skipping", id)
				results <- result{id: id, skip: true}
				return
			}

			dbg("calling LLM for %s", id)
			title, err := llm.Generate(ctx, prompt)
			if err != nil {
				dbg("generate error %s: %v", id, err)
				results <- result{id: id, err: fmt.Errorf("generate: %w", err)}
				return
			}
			title = strings.TrimSpace(strings.Trim(strings.TrimSpace(title), `"'`))
			if title == "" || len(title) > 100 {
				dbg("invalid title for %s: %q", id, title)
				results <- result{id: id, skip: true}
				return
			}

			conv.Title = title
			if err := saveStore.Save(ctx, conv); err != nil {
				dbg("save error %s: %v", id, err)
				results <- result{id: id, err: fmt.Errorf("save: %w", err)}
				return
			}
			dbg("saved title for %s: %q", id, title)
			results <- result{id: id, title: title}
		}(convID)
	}

	// ── Drain results with live progress ─────────────────────────────────────
	var done, titled, skipped, errors int64
	progressLine := func() string {
		d := int(atomic.LoadInt64(&done))
		elapsed := time.Since(start)
		rate := float64(d) / elapsed.Seconds()
		remaining := toProcess - d
		var eta string
		if rate > 0 && remaining > 0 {
			sec := float64(remaining) / rate
			if sec < 60 {
				eta = fmt.Sprintf("%.0fs", sec)
			} else {
				eta = fmt.Sprintf("%.0fm%.0fs", sec/60, float64(int(sec)%60))
			}
		} else {
			eta = "—"
		}
		return fmt.Sprintf(
			"  [%d/%d done | ✓ %d titled | ⊘ %d skipped | ✗ %d errors | %.1f/s | ETA %s]",
			d, toProcess,
			atomic.LoadInt64(&titled),
			atomic.LoadInt64(&skipped),
			atomic.LoadInt64(&errors),
			rate, eta,
		)
	}

	for i := 0; i < toProcess; i++ {
		r := <-results
		atomic.AddInt64(&done, 1)

		switch {
		case r.err != nil:
			atomic.AddInt64(&errors, 1)
			fmt.Fprintf(stderr, "  ✗ %s: %v\n", r.id, r.err)
		case r.skip:
			atomic.AddInt64(&skipped, 1)
			// Only show skips at debug level — they're noise otherwise.
			dbg("skip %s — no extractable content", r.id)
		default:
			atomic.AddInt64(&titled, 1)
			fmt.Fprintf(stdout, "  ✓ %s → %s\n", r.id, r.title)
		}

		// Print progress after every result (overwrite previous progress line
		// with \r then print the updated line followed by \r so the next
		// result line overwrites it cleanly).
		fmt.Fprintf(stdout, "\r%s\r", progressLine())
	}

	// Final newline to clear the progress line, then summary.
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout)

	elapsed := time.Since(start).Round(time.Millisecond)
	rate := float64(titled+skipped+errors) / elapsed.Seconds()
	fmt.Fprintf(stdout, "Done in %v  (%.1f convs/s)\n", elapsed, rate)
	fmt.Fprintf(stdout, "  Scanned : %d\n", total)
	fmt.Fprintf(stdout, "  Needed  : %d\n", toProcess)
	if *dryRun {
		fmt.Fprintf(stdout, "  Preview : %d  (dry-run — nothing saved)\n", titled)
	} else {
		fmt.Fprintf(stdout, "  Titled  : %d\n", titled)
		fmt.Fprintf(stdout, "  Skipped : %d\n", skipped)
		fmt.Fprintf(stdout, "  Errors  : %d\n", errors)
	}
	return nil
}

// runConversationsResummarize backfills ConversationSummary.Recap using a real
// LLM call, mirroring runConversationsRetitle but for summaries. This is the
// live proof harness for the summary (recap) last mile.
func runConversationsResummarize(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("conversations backfill-summaries", flag.ContinueOnError)
	fs.SetOutput(stderr)
	provider := fs.String("provider", "", "Provider override")
	model := fs.String("model", "", "Model override")
	workspace := fs.String("workspace", "", "Scope to workspace path")
	limit := fs.Int("limit", 0, "Max conversations to process (0 = all)")
	debug := fs.Bool("debug", false, "Show internal debug logs")

	homeDir, _ := os.UserHomeDir()
	defaultStorageDir := ""
	if homeDir != "" {
		defaultStorageDir = homeDir + "/.swarmos/conversations"
	}
	storageDir := fs.String("storage-dir", defaultStorageDir, "Conversation storage directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *storageDir == "" {
		return fmt.Errorf("--storage-dir is required (or set HOME)")
	}
	dbg := func(format string, a ...any) {
		if *debug {
			fmt.Fprintf(stdout, "[debug] "+format+"\n", a...)
		}
	}

	resolvedProvider, resolvedModel := activeProfileMainModel()
	if *provider != "" {
		resolvedProvider = *provider
	}
	if *model != "" {
		resolvedModel = *model
	}

	var clientOpts []sdkclient.Option
	clientOpts = append(clientOpts, sdkclient.WithClientType(sdkclient.ClientTypeHeadless))
	if resolvedProvider != "" && resolvedModel != "" {
		clientOpts = append(clientOpts, sdkclient.WithProvider(sdkclient.Provider(resolvedProvider), resolvedModel))
	} else if resolvedProvider != "" {
		clientOpts = append(clientOpts, sdkclient.WithProvider(sdkclient.Provider(resolvedProvider), ""))
	}
	llm, err := sdkclient.New(clientOpts...)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer llm.Stop(context.Background())
	info := llm.ProviderInfo()
	fmt.Fprintf(stdout, "Using model  : %s / %s\n", info.Name, info.Model)
	fmt.Fprintf(stdout, "Storage      : %s\n", *storageDir)

	scanStore, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{BaseDir: *storageDir})
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	ctx := context.Background()
	allConvs, err := scanStore.Query(ctx, storage.Filter{
		WorkspacePath:   *workspace,
		ExcludeMessages: true,
		SortBy:          "updated_at",
		SortOrder:       storage.SortDescending,
	})
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}
	fmt.Fprintf(stdout, "Total convs  : %d\n", len(allConvs))

	var need []string
	for _, conv := range allConvs {
		if conv.Summary == nil || strings.TrimSpace(conv.Summary.Recap) == "" {
			need = append(need, conv.ID)
		}
	}
	fmt.Fprintf(stdout, "Need recap   : %d\n", len(need))
	if len(need) == 0 {
		fmt.Fprintln(stdout, "\nNothing to do — all conversations already have recaps.")
		return nil
	}
	if *limit > 0 && len(need) > *limit {
		need = need[:*limit]
	}

	saveStore, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{BaseDir: *storageDir})
	if err != nil {
		return fmt.Errorf("open save storage: %w", err)
	}

	summarized, skipped, errCount := 0, 0, 0
	for _, id := range need {
		conv, err := saveStore.Load(ctx, id)
		if err != nil {
			errCount++
			fmt.Fprintf(stderr, "  ✗ %s: load: %v\n", id, err)
			continue
		}
		prompt := buildSummaryPrompt(conv)
		if prompt == "" {
			skipped++
			dbg("skip %s — not enough content", id)
			continue
		}
		recap, err := llm.Generate(ctx, prompt)
		if err != nil {
			errCount++
			fmt.Fprintf(stderr, "  ✗ %s: generate: %v\n", id, err)
			continue
		}
		recap = strings.TrimSpace(strings.Trim(strings.TrimSpace(recap), "\"'`"))
		if recap == "" {
			skipped++
			continue
		}
		if len(recap) > 600 {
			recap = strings.TrimSpace(recap[:600])
		}
		conv.EnsureSummary().Recap = recap
		if err := saveStore.Save(ctx, conv); err != nil {
			errCount++
			fmt.Fprintf(stderr, "  ✗ %s: save: %v\n", id, err)
			continue
		}
		summarized++
		fmt.Fprintf(stdout, "  ✓ %s → %.80s\n", id, recap)
	}

	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  Summarized : %d\n", summarized)
	fmt.Fprintf(stdout, "  Skipped    : %d\n", skipped)
	fmt.Fprintf(stdout, "  Errors     : %d\n", errCount)
	return nil
}

// buildSummaryPrompt mirrors spawnSummaryAgent's prompt in the TUI.
func buildSummaryPrompt(conv *conversation.Conversation) string {
	var msgs []string
	for _, msg := range conv.Messages {
		if len(msgs) >= 8 {
			break
		}
		content := stripSystemXMLTags(msg.Content)
		if strings.TrimSpace(content) == "" {
			continue
		}
		if len(content) > 400 {
			content = content[:400] + "..."
		}
		msgs = append(msgs, fmt.Sprintf("%s: %s", msg.Role, content))
	}
	if len(msgs) < 2 {
		return ""
	}
	return fmt.Sprintf(`Summarize this conversation in 2-3 short sentences.

Rules:
- Describe what the user wanted and what was discussed or done.
- Be concrete and specific; no filler like "This conversation is about".
- Plain prose, no bullet points, no headings, no quotes.

Conversation:
%s`, strings.Join(msgs, "\n"))
}

// activeProfileMainModel reads ~/.swarmos/agent_profiles.json and returns the
// provider + model for the active profile's "main" role.
func activeProfileMainModel() (provider, model string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	mgr := profiles.NewManager(filepath.Join(homeDir, ".swarmos"))
	profile, err := mgr.GetActiveProfile()
	if err != nil || profile == nil {
		return "", ""
	}
	if rc, ok := profile.Roles["main"]; ok {
		if p := rc.Chain.Primary.Provider; p != "" {
			return p, rc.Chain.Primary.Model
		}
	}
	for _, rc := range profile.Roles {
		if rc.Chain.Primary.Provider != "" && rc.Chain.Primary.Model != "" {
			return rc.Chain.Primary.Provider, rc.Chain.Primary.Model
		}
	}
	return "", ""
}

// conversationHasMeaningfulTitle returns true when the conversation has a
// non-empty title that is not the "New Chat" default placeholder.
func conversationHasMeaningfulTitle(conv *conversation.Conversation) bool {
	if conv == nil {
		return false
	}
	if conv.Title != "" && conv.Title != "New Chat" {
		return true
	}
	if conv.Metadata.Custom != nil {
		if t, ok := conv.Metadata.Custom["custom_title"].(string); ok && t != "" && t != "New Chat" {
			return true
		}
	}
	return false
}

// extractTitleFromConversation extracts a short title from the first user
// message — used only for --dry-run preview (no AI call).
func extractTitleFromConversation(conv *conversation.Conversation) string {
	if conv == nil {
		return ""
	}
	if conv.Metadata.Custom != nil {
		if t, ok := conv.Metadata.Custom["custom_title"].(string); ok && t != "" {
			return t
		}
	}
	if conv.Summary != nil && conv.Summary.FirstUserPrompt != "" {
		if t := cleanTitleText(conv.Summary.FirstUserPrompt); t != "" {
			return truncateTitleStr(t, 60)
		}
	}
	for _, msg := range conv.Messages {
		if msg.Role == conversation.RoleUser && msg.Content != "" {
			if t := cleanTitleText(msg.Content); t != "" {
				return truncateTitleStr(t, 60)
			}
		}
	}
	return ""
}

// buildTitlePrompt builds the same prompt that spawnNamingAgent uses in the TUI.
func buildTitlePrompt(conv *conversation.Conversation) string {
	var msgs []string
	for _, msg := range conv.Messages {
		if len(msgs) >= 3 {
			break
		}
		content := cleanTitleText(msg.Content)
		if content == "" {
			continue
		}
		role := "user"
		if msg.Role == conversation.RoleAssistant {
			role = "assistant"
		}
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		msgs = append(msgs, role+": "+content)
	}
	if len(msgs) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Generate a concise, descriptive title (max 50 characters) for this conversation.\n"+
			"The title should capture the main topic or goal.\n\n"+
			"Recent messages:\n%s\n\n"+
			"Respond with ONLY the title text, nothing else. No quotes, no explanation.",
		strings.Join(msgs, "\n"),
	)
}

// cleanTitleText strips system-injected XML tags and returns the first line.
func cleanTitleText(content string) string {
	text := stripSystemXMLTags(content)
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// stripSystemXMLTags removes system-injected XML-like block tags and content.
func stripSystemXMLTags(s string) string {
	for {
		openIdx := strings.Index(s, "<")
		if openIdx == -1 {
			break
		}
		closeIdx := strings.Index(s[openIdx:], ">")
		if closeIdx == -1 {
			break
		}
		closeIdx += openIdx
		tagContent := s[openIdx+1 : closeIdx]
		fields := strings.Fields(tagContent)
		if len(fields) == 0 {
			s = s[:openIdx] + s[closeIdx+1:]
			continue
		}
		tagName := strings.TrimPrefix(fields[0], "/")
		endTag := "</" + tagName + ">"
		endIdx := strings.Index(s, endTag)
		if endIdx != -1 && endIdx > openIdx {
			s = s[:openIdx] + s[endIdx+len(endTag):]
		} else {
			if strings.HasPrefix(tagName, "system-") || tagName == "context" || tagName == "antml:thinking" {
				s = s[:openIdx]
				break
			}
			s = s[:openIdx] + s[closeIdx+1:]
		}
	}
	return strings.TrimSpace(s)
}

func truncateTitleStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
