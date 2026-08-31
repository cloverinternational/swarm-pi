// Command history-probe is a CLI dogfooding shim for the HistorySearch and
// HistoryGet tools. It wires the real internal/tools/history tools to the real
// on-disk conversation storage (DirectoryFileStorage) exactly the way the TUI's
// sdk_integration does, so we can reproduce and iterate on history-tool bugs
// from the shell without booting the whole TUI.
//
// Usage:
//
//	go run ./cmd/history-probe -scope all -query "task tool"
//	go run ./cmd/history-probe -workspace /home/swarm/Work/mono -query auth
//	go run ./cmd/history-probe -id conv_1768783023327180829 -get
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
)

func main() {
	var (
		baseDir     = flag.String("base", defaultBaseDir(), "conversation storage base dir")
		workspace   = flag.String("workspace", mustCwd(), "current workspace path")
		scope       = flag.String("scope", "current", "search scope: current|all")
		query       = flag.String("query", "", "search query")
		limit       = flag.Int("limit", 10, "max results")
		id          = flag.String("id", "", "conversation id (for -get)")
		doGet       = flag.Bool("get", false, "run HistoryGet on -id instead of search")
		wsPath      = flag.String("workspace-path", "", "exact workspace_path param (search/get)")
		anyWs       = flag.Bool("any", false, "HistoryGet: read across any workspace (all_workspaces)")
		searchBody  = flag.Bool("search-body", true, "HistorySearch: include substantive message bodies")
		sortBy      = flag.String("sort", "", "HistorySearch: relevance|recency|message_count")
		order       = flag.String("order", "desc", "HistorySearch: asc|desc")
		minMessages = flag.Int("min-messages", 0, "HistorySearch: minimum stored messages")
		origin      = flag.String("origin", "", "HistorySearch: interactive|subagent|headless")
		tail        = flag.Int("tail", 0, "HistoryGet: final N messages")
		offset      = flag.Int("offset", -1, "HistoryGet: zero-based stored-message offset")
		cpuProfile  = flag.String("cpuprofile", "", "write a CPU profile to this path")
		memProfile  = flag.String("memprofile", "", "write a heap profile to this path after the run")
		humanOnly   = flag.Bool("human-only", false, "HistoryGet: omit system/tool/boilerplate messages")
	)
	flag.Parse()

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fatal("create cpu profile: %v", err)
		}
		defer func() { _ = f.Close() }()
		if err := pprof.StartCPUProfile(f); err != nil {
			fatal("start cpu profile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}
	if *memProfile != "" {
		defer func() {
			f, err := os.Create(*memProfile)
			if err != nil {
				return
			}
			defer func() { _ = f.Close() }()
			runtime.GC()
			_ = pprof.WriteHeapProfile(f)
		}()
	}

	store, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{BaseDir: *baseDir})
	if err != nil {
		fatal("open storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	searchFn := func(ctx context.Context, request historytools.SearchRequest) ([]historytools.Summary, error) {
		convs, qErr := store.Query(ctx, storage.Filter{
			WorkspacePath:       request.WorkspacePath,
			FirstMessagePreview: map[bool]int{true: 0, false: 1}[request.SearchBody],
			SortBy:              "updated_at",
			SortOrder:           storage.SortDescending,
		})
		if qErr != nil {
			return nil, qErr
		}
		out := make([]historytools.Summary, 0, len(convs))
		for _, c := range convs {
			summary := summaryFromConv(c)
			if request.SearchBody {
				var body strings.Builder
				for _, message := range c.Messages {
					if message == nil || (message.Role != conversation.RoleUser && message.Role != conversation.RoleAssistant) {
						continue
					}
					if cleaned := historytools.CleanText(message.Content); cleaned != "" {
						body.WriteString(cleaned)
						body.WriteByte('\n')
					}
				}
				summary.Body = body.String()
			}
			out = append(out, summary)
		}
		return out, nil
	}

	if *doGet {
		getTool := historytools.NewGetTool(*workspace, store.Load)
		params := map[string]any{"conversation_id": *id}
		if *wsPath != "" {
			params["workspace_path"] = *wsPath
		}
		if *anyWs {
			params["all_workspaces"] = true
		}
		if *tail > 0 {
			params["tail"] = *tail
		}
		if *offset >= 0 {
			params["offset"] = *offset
		}
		if *humanOnly {
			params["human_only"] = true
		}
		res, execErr := getTool.Execute(ctx, params)
		emit(res, execErr)
		return
	}

	searchTool := historytools.NewSearchToolWithOptions(*workspace, searchFn)
	params := map[string]any{
		"scope": *scope, "query": *query, "limit": *limit, "search_body": *searchBody,
		"order": *order, "min_messages": *minMessages,
	}
	if *sortBy != "" {
		params["sort"] = *sortBy
	}
	if *origin != "" {
		params["origin"] = *origin
	}
	if *wsPath != "" {
		params["workspace_path"] = *wsPath
		delete(params, "scope")
	}
	res, execErr := searchTool.Execute(ctx, params)
	emit(res, execErr)
}

// summaryFromConv mirrors client.summaryFromConv (unexported) so the shim
// exercises the exact same mapping the TUI uses.
func summaryFromConv(c *conversation.Conversation) historytools.Summary {
	if c == nil {
		return historytools.Summary{}
	}
	source := ""
	if substantive := historytools.FirstSubstantiveUserText(c.Messages); substantive != "" {
		source = substantive
	} else if c.Summary != nil && c.Summary.FirstUserPrompt != "" {
		source = c.Summary.FirstUserPrompt
	} else if len(c.Messages) > 0 && c.Messages[0] != nil {
		source = c.Messages[0].Content
	}
	preview := truncate(historytools.CleanText(source), 200)
	return historytools.Summary{
		ID:            c.ID,
		Title:         c.Title,
		Preview:       preview,
		MessageCount:  conversationMessageCount(c),
		UpdatedAt:     c.UpdatedAt,
		WorkspacePath: c.WorkspacePath,
		Origin:        conversationOrigin(c),
	}
}

func conversationMessageCount(c *conversation.Conversation) int {
	if c.Summary != nil && len(c.Messages) <= 1 {
		return c.Summary.MessageCount
	}
	return len(c.Messages)
}

func conversationOrigin(c *conversation.Conversation) string {
	if c.Metadata.Custom != nil {
		if origin, ok := c.Metadata.Custom["origin"].(string); ok && origin != "" {
			return origin
		}
	}
	for _, tag := range c.Metadata.Tags {
		if tag == "subagent" {
			return "subagent"
		}
		if tag == conversation.HeadlessTag {
			return "headless"
		}
	}
	return "interactive"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func emit(res *tools.ToolResult, err error) {
	if err != nil {
		fatal("execute: %v", err)
	}
	fmt.Println(res.Output)
}

func defaultBaseDir() string {
	return paths.ConversationsDir()
}

func mustCwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "/"
	}
	return d
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
