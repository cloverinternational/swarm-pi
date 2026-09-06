package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
)

func indexedHistorySearch(ctx context.Context, sdkC *sdkclient.Client, request historytools.SearchRequest) ([]historytools.Summary, error) {
	files, err := historySourceFiles(sdkC, request.WorkspacePath)
	if err != nil {
		return fallbackHistorySearch(ctx, sdkC, nil, request)
	}
	engine, err := openHistoryIndex()
	if err != nil {
		return fallbackHistorySearch(ctx, sdkC, files, request)
	}
	defer engine.Close()

	loadedConversations := make(map[string]*conversation.Conversation)
	var loadedConversationsMu sync.Mutex
	loadSegments := func(ctx context.Context, file searchindex.SourceFile) ([]searchindex.Segment, error) {
		if file.FileSize > searchindex.MaxConversationFileSize {
			return streamingConversationSegments(ctx, file)
		}
		full, loadErr := sdkC.LoadConversation(ctx, file.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		loadedConversationsMu.Lock()
		loadedConversations[file.ID] = full
		loadedConversationsMu.Unlock()
		return conversationSegments(full), nil
	}
	load := func(ctx context.Context, file searchindex.SourceFile, loadBody bool) (searchindex.Document, bool, error) {
		loadedConversationsMu.Lock()
		full := loadedConversations[file.ID]
		delete(loadedConversations, file.ID)
		loadedConversationsMu.Unlock()
		if full == nil {
			var loadErr error
			full, loadErr = sdkC.LoadConversation(ctx, file.ID)
			if loadErr != nil {
				return searchindex.Document{}, false, loadErr
			}
		}
		return historyDocument(full, loadBody), true, nil
	}
	if err := searchindex.SyncFiles(ctx, engine, request.WorkspacePath, files, request.SearchBody, load, searchindex.WithSegments(loadSegments)); err != nil {
		return fallbackHistorySearch(ctx, sdkC, files, request)
	}
	if segmentSearchRequested(request) {
		return indexedSegmentHistorySearch(ctx, engine, sdkC, files, request)
	}
	indexed, err := engine.Search(ctx, searchindex.Query{
		Text: request.Query, SearchBody: request.SearchBody,
		WorkspacePath: request.WorkspacePath, Origin: request.Origin,
		ExcludeHeadless: request.Origin == "", MinMessages: request.MinMessages,
		Sort: request.Sort, Order: request.Order, Limit: request.Limit,
	})
	if err != nil {
		return fallbackHistorySearch(ctx, sdkC, files, request)
	}
	results := make([]historytools.Summary, 0, len(indexed))
	for _, item := range indexed {
		results = append(results, historytools.Summary{
			ID: item.ID, Title: item.Title, Preview: item.Preview,
			Body:         item.Body,
			MessageCount: item.MessageCount, UpdatedAt: item.UpdatedAt,
			WorkspacePath: item.WorkspacePath, Origin: item.Origin,
			SearchScore: item.Score, SearchMatched: strings.TrimSpace(request.Query) != "",
		})
	}
	return results, nil
}

func segmentSearchRequested(request historytools.SearchRequest) bool {
	return request.ToolName != "" || request.ToolOutcome != "" || request.SegmentKind != "" ||
		request.Stats || request.NGram != 0 || request.TopTerms != 0
}

func segmentFilter(request historytools.SearchRequest) searchindex.SegmentFilter {
	return searchindex.SegmentFilter{
		WorkspacePath:  request.WorkspacePath,
		ToolName:       request.ToolName,
		Kind:           request.SegmentKind,
		Outcome:        request.ToolOutcome,
		ExcludeRuntime: request.ExcludeRuntime,
	}
}

func indexedSegmentHistorySearch(
	ctx context.Context,
	engine searchindex.Engine,
	sdkC *sdkclient.Client,
	files []searchindex.SourceFile,
	request historytools.SearchRequest,
) ([]historytools.Summary, error) {
	segments, ok := engine.(searchindex.SegmentEngine)
	if !ok {
		return fallbackHistorySearch(ctx, sdkC, files, request)
	}
	filter := segmentFilter(request)
	if request.Stats {
		// Model-facing statistics are formatted by history.SearchTool through its
		// injected SegmentEngine. Keep this direct callback path exercising the
		// same filter/options and preserving its fallback contract; []Summary has
		// no representation for frequency rows.
		if _, err := segments.TermStats(ctx, filter, searchindex.StatsOptions{
			NGram: request.NGram,
			Top:   request.TopTerms,
		}); err != nil {
			return fallbackHistorySearch(ctx, sdkC, files, request)
		}
		return nil, nil
	}
	hits, err := segments.SearchSegments(ctx, searchindex.SegmentQuery{
		Filter: filter,
		Text:   request.Query,
		Limit:  searchindex.MaxStatsTop,
	})
	if err != nil {
		return fallbackHistorySearch(ctx, sdkC, files, request)
	}
	states, err := engine.States(ctx, request.WorkspacePath)
	if err != nil {
		return fallbackHistorySearch(ctx, sdkC, files, request)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = searchindex.DefaultSegmentLimit
	}
	results := make([]historytools.Summary, 0, min(limit, len(hits)))
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if _, duplicate := seen[hit.ConversationID]; duplicate {
			continue
		}
		seen[hit.ConversationID] = struct{}{}
		item, exists := states[hit.ConversationID]
		if !exists || item.MessageCount < request.MinMessages ||
			(request.Origin != "" && item.Origin != request.Origin) ||
			(request.Origin == "" && item.Origin == "headless") {
			continue
		}
		results = append(results, historytools.Summary{
			ID: item.ID, Title: item.Title, Preview: item.Preview,
			MessageCount: item.MessageCount, UpdatedAt: item.UpdatedAt,
			WorkspacePath: item.WorkspacePath, Origin: item.Origin,
			SearchScore: hit.Score, SearchMatched: strings.TrimSpace(request.Query) != "",
		})
		if len(results) == limit {
			break
		}
	}
	return results, nil
}

func historySourceFiles(sdkC *sdkclient.Client, workspace string) ([]searchindex.SourceFile, error) {
	rooted, ok := sdkC.Storage().(interface{ BaseDir() string })
	if !ok || rooted.BaseDir() == "" {
		return nil, fmt.Errorf("history search storage does not expose a filesystem root")
	}
	root := rooted.BaseDir()
	if workspace != "" {
		return historySourceFilesInDir(filepath.Join(root, storage.EncodeWorkspacePath(workspace)), workspace)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var files []searchindex.SourceFile
	for _, entry := range entries {
		if !entry.IsDir() || !storage.IsWorkspaceDir(entry.Name()) {
			continue
		}
		workspacePath, err := storage.DecodeWorkspacePath(entry.Name())
		if err != nil {
			continue
		}
		workspaceFiles, err := historySourceFilesInDir(filepath.Join(root, entry.Name()), workspacePath)
		if err != nil {
			return nil, err
		}
		files = append(files, workspaceFiles...)
	}
	return files, nil
}

func historySourceFilesInDir(dir, workspace string) ([]searchindex.SourceFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]searchindex.SourceFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, searchindex.SourceFile{
			ID:              strings.TrimSuffix(entry.Name(), ".json"),
			Path:            filepath.Join(dir, entry.Name()),
			WorkspacePath:   workspace,
			FileModUnixNano: info.ModTime().UnixNano(),
			FileSize:        info.Size(),
		})
	}
	return files, nil
}

func historyDocument(conv *conversation.Conversation, includeBody bool) searchindex.Document {
	if conv == nil {
		return searchindex.Document{}
	}
	preview := historytools.FirstSubstantiveUserText(conv.Messages)
	if preview == "" && conv.Summary != nil {
		preview = conv.Summary.FirstUserPrompt
	}
	if preview == "" && len(conv.Messages) > 0 && conv.Messages[0] != nil {
		preview = conv.Messages[0].Content
	}
	preview = historytools.CleanText(preview)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	messageCount := len(conv.Messages)
	if conv.Summary != nil && len(conv.Messages) <= 1 {
		messageCount = conv.Summary.MessageCount
	}
	doc := searchindex.Document{
		ID: conv.ID, WorkspacePath: conv.WorkspacePath, Title: conv.Title,
		Preview: preview, MessageCount: messageCount, UpdatedAt: conv.UpdatedAt,
		Origin: conversationOrigin(conv),
	}
	if doc.Title == "" {
		doc.Title = historytools.DeriveTitle(doc.Preview)
	}
	if includeBody {
		doc.Body = searchableConversationBody(conv)
		doc.BodyIndexed = true
	}
	return doc
}

func conversationOrigin(conv *conversation.Conversation) string {
	if conv != nil && conv.Metadata.Custom != nil {
		if origin, ok := conv.Metadata.Custom["origin"].(string); ok {
			switch origin {
			case "interactive", "subagent", "headless":
				return origin
			}
		}
	}
	if hasConversationTag(conv, "subagent") {
		return "subagent"
	}
	if hasConversationTag(conv, conversation.HeadlessTag) {
		return "headless"
	}
	return "interactive"
}

func hasConversationTag(conv *conversation.Conversation, tag string) bool {
	if conv == nil {
		return false
	}
	for _, candidate := range conv.Metadata.Tags {
		if candidate == tag {
			return true
		}
	}
	return false
}

func fallbackHistorySearch(ctx context.Context, sdkC *sdkclient.Client, files []searchindex.SourceFile, request historytools.SearchRequest) ([]historytools.Summary, error) {
	if files == nil {
		conversations, err := sdkC.ListConversations(ctx, sdkclient.ListOptions{
			WorkspacePath:   request.WorkspacePath,
			IncludeHeadless: request.Origin == "headless" || request.Origin == "subagent",
		})
		if err != nil {
			return nil, err
		}
		return scanHistoryConversations(ctx, sdkC, conversations, request), nil
	}
	includeHeadless := request.Origin == "headless" || request.Origin == "subagent"
	conversations := make([]sdkclient.ConversationSummary, 0, len(files))
	for _, file := range files {
		if file.FileSize > searchindex.MaxConversationFileSize {
			continue
		}
		full, err := sdkC.LoadConversation(ctx, file.ID)
		if err != nil {
			continue
		}
		if !includeHeadless && hasConversationTag(full, conversation.HeadlessTag) {
			continue
		}
		doc := historyDocument(full, false)
		conversations = append(conversations, sdkclient.ConversationSummary{
			ID: doc.ID, WorkspacePath: doc.WorkspacePath, Title: doc.Title,
			Preview: doc.Preview, MessageCount: doc.MessageCount, UpdatedAt: doc.UpdatedAt,
			Origin: doc.Origin,
		})
	}
	return scanHistoryConversations(ctx, sdkC, conversations, request), nil
}

func openHistoryIndex() (searchindex.Engine, error) {
	dir := filepath.Join(paths.ConversationsDir(), "_index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	backend := searchindex.Backend(strings.ToLower(strings.TrimSpace(os.Getenv("SWARM_HISTORY_INDEX_BACKEND"))))
	if backend == "" {
		backend = searchindex.BackendAuto
	}
	engine, _, err := searchindex.Open(context.Background(), searchindex.OpenOptions{
		Backend:    backend,
		TursoPath:  filepath.Join(dir, "history-search.turso"),
		SQLitePath: filepath.Join(dir, "history-search.sqlite"),
	})
	return engine, err
}

func scanHistoryConversations(ctx context.Context, sdkC *sdkclient.Client, conversations []sdkclient.ConversationSummary, request historytools.SearchRequest) []historytools.Summary {
	results := make([]historytools.Summary, 0, len(conversations))
	for _, item := range conversations {
		if item.MessageCount < request.MinMessages || (request.Origin != "" && item.Origin != request.Origin) {
			continue
		}
		summary := historytools.Summary{
			ID: item.ID, Title: item.Title, Preview: item.Preview,
			MessageCount: item.MessageCount, UpdatedAt: item.UpdatedAt,
			WorkspacePath: item.WorkspacePath, Origin: item.Origin,
		}
		if request.SearchBody {
			if full, err := sdkC.LoadConversation(ctx, item.ID); err == nil {
				summary.MessageCount = len(full.Messages)
				if substantive := historytools.FirstSubstantiveUserText(full.Messages); substantive != "" {
					summary.Preview = substantive
				}
				summary.Body = searchableConversationBody(full)
			}
		}
		results = append(results, summary)
	}
	return results
}

func searchableConversationBody(conv *conversation.Conversation) string {
	if conv == nil {
		return ""
	}
	var body strings.Builder
	for _, message := range conv.Messages {
		if message == nil || (message.Role != conversation.RoleUser && message.Role != conversation.RoleAssistant) {
			continue
		}
		if cleaned := historytools.CleanText(message.Content); cleaned != "" {
			body.WriteString(cleaned)
			body.WriteByte('\n')
		}
	}
	return body.String()
}
