package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 50
	defaultMaxMessages = 20
	maxMessageLimit    = 100
	defaultMaxChars    = 12000
	maxCharsLimit      = 50000
)

type Summary struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Preview       string    `json:"preview,omitempty"`
	MessageCount  int       `json:"message_count"`
	UpdatedAt     time.Time `json:"updated_at"`
	WorkspacePath string    `json:"workspace_path,omitempty"`
	Origin        string    `json:"origin,omitempty"`
	// Body is searchable text supplied by body-aware backends. It is never
	// returned in HistorySearch output.
	Body string `json:"-"`
	// SearchScore/SearchMatched let an index backend return ranked body matches
	// without materializing the complete body back into the tool process.
	SearchScore   float64 `json:"-"`
	SearchMatched bool    `json:"-"`
}

// SearchFunc lists conversations matching query. workspacePath is the exact
// canonical workspace to search, or empty to search all workspaces.
type SearchFunc func(ctx context.Context, query, workspacePath string) ([]Summary, error)
type SearchRequest struct {
	Query         string
	WorkspacePath string
	SearchBody    bool
	Sort          string
	Order         string
	MinMessages   int
	Origin        string
	Limit         int
	// Fields below were added for regex/field/snippet post-filtering. Their ZERO
	// VALUES mean "exactly the legacy behavior", so existing backends that never
	// read or set them keep compiling and behaving identically.
	//
	// Regex is informational for backends that can pre-narrow; the authoritative
	// regex evaluation happens as a post-filter inside this package because
	// SQLite FTS5 cannot evaluate RE2.
	Regex          string
	CaseSensitive  bool
	Fields         []string
	ExcludeRuntime bool
	// Segment search and statistics. Every zero value preserves the legacy
	// conversation-level search behavior.
	ToolName    string
	ToolOutcome string
	SegmentKind string
	Stats       bool
	NGram       int
	TopTerms    int
}
type SearchOptionsFunc func(context.Context, SearchRequest) ([]Summary, error)
type GetFunc func(context.Context, string) (*conversation.Conversation, error)

// segmentSearcher is the narrow part of searchindex.SegmentEngine used by
// HistorySearch. Keeping it local lets callers inject any compatible index
// without this package constructing or depending on a concrete engine.
type segmentSearcher interface {
	SearchSegments(context.Context, searchindex.SegmentQuery) ([]searchindex.SegmentHit, error)
	TermStats(context.Context, searchindex.SegmentFilter, searchindex.StatsOptions) ([]searchindex.TermStat, error)
	SegmentCount(context.Context, searchindex.SegmentFilter) (int, error)
}

type SearchTool struct {
	tools.BaseTool
	workspace string
	search    SearchFunc
	searchV2  SearchOptionsFunc
	segments  segmentSearcher
}

func NewSearchTool(workspace string, search SearchFunc) *SearchTool {
	return &SearchTool{workspace: workspace, search: search}
}

func NewSearchToolWithOptions(workspace string, search SearchOptionsFunc, segments ...segmentSearcher) *SearchTool {
	tool := &SearchTool{workspace: workspace, searchV2: search}
	if len(segments) > 0 {
		tool.segments = segments[0]
	}
	return tool
}
func (*SearchTool) Name() string { return "HistorySearch" }
func (*SearchTool) Description() string {
	return "Search prior conversations by title, preview, or substantive message text. Defaults to the current workspace (the common case — just pass a query). Set scope=\"all\" to search across every workspace, but only do that when the current workspace has no match or the user explicitly asks to look across projects. You normally do NOT need to pass workspace_path; it is an advanced override for pinning one exact absolute workspace. Returns bounded conversation metadata and provenance, not full transcripts. Use HistoryGet only for selected conversation IDs."
}
func (*SearchTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"query":          map[string]any{"type": "string", "description": "Case-insensitive title, preview, and message-body query. Empty lists recent conversations."},
		"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchLimit, "default": defaultSearchLimit},
		"scope":          map[string]any{"type": "string", "enum": []string{"current", "all"}, "default": "current", "description": "Default \"current\" searches this workspace. Use \"all\" only when the current workspace has no match or the user explicitly asks to search across all projects."},
		"workspace_path": map[string]any{"type": "string", "description": "Advanced: exact absolute workspace path to search. You rarely need this — prefer the default current scope, or scope=all. Mutually exclusive with scope=all."},
		"search_body":    map[string]any{"type": "boolean", "description": "Search substantive user/assistant message text in addition to title and preview. Defaults to true when query or regex is non-empty."},
		"sort":           map[string]any{"type": "string", "enum": []string{"relevance", "recency", "message_count"}, "description": "Sort results by relevance, recency, or message count. Defaults to relevance for a query or regex and recency when both are empty."},
		"order":          map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "desc"},
		"min_messages":   map[string]any{"type": "integer", "minimum": 0, "description": "Exclude conversations with fewer stored messages."},
		"origin":         map[string]any{"type": "string", "enum": []string{"interactive", "subagent", "headless"}, "description": "Filter by conversation origin."},
		"regex": map[string]any{
			"type":        "string",
			"description": "Go RE2 regular expression applied to the selected fields. Reach for this when a plain query cannot express the shape you want: commit prefixes (`fix\\(hooks\\):\\s+\\w+`), identifiers (`PR #\\d{3}`), alternation (`panic|fatal error`), or anchors. RE2 syntax only — lookahead and backreferences are NOT supported and return a clear error. Combines with `query`: the query narrows candidates cheaply and the regex then filters them, so both must match. An invalid pattern is reported as an error, never silently ignored.",
		},
		"case_sensitive": map[string]any{
			"type": "boolean", "default": false,
			"description": "Match case-exactly. Applies to both plain query terms and `regex`. Default false (case-insensitive; the regex is compiled with a leading (?i) so character classes still behave correctly). Set true to distinguish `Error` from `error`, or a Go exported identifier from a local one.",
		},
		"fields": map[string]any{
			"type": "array", "items": map[string]any{"type": "string", "enum": []string{"title", "preview", "body"}},
			"description": "Restrict matching to these fields. Default (omitted) searches all of them, which is today's behavior. Use [\"title\"] to find conversations ABOUT a topic rather than ones that merely mention it in passing; use [\"body\"] to find a string that was said mid-conversation. Note `body` is only consulted when search_body is on.",
		},
		"snippet": map[string]any{
			"type": "boolean", "default": false,
			"description": "Return the matching text with surrounding context plus which field matched, instead of only a generic preview. Turn this on whenever you need to know WHY a conversation matched — it is the difference between a list of IDs and actual evidence. Snippets are bounded in length and count and never split a multi-byte character.",
		},
		"snippet_context": map[string]any{
			"type": "integer", "minimum": 1, "maximum": maxSnippetContext, "default": defaultSnippetContext,
			"description": "Bytes of surrounding context on each side of a match in a snippet. Only meaningful with snippet=true.",
		},
		"max_snippets": map[string]any{
			"type": "integer", "minimum": 1, "maximum": maxSnippetsPerResult, "default": defaultMaxSnippets,
			"description": "Maximum snippets returned per result. Only meaningful with snippet=true.",
		},
		"exclude_runtime": map[string]any{
			"type": "boolean", "default": false,
			"description": "Ignore runtime-injected pseudo-messages when matching message bodies. A `user` role is NOT proof a human typed it: the runtime injects `<system-reminder`, `[SCHEDULED]`, `## MCP Context`, `## Current Tasks` and `**Current Mode**` blocks into the user turn. Set true when you are looking for something a PERSON actually said and do not want scaffolding boilerplate to match. Same predicate as HistoryGet's human_only.",
		},
		"tool_name": map[string]any{
			"type":        "string",
			"description": "Filter to segments for one tool name, for example \"Bash\". This matches only tool segments, so setting it searches tool traffic rather than ordinary conversation prose.",
		},
		"tool_outcome": map[string]any{
			"type":        "string",
			"enum":        []string{"any", "failed", "succeeded"},
			"description": "Filter tool results by outcome: any, failed, or succeeded. Setting this implies tool-result segments because only tool results have an outcome.",
		},
		"segment_kind": map[string]any{
			"type":        "string",
			"enum":        []string{"message", "tool_call", "tool_result"},
			"description": "Filter matches to one segment kind: message prose, tool_call input, or tool_result output.",
		},
		"stats": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Return a frequency table INSTEAD OF a conversation list. This changes the shape of the output. Use it to study vocabulary or recurring phrasing across many conversations; do not use it when you are trying to find and open one conversation.",
		},
		"ngram": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"maximum":     searchindex.MaxStatsNGram,
			"default":     1,
			"description": "Phrase length for stats, from 1 to 5. Use 1 for vocabulary; 2 or 3 for useful recurring phrases. Higher values usually produce near-unique strings. Only meaningful with stats=true.",
		},
		"top_terms": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"maximum":     searchindex.MaxStatsTop,
			"default":     searchindex.DefaultStatsTop,
			"description": "Maximum frequency rows to return. Defaults to 50 and is capped at 500 because large frequency tables can flood a terminal or model context window. Only meaningful with stats=true.",
		},
	}}
}
func (t *SearchTool) IsIdempotent() bool { return true }
func (t *SearchTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if t.search == nil && t.searchV2 == nil {
		return nil, errors.New("HistorySearch unavailable: no search backend")
	}
	workspace, err := canonicalWorkspace(t.workspace)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: invalid current workspace: %w", err)
	}
	selectedWorkspace, scope, err := searchWorkspace(params, workspace)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	query, _ := params["query"].(string)
	opts, err := parseMatchOptions(params)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	limit, err := boundedIntParam(params, "limit", defaultSearchLimit, maxSearchLimit)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	searchBody, err := boolParam(params, "search_body", strings.TrimSpace(query) != "" || opts.Regex != nil)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	defaultSort := defaultHistorySort(query)
	if opts.Regex != nil {
		defaultSort = "relevance"
	}
	sortBy, err := enumParam(params, "sort", defaultSort, "relevance", "recency", "message_count")
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	order, err := enumParam(params, "order", "desc", "asc", "desc")
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	minMessages, err := nonNegativeIntParam(params, "min_messages", 0)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	origin, err := optionalEnumParam(params, "origin", "interactive", "subagent", "headless")
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	segmentOpts, err := parseSegmentOptions(params)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	request := SearchRequest{
		Query: query, WorkspacePath: selectedWorkspace, SearchBody: searchBody,
		Sort: sortBy, Order: order, MinMessages: minMessages, Origin: origin, Limit: limit,
		Regex: opts.RegexSource, CaseSensitive: opts.CaseSensitive,
		Fields: fieldNames(opts.Fields), ExcludeRuntime: opts.ExcludeRuntime,
		ToolName: segmentOpts.ToolName, ToolOutcome: segmentOpts.ToolOutcome,
		SegmentKind: segmentOpts.SegmentKind, Stats: segmentOpts.Stats,
		NGram: segmentOpts.RequestNGram, TopTerms: segmentOpts.RequestTopTerms,
	}
	if segmentOpts.active() {
		if t.segments == nil {
			return nil, errors.New("HistorySearch: segment index unavailable; configure a segment-capable history index before using tool_name, tool_outcome, segment_kind, stats, ngram, or top_terms")
		}
		return t.executeSegmentSearch(ctx, request, segmentOpts, opts, selectedWorkspace, scope)
	}
	// Over-fetch when a post-filter is active: the filter runs after the backend
	// returns, so asking for exactly `limit` rows would systematically under-fill
	// the result set. See candidateFetchLimit for the multiplier/cap reasoning.
	request.Limit = candidateFetchLimit(limit, opts)
	var items []Summary
	if t.searchV2 != nil {
		items, err = t.searchV2(ctx, request)
	} else {
		items, err = t.search(ctx, query, selectedWorkspace)
	}
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	candidatePoolExhausted := false
	if opts.filterActive() {
		if t.searchV2 != nil {
			candidatePoolExhausted = len(items) >= request.Limit
		} else if len(items) > request.Limit {
			// The legacy callback has no limit argument. Bound its returned pool
			// locally so extended matching cannot scan an unbounded result slice.
			items = items[:request.Limit]
			candidatePoolExhausted = true
		}
	}
	terms := parseTermsWithCase(query, opts.CaseSensitive)
	type ranked struct {
		item  Summary
		score float64
		snips []Snippet
	}
	matches := make([]ranked, 0, len(items))
	candidatesConsidered := 0
	for _, item := range items {
		if selectedWorkspace != "" && !workspaceMatches(selectedWorkspace, item.WorkspacePath) {
			continue
		}
		if item.MessageCount < minMessages || (origin != "" && item.Origin != origin) {
			continue
		}
		candidatesConsidered++
		cleaned, titleHaystack, bodyHaystack := prepareSummary(item, searchBody)
		if opts.filterActive() {
			// Extended matching path. Backend-supplied SearchMatched/SearchScore
			// are NOT trusted here: the backend cannot evaluate regex, field
			// targeting or runtime exclusion, so this package is authoritative.
			score, ok, snips := postFilterMatch(item, cleaned, opts, terms, searchBody)
			if !ok {
				continue
			}
			matches = append(matches, ranked{item: cleaned, score: float64(score), snips: snips})
			continue
		}
		score, ok := scoreSummary(titleHaystack, bodyHaystack, terms)
		finalScore := float64(score)
		if item.SearchMatched {
			ok = true
			finalScore = item.SearchScore
		}
		if !ok {
			continue
		}
		var snips []Snippet
		if opts.Snippets {
			snips = collectSnippets(fieldTexts(item, cleaned, opts, searchBody), opts, terms)
		}
		matches = append(matches, ranked{item: cleaned, score: finalScore, snips: snips})
	}
	// Apply the requested ordering after all filters, before limiting.
	sort.SliceStable(matches, func(i, j int) bool {
		cmp := 0
		switch sortBy {
		case "message_count":
			if matches[i].item.MessageCount != matches[j].item.MessageCount {
				if matches[i].item.MessageCount < matches[j].item.MessageCount {
					cmp = -1
				} else {
					cmp = 1
				}
			}
		case "recency":
			if matches[i].item.UpdatedAt.Before(matches[j].item.UpdatedAt) {
				cmp = -1
			} else if matches[i].item.UpdatedAt.After(matches[j].item.UpdatedAt) {
				cmp = 1
			}
		default:
			if matches[i].score != matches[j].score {
				if matches[i].score < matches[j].score {
					cmp = -1
				} else {
					cmp = 1
				}
			}
		}
		if cmp == 0 && matches[i].item.UpdatedAt != matches[j].item.UpdatedAt {
			if matches[i].item.UpdatedAt.Before(matches[j].item.UpdatedAt) {
				cmp = -1
			} else {
				cmp = 1
			}
		}
		// Stable sort preserves backend order for exact ties.
		if order == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})
	truncated := len(matches) > limit
	if truncated {
		matches = matches[:limit]
	}
	// Build compact result rows that carry the same information with fewer tokens:
	// omit empty/zero fields, drop per-row workspace_path unless it actually varies
	// (scope=all), and trim nanosecond timestamps. The top-level workspace_path
	// already states the workspace for current/exact scope, so repeating it on
	// every row is pure redundancy.
	results := make([]map[string]any, len(matches))
	for i, m := range matches {
		item := m.item
		row := map[string]any{"id": item.ID, "title": item.Title}
		if item.Preview != "" && item.Preview != item.Title {
			row["preview"] = item.Preview
		}
		if item.MessageCount > 0 {
			row["message_count"] = item.MessageCount
		}
		if !item.UpdatedAt.IsZero() {
			row["updated_at"] = item.UpdatedAt.UTC().Format(time.RFC3339)
		}
		if scope == "all" && item.WorkspacePath != "" {
			row["workspace_path"] = item.WorkspacePath
		}
		if item.Origin != "" {
			row["origin"] = item.Origin
		}
		if len(m.snips) > 0 {
			snips := make([]map[string]any, len(m.snips))
			for j, s := range m.snips {
				snips[j] = map[string]any{"field": s.Field, "text": s.Text}
			}
			row["snippets"] = snips
		}
		results[i] = row
	}
	reportedQuery := strings.TrimSpace(query)
	if !opts.CaseSensitive {
		reportedQuery = strings.ToLower(reportedQuery)
	}
	out := map[string]any{
		"scope": scope, "query": reportedQuery,
		"sort": sortBy, "order": order, "results": results,
	}
	if selectedWorkspace != "" {
		out["workspace_path"] = selectedWorkspace
	}
	if truncated {
		out["truncated"] = true
	}
	// Honest incompleteness reporting: with a post-filter active we only ever saw
	// `request.Limit` candidates. If the backend filled that pool AND we still
	// could not fill `limit` results, more matches may exist beyond the pool —
	// say so rather than letting the caller read an empty tail as "no more".
	if opts.filterActive() {
		out["post_filtered"] = true
		out["candidates_examined"] = candidatesConsidered
		if candidatePoolExhausted {
			out["candidate_pool_exhausted"] = true
		}
		if candidatePoolExhausted && len(matches) < limit {
			out["incomplete"] = true
		}
	}
	return jsonResult(out)
}

// fieldNames converts resolved match fields back to plain strings for the
// backend request. Returns nil (not an empty slice) when unset so the zero
// value keeps meaning "legacy behavior".
func fieldNames(fields []matchField) []string {
	if len(fields) == 0 {
		return nil
	}
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = string(f)
	}
	return out
}

// parseMatchOptions validates and resolves the extended matching parameters.
// When none are supplied it returns the zero matchOptions, whose active() is
// false, guaranteeing the legacy code path is taken unchanged.
func parseMatchOptions(params map[string]any) (matchOptions, error) {
	var opts matchOptions
	caseSensitive, err := boolParam(params, "case_sensitive", false)
	if err != nil {
		return opts, err
	}
	opts.CaseSensitive = caseSensitive

	if value, ok := params["regex"]; ok && value != nil {
		pattern, valid := value.(string)
		if !valid {
			return opts, errors.New("regex must be a string")
		}
		// The pattern is compiled verbatim; no caller text is interpolated into it.
		compiled, compileErr := compileSearchRegex(pattern, caseSensitive)
		if compileErr != nil {
			return opts, compileErr
		}
		opts.Regex = compiled
		if compiled != nil {
			opts.RegexSource = pattern
		}
	}

	if value, ok := params["fields"]; ok && value != nil {
		raw, valid := value.([]any)
		if !valid {
			names, stringSlice := value.([]string)
			if !stringSlice {
				return opts, errors.New("fields must be an array of title, preview or body")
			}
			fields, fieldErr := parseMatchFields(names)
			if fieldErr != nil {
				return opts, fieldErr
			}
			opts.Fields = fields
		} else {
			names := make([]string, 0, len(raw))
			for _, entry := range raw {
				name, isString := entry.(string)
				if !isString {
					return opts, errors.New("fields must be an array of title, preview or body")
				}
				names = append(names, name)
			}
			fields, fieldErr := parseMatchFields(names)
			if fieldErr != nil {
				return opts, fieldErr
			}
			opts.Fields = fields
		}
	}

	snippets, err := boolParam(params, "snippet", false)
	if err != nil {
		return opts, err
	}
	opts.Snippets = snippets
	opts.SnippetContext, err = boundedIntParam(params, "snippet_context", defaultSnippetContext, maxSnippetContext)
	if err != nil {
		return opts, err
	}
	opts.MaxSnippets, err = boundedIntParam(params, "max_snippets", defaultMaxSnippets, maxSnippetsPerResult)
	if err != nil {
		return opts, err
	}
	excludeRuntime, err := boolParam(params, "exclude_runtime", false)
	if err != nil {
		return opts, err
	}
	opts.ExcludeRuntime = excludeRuntime
	return opts, nil
}

func searchWorkspace(params map[string]any, currentWorkspace string) (workspace, scope string, err error) {
	scope = "current"
	if value, ok := params["scope"]; ok && value != nil {
		var valid bool
		scope, valid = value.(string)
		if !valid || (scope != "current" && scope != "all") {
			return "", "", errors.New("scope must be one of current or all")
		}
	}
	requested, hasRequested := params["workspace_path"]
	if hasRequested && requested != nil {
		path, valid := requested.(string)
		if !valid || path == "" {
			return "", "", errors.New("workspace_path must be an absolute non-root path")
		}
		if scope == "all" {
			return "", "", errors.New("workspace_path and scope=all are mutually exclusive")
		}
		workspace, err = canonicalWorkspace(path)
		if err != nil {
			return "", "", fmt.Errorf("invalid workspace_path: %w", err)
		}
		return workspace, "current", nil
	}
	if scope == "all" {
		return "", scope, nil
	}
	return currentWorkspace, scope, nil
}

func defaultHistorySort(query string) string {
	if strings.TrimSpace(query) == "" {
		return "recency"
	}
	return "relevance"
}

func boolParam(params map[string]any, name string, fallback bool) (bool, error) {
	value, ok := params[name]
	if !ok || value == nil {
		return fallback, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return result, nil
}

func enumParam(params map[string]any, name, fallback string, allowed ...string) (string, error) {
	value, ok := params[name]
	if !ok || value == nil {
		return fallback, nil
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be one of %s", name, strings.Join(allowed, ", "))
	}
	for _, candidate := range allowed {
		if result == candidate {
			return result, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s", name, strings.Join(allowed, ", "))
}

func optionalEnumParam(params map[string]any, name string, allowed ...string) (string, error) {
	if value, ok := params[name]; !ok || value == nil {
		return "", nil
	}
	return enumParam(params, name, "", allowed...)
}

func nonNegativeIntParam(params map[string]any, name string, fallback int) (int, error) {
	value, ok := params[name]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		if typed == 0 {
			return 0, nil
		}
	case int8:
		if typed == 0 {
			return 0, nil
		}
	case int16:
		if typed == 0 {
			return 0, nil
		}
	case int32:
		if typed == 0 {
			return 0, nil
		}
	case int64:
		if typed == 0 {
			return 0, nil
		}
	case uint, uint8, uint16, uint32, uint64:
		if fmt.Sprint(typed) == "0" {
			return 0, nil
		}
	case float32:
		if typed == 0 {
			return 0, nil
		}
	case float64:
		if typed == 0 {
			return 0, nil
		}
	case json.Number:
		if typed.String() == "0" {
			return 0, nil
		}
	}
	result, err := boundedIntParam(params, name, 1, math.MaxInt)
	if err != nil {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return result, nil
}

type GetTool struct {
	tools.BaseTool
	workspace string
	get       GetFunc
}

func NewGetTool(workspace string, get GetFunc) *GetTool {
	return &GetTool{workspace: workspace, get: get}
}
func (*GetTool) Name() string { return "HistoryGet" }
func (*GetTool) Description() string {
	return "Read a bounded excerpt from one prior conversation selected with HistorySearch. Defaults to the current workspace — just pass conversation_id. If the conversation lives in another project (e.g. it came from a scope=all search) OR the user explicitly asks to read across projects, set all_workspaces=true to read it by ID regardless of directory. You do NOT need to pass workspace_path; that is an advanced exact-path override. Returns message IDs, roles, timestamps, and truncation provenance."
}
func (*GetTool) Parameters() any {
	return map[string]any{"type": "object", "required": []string{"conversation_id"}, "properties": map[string]any{
		"conversation_id": map[string]any{"type": "string"},
		"all_workspaces":  map[string]any{"type": "boolean", "default": false, "description": "Read the conversation by ID regardless of which workspace it belongs to. Use this when the ID came from a scope=all search or the user asks to read across projects. Preferred over passing workspace_path."},
		"workspace_path":  map[string]any{"type": "string", "description": "Advanced: exact absolute workspace path containing the conversation. You normally do not need this — use the current-workspace default or all_workspaces=true. Ignored when all_workspaces=true."},
		"max_messages":    map[string]any{"type": "integer", "minimum": 1, "maximum": maxMessageLimit, "default": defaultMaxMessages},
		"max_chars":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxCharsLimit, "default": defaultMaxChars},
		"tail":            map[string]any{"type": "integer", "minimum": 1, "maximum": maxMessageLimit, "description": "Return the final N stored messages. Mutually exclusive with offset."},
		"offset":          map[string]any{"type": "integer", "minimum": 0, "description": "Zero-based start position in the original stored message sequence. Uses max_messages as the page size. Mutually exclusive with tail."},
		"human_only":      map[string]any{"type": "boolean", "default": false, "description": "Omit system/tool messages and machine-only reminder boilerplate."},
	}}
}
func (*GetTool) IsIdempotent() bool { return true }
func (t *GetTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if t.get == nil {
		return nil, errors.New("HistoryGet unavailable: no conversation backend")
	}
	workspace, err := canonicalWorkspace(t.workspace)
	if err != nil {
		return nil, fmt.Errorf("HistoryGet: invalid current workspace: %w", err)
	}
	anyWorkspace := false
	if value, ok := params["all_workspaces"]; ok && value != nil {
		flag, valid := value.(bool)
		if !valid {
			return nil, errors.New("HistoryGet: all_workspaces must be a boolean")
		}
		anyWorkspace = flag
	}
	selectedWorkspace := workspace
	if value, ok := params["workspace_path"]; ok && value != nil && !anyWorkspace {
		path, valid := value.(string)
		if !valid || path == "" {
			return nil, errors.New("HistoryGet: workspace_path must be an absolute non-root path")
		}
		selectedWorkspace, err = canonicalWorkspace(path)
		if err != nil {
			return nil, fmt.Errorf("HistoryGet: invalid workspace_path: %w", err)
		}
	}
	id, _ := params["conversation_id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("HistoryGet: conversation_id is required")
	}
	maxMessages, err := boundedIntParam(params, "max_messages", defaultMaxMessages, maxMessageLimit)
	if err != nil {
		return nil, fmt.Errorf("HistoryGet: %w", err)
	}
	maxChars, err := boundedIntParam(params, "max_chars", defaultMaxChars, maxCharsLimit)
	if err != nil {
		return nil, fmt.Errorf("HistoryGet: %w", err)
	}
	humanOnly, err := boolParam(params, "human_only", false)
	if err != nil {
		return nil, fmt.Errorf("HistoryGet: %w", err)
	}
	_, hasTail := params["tail"]
	_, hasOffset := params["offset"]
	if hasTail && hasOffset {
		return nil, errors.New("HistoryGet: tail and offset are mutually exclusive")
	}
	tail := 0
	if hasTail {
		tail, err = boundedIntParam(params, "tail", defaultMaxMessages, maxMessageLimit)
		if err != nil {
			return nil, fmt.Errorf("HistoryGet: %w", err)
		}
	}
	offset := 0
	if hasOffset {
		offset, err = nonNegativeIntParam(params, "offset", 0)
		if err != nil {
			return nil, fmt.Errorf("HistoryGet: %w", err)
		}
	}
	conv, err := t.get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("HistoryGet: %w", err)
	}
	if conv == nil {
		return nil, errors.New("HistoryGet: conversation not found")
	}
	// By default a conversation is only readable from its own workspace. When the
	// caller opts into all_workspaces we read it by ID regardless of directory, and
	// report the conversation's real workspace back so the agent has provenance.
	if anyWorkspace {
		selectedWorkspace = conv.WorkspacePath
	} else if !workspaceMatches(selectedWorkspace, conv.WorkspacePath) {
		return nil, errors.New("HistoryGet: conversation does not match the requested workspace")
	}
	totalMessageCount := len(conv.Messages)
	start, end := 0, totalMessageCount
	switch {
	case hasOffset:
		start = offset
		if start > totalMessageCount {
			start = totalMessageCount
		}
		end = start + maxMessages
		if end > totalMessageCount {
			end = totalMessageCount
		}
	case hasTail:
		if totalMessageCount > tail {
			start = totalMessageCount - tail
		}
	default:
		if totalMessageCount > maxMessages {
			start = totalMessageCount - maxMessages
		}
	}
	messages := make([]map[string]any, 0, end-start)
	remaining := maxChars
	omittedMessageCount := start + (totalMessageCount - end)
	filteredMessageCount := 0
	contentTruncated := false
	for index := end - 1; index >= start; index-- {
		if remaining == 0 {
			omittedMessageCount += index - start + 1
			contentTruncated = true
			break
		}
		message := conv.Messages[index]
		if message == nil {
			filteredMessageCount++
			continue
		}
		if humanOnly {
			if message.Role != conversation.RoleUser && message.Role != conversation.RoleAssistant {
				filteredMessageCount++
				continue
			}
		}
		rawContent := message.Content
		if humanOnly {
			rawContent = CleanText(rawContent)
			if rawContent == "" || (message.Role == conversation.RoleUser && !isSubstantiveUserText(rawContent)) {
				filteredMessageCount++
				continue
			}
		}
		row, encodedSize, truncated := boundedHistoryRow(message, rawContent, humanOnly, remaining)
		if row == nil {
			omittedMessageCount += index - start + 1
			contentTruncated = true
			break
		}
		remaining -= encodedSize
		contentTruncated = contentTruncated || truncated
		messages = append(messages, row)
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	out := map[string]any{
		"conversation_id": conv.ID, "workspace_path": selectedWorkspace, "messages": messages,
		"total_message_count": totalMessageCount, "window_start": start, "window_end": end,
		"rendered_message_count": len(messages),
	}
	if conv.Title != "" {
		out["title"] = conv.Title
	}
	if omittedMessageCount > 0 || contentTruncated {
		out["truncated"] = true
	}
	if contentTruncated {
		out["content_truncated"] = true
	}
	if omittedMessageCount > 0 {
		out["omitted_message_count"] = omittedMessageCount
	}
	if filteredMessageCount > 0 {
		out["filtered_message_count"] = filteredMessageCount
	}
	if totalMessageCount == 0 || len(messages) == 0 {
		out["empty"] = true
	}
	out["metadata_truncated"] = false
	return jsonResult(out, maxChars)
}

var (
	authorizationSecret = regexp.MustCompile(`(?i)(authorization\s*:\s*(?:bearer|basic)\s+)\S+`)
	assignmentSecret    = regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|password|secret)\s*[=:]\s*)[^\s"'&]+`)
	sudoEchoSecret      = regexp.MustCompile(`(?i)(echo\s+["'])[^"']+(["']\s*\|\s*sudo\s+-S)`)
	systemReminder      = regexp.MustCompile(`(?is)<system-reminder[^>]*>.*?</system-reminder>`)
)

func cleanHumanHistoryText(value string) string {
	value = strings.TrimSpace(systemReminder.ReplaceAllString(value, " "))
	lower := strings.ToLower(value)
	for _, prefix := range []string{"[scheduled]", "## mcp context", "## current tasks", "**current mode**", "[task nudge]", "[skill reminder]"} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	return value
}

func boundedHistoryRow(message *conversation.Message, content string, humanOnly bool, budget int) (map[string]any, int, bool) {
	row := map[string]any{
		"id":        message.ID,
		"timestamp": message.Timestamp,
		"role":      message.Role,
		"content":   "",
	}
	baseSize := encodedJSONSize(row)
	if baseSize > budget {
		return nil, 0, true
	}
	truncated := fitStringField(row, "content", content, budget)
	if humanOnly {
		return row, encodedJSONSize(row), truncated
	}

	for _, call := range message.ToolCalls {
		callRow := map[string]any{
			"id":         call.ID,
			"name":       call.Name,
			"parameters": sanitizeStructuredValue(call.Parameters, ""),
		}
		calls, _ := row["tool_calls"].([]map[string]any)
		candidate := append(append([]map[string]any(nil), calls...), callRow)
		row["tool_calls"] = candidate
		if encodedJSONSize(row) > budget {
			if len(calls) == 0 {
				delete(row, "tool_calls")
			} else {
				row["tool_calls"] = calls
			}
			truncated = true
			break
		}
	}
	for _, result := range message.ToolResults {
		resultRow := sanitizedToolResult(result)
		results, _ := row["tool_results"].([]map[string]any)
		candidate := append(append([]map[string]any(nil), results...), resultRow)
		row["tool_results"] = candidate
		if encodedJSONSize(row) > budget {
			if len(results) == 0 {
				delete(row, "tool_results")
			} else {
				row["tool_results"] = results
			}
			truncated = true
			break
		}
	}
	return row, encodedJSONSize(row), truncated
}

func fitStringField(row map[string]any, key, value string, budget int) bool {
	value = redactInlineSecrets(value)
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		row[key] = string(runes[:mid])
		if encodedJSONSize(row) <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	row[key] = string(runes[:low])
	return low < len(runes)
}

func encodedJSONSize(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return math.MaxInt
	}
	return len(data)
}

func sanitizedToolResult(result conversation.ToolResult) map[string]any {
	row := map[string]any{
		"call_id": result.CallID,
		"name":    result.Name,
		"output":  redactInlineSecrets(result.Output),
	}
	if result.Error != nil {
		row["error"] = map[string]any{
			"type":    result.Error.Type,
			"message": redactInlineSecrets(result.Error.Message),
		}
	}
	if len(result.Content) > 0 {
		content := make([]map[string]any, 0, len(result.Content))
		for _, block := range result.Content {
			item := map[string]any{"type": block.Type}
			if block.MimeType != "" {
				item["mime_type"] = block.MimeType
			}
			if len(block.Data) > 0 {
				item["binary"] = fmt.Sprintf("[binary omitted: %s, %d bytes]", block.MimeType, len(block.Data))
			}
			if block.Text != "" {
				item["text"] = redactInlineSecrets(block.Text)
			}
			content = append(content, item)
		}
		row["content"] = content
	}
	return row
}

func sanitizeStructuredValue(value any, key string) any {
	if sensitiveKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[childKey] = sanitizeStructuredValue(childValue, childKey)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, childValue := range typed {
			out[index] = sanitizeStructuredValue(childValue, key)
		}
		return out
	case string:
		return redactInlineSecrets(typed)
	case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return value
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return "[unavailable]"
		}
		var normalized any
		if err := json.Unmarshal(data, &normalized); err != nil {
			return "[unavailable]"
		}
		return sanitizeStructuredValue(normalized, key)
	}
}

func redactInlineSecrets(value string) string {
	value = authorizationSecret.ReplaceAllString(value, `${1}[REDACTED]`)
	value = assignmentSecret.ReplaceAllString(value, `${1}[REDACTED]`)
	return sudoEchoSecret.ReplaceAllString(value, `${1}[REDACTED]${2}`)
}

func sensitiveKey(key string) bool {
	key = strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', '.', ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, strings.ToLower(key))
	for _, marker := range []string{
		"apikey", "authorization", "token", "password", "secret", "credential",
		"privatekey", "cookie", "sessionid", "clientsecret",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func runePrefix(content string, limit int) (string, int, bool) {
	if limit == 0 {
		return "", 0, content != ""
	}
	count := 0
	for byteIndex := range content {
		if count == limit {
			return content[:byteIndex], count, true
		}
		count++
	}
	return content, count, false
}

func boundedIntParam(params map[string]any, name string, fallback, maximum int) (int, error) {
	value, ok := params[name]
	if !ok || value == nil {
		return fallback, nil
	}

	var result int
	switch typed := value.(type) {
	case int:
		result = typed
	case int8:
		result = int(typed)
	case int16:
		result = int(typed)
	case int32:
		result = int(typed)
	case int64:
		if typed > int64(math.MaxInt) || typed < int64(math.MinInt) {
			return 0, invalidIntegerParam(name, maximum)
		}
		result = int(typed)
	case uint:
		if uint64(typed) > uint64(math.MaxInt) {
			return 0, invalidIntegerParam(name, maximum)
		}
		result = int(typed)
	case uint8:
		result = int(typed)
	case uint16:
		result = int(typed)
	case uint32:
		result = int(typed)
	case uint64:
		if typed > uint64(math.MaxInt) {
			return 0, invalidIntegerParam(name, maximum)
		}
		result = int(typed)
	case float32:
		value64 := float64(typed)
		if math.IsNaN(value64) || math.IsInf(value64, 0) || math.Trunc(value64) != value64 || value64 > float64(math.MaxInt) || value64 < float64(math.MinInt) {
			return 0, invalidIntegerParam(name, maximum)
		}
		result = int(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed > float64(math.MaxInt) || typed < float64(math.MinInt) {
			return 0, invalidIntegerParam(name, maximum)
		}
		result = int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil || parsed > int64(math.MaxInt) || parsed < int64(math.MinInt) {
			return 0, invalidIntegerParam(name, maximum)
		}
		result = int(parsed)
	default:
		return 0, invalidIntegerParam(name, maximum)
	}
	if result < 1 || result > maximum {
		return 0, invalidIntegerParam(name, maximum)
	}
	return result, nil
}

func invalidIntegerParam(name string, maximum int) error {
	return fmt.Errorf("%s must be an integer between 1 and %d", name, maximum)
}

func canonicalWorkspace(workspace string) (string, error) {
	if workspace == "" {
		return "", errors.New("workspace path is empty")
	}
	if !filepath.IsAbs(workspace) {
		return "", errors.New("workspace path must be absolute")
	}
	workspace = filepath.Clean(workspace)
	if filepath.Dir(workspace) == workspace {
		return "", errors.New("workspace path must not be a filesystem root")
	}
	return workspace, nil
}

// workspaceMatches intentionally compares cleaned absolute paths without
// resolving symlinks. A conversation recorded through one symlink alias is not
// visible from another alias unless both records use the same path spelling.
func workspaceMatches(currentWorkspace, candidateWorkspace string) bool {
	candidate, err := canonicalWorkspace(candidateWorkspace)
	return err == nil && candidate == currentWorkspace
}

func jsonResult(value any, maxChars ...int) (*tools.ToolResult, error) {
	if len(maxChars) > 0 {
		response, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("bounded JSON result requires an object")
		}
		return boundedJSONResult(response, maxChars[0])
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return tools.NewToolResult(string(data)), nil
}

// boundedJSONResult applies HistoryGet's max_chars limit to the complete JSON
// response, including metadata, truncation fields, formatting, and syntax.
func boundedJSONResult(response map[string]any, maxChars int) (*tools.ToolResult, error) {
	encode := func() ([]byte, error) {
		return json.Marshal(response)
	}
	data, err := encode()
	if err != nil {
		return nil, err
	}
	if len(data) <= maxChars {
		return tools.NewToolResult(string(data)), nil
	}

	metadataTruncated := false
	for _, key := range []string{"title", "workspace_path", "conversation_id"} {
		if len(data) <= maxChars {
			break
		}
		value, _ := response[key].(string)
		runes := []rune(value)
		low, high := 0, len(runes)
		for low < high {
			mid := (low + high + 1) / 2
			response[key] = string(runes[:mid])
			response["metadata_truncated"] = true
			candidate, marshalErr := encode()
			if marshalErr != nil {
				return nil, marshalErr
			}
			if len(candidate) <= maxChars {
				low = mid
			} else {
				high = mid - 1
			}
		}
		response[key] = string(runes[:low])
		metadataTruncated = metadataTruncated || low < len(runes)
		response["metadata_truncated"] = metadataTruncated
		data, err = encode()
		if err != nil {
			return nil, err
		}
	}
	if messages, ok := response["messages"].([]map[string]any); ok {
		for len(messages) > 0 && len(data) > maxChars {
			messages = messages[1:]
			response["messages"] = messages
			response["truncated"] = true
			response["content_truncated"] = true
			if omitted, ok := response["omitted_message_count"].(int); ok {
				response["omitted_message_count"] = omitted + 1
			}
			data, err = encode()
			if err != nil {
				return nil, err
			}
		}
	}
	if len(data) <= maxChars {
		return tools.NewToolResult(string(data)), nil
	}

	// Budgets below the minimum provenance envelope still return valid JSON
	// while honoring the hard limit.
	if maxChars >= 18 {
		return tools.NewToolResult(`{"truncated":true}`), nil
	}
	if maxChars >= 2 {
		return tools.NewToolResult(`{}`), nil
	}
	return tools.NewToolResult(`0`), nil
}
