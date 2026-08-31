package history

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
)

func TestCleanTextStripsMachineNoise(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "naming envelope with output marker",
			in:   `JSON only. Message: "ok so run it and debug the welcome screen"\nOutput: {"action":"x"}`,
			want: "ok so run it and debug the welcome screen",
		},
		{
			name: "truncated naming envelope no output marker",
			in:   `JSON only. Message: "We are going to fix the user intro welcome screen. Currentl`,
			want: "We are going to fix the user intro welcome screen. Currentl",
		},
		{
			name: "nested naming envelope",
			in:   `JSON only. Message: "JSON only. Message: "We are going to fix the welcome screen`,
			want: "We are going to fix the welcome screen",
		},
		{
			name: "full system-reminder block wrapping real prompt",
			in:   "<system-reminder source=\"user_prompt_submit\">\n[Task Nudge] You have no active tasks.\n</system-reminder>\n\nPlease refactor the auth module",
			want: "Please refactor the auth module",
		},
		{
			name: "unicode-escaped system-reminder block",
			in:   `\u003csystem-reminder source=\"user_prompt_submit\"\u003e[Task Nudge] none\u003c/system-reminder\u003e Please add pagination`,
			want: "Please add pagination",
		},
		{
			name: "clean text unchanged",
			in:   "Fix the welcome screen viewport bug",
			want: "Fix the welcome screen viewport bug",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanText(c.in); got != c.want {
				t.Fatalf("CleanText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// fullRuntimeGuidance mimics the large injected runtime block that the TUI
// prepends to the first user message. Its FIRST advertised skill is the
// Google-font/Next.js skill that was leaking into conversation titles.
const fullRuntimeGuidance = `<swarm_runtime_guidance><swarm_runtime_skills><available_skills>` +
	`<skill><name>add-google-font-nextjs-landing</name>` +
	`<description>Add and apply a Google font (e.g. Inter) to a specific page/route in a Next.js App Router project using next/font/google.</description></skill>` +
	`<skill><name>lang-port</name><description>Universal language-to-language code porting skill.</description></skill>` +
	`</available_skills></swarm_runtime_skills>` +
	`<swarm_runtime_capabilities>read write bash</swarm_runtime_capabilities></swarm_runtime_guidance>`

func TestCleanTextStripsInjectedRuntimeScaffolding(t *testing.T) {
	t.Run("full runtime guidance then real prompt", func(t *testing.T) {
		in := fullRuntimeGuidance + "\n\nfix the compaction blocking-limit bug"
		got := CleanText(in)
		if got != "fix the compaction blocking-limit bug" {
			t.Fatalf("CleanText() = %q, want %q", got, "fix the compaction blocking-limit bug")
		}
		lower := strings.ToLower(got)
		for _, banned := range []string{"google", "font", "skill"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("cleaned text still contains %q: %q", banned, got)
			}
		}
	})

	t.Run("bare available_skills then real prompt", func(t *testing.T) {
		in := `<available_skills><skill><name>add-google-font-nextjs-landing</name>` +
			`<description>Add a Google font to Next.js.</description></skill></available_skills>` +
			"\n\nrefactor the storage pooled loader"
		got := CleanText(in)
		if got != "refactor the storage pooled loader" {
			t.Fatalf("CleanText() = %q, want %q", got, "refactor the storage pooled loader")
		}
		lower := strings.ToLower(got)
		if strings.Contains(lower, "google") || strings.Contains(lower, "font") {
			t.Fatalf("font text leaked: %q", got)
		}
	})

	t.Run("truncated available_skills with no close", func(t *testing.T) {
		in := "<available_skills>\n  <skill><name>add-google-font-nextjs-landing</name>"
		got := CleanText(in)
		if got != "" {
			t.Fatalf("CleanText() = %q, want empty string", got)
		}
	})

	t.Run("named context block then real prompt", func(t *testing.T) {
		in := `<context name="globalClaudeMd"># Unified MCP Agent System Prompt` +
			"\nlots of boilerplate about modes and skills\n</context>" +
			"\n\nadd pagination to the history menu"
		got := CleanText(in)
		if got != "add pagination to the history menu" {
			t.Fatalf("CleanText() = %q, want %q", got, "add pagination to the history menu")
		}
	})
}

func TestScoreSummaryRanksTitleOverBody(t *testing.T) {
	terms := parseQueryTerms("welcome screen")
	titleScore, ok := scoreSummary("fix welcome screen", "unrelated body", terms)
	if !ok {
		t.Fatal("expected title match")
	}
	bodyScore, ok := scoreSummary("", "fix welcome screen bug", terms)
	if !ok {
		t.Fatal("expected body match")
	}
	if titleScore <= bodyScore {
		t.Fatalf("title score %d should outrank body score %d", titleScore, bodyScore)
	}
	if _, ok := scoreSummary("welcome only", "no other term here", terms); ok {
		t.Fatal("AND semantics: missing 'screen' term should not match")
	}
}

func TestFirstSubstantiveUserTextSkipsResumeAndRuntimeMessages(t *testing.T) {
	messages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "This session is being continued from a previous conversation that ran out of context. Summary follows."},
		{Role: conversation.RoleUser, Content: "## MCP Context\nConnected MCP Servers"},
		{Role: conversation.RoleUser, Content: "Please continue the conversation from where we left off without asking the user any further questions."},
		{Role: conversation.RoleUser, Content: "continue from where you left off"},
		{Role: conversation.RoleUser, Content: "<swarm_runtime_guidance>injected</swarm_runtime_guidance>"},
		{Role: conversation.RoleUser, Content: `<system-reminder source="user_prompt_submit">noise</system-reminder>
listen please run everything in 1 so I can drag and drop a pdf`},
	}
	got := FirstSubstantiveUserText(messages)
	if got != "listen please run everything in 1 so I can drag and drop a pdf" {
		t.Fatalf("FirstSubstantiveUserText() = %q", got)
	}
}

func TestHistorySearchExtendedMatching(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tests := []struct {
		name        string
		params      map[string]any
		summaries   []Summary
		wantIDs     []string
		wantErrText string
		check       func(*testing.T, SearchRequest)
	}{
		{
			name:   "plain query keeps legacy insensitive AND scoring",
			params: map[string]any{"query": "welcome screen"},
			summaries: []Summary{
				{ID: "body", Body: "WELCOME screen", WorkspacePath: workspace},
				{ID: "title", Title: "Welcome Screen", WorkspacePath: workspace},
				{ID: "partial", Title: "welcome only", WorkspacePath: workspace},
			},
			wantIDs: []string{"title", "body"},
			check: func(t *testing.T, got SearchRequest) {
				t.Helper()
				if got.Limit != defaultSearchLimit {
					t.Fatalf("legacy request limit = %d, want %d", got.Limit, defaultSearchLimit)
				}
				if got.Regex != "" || got.CaseSensitive || got.Fields != nil || got.ExcludeRuntime {
					t.Fatalf("new request fields must retain zero values: %+v", got)
				}
			},
		},
		{
			name:   "regex matches syntax plain terms cannot express",
			params: map[string]any{"regex": `fix\(hooks\):\s+\w+`},
			summaries: []Summary{
				{ID: "match", Preview: "fix(hooks): register advisory", WorkspacePath: workspace},
				{ID: "miss", Preview: "fix hooks later", WorkspacePath: workspace},
			},
			wantIDs: []string{"match"},
		},
		{
			name:        "invalid regex is actionable",
			params:      map[string]any{"regex": `PR #(\d{3}`},
			wantErrText: "invalid regex",
		},
		{
			name:   "default plain matching remains case insensitive",
			params: map[string]any{"query": "Error"},
			summaries: []Summary{
				{ID: "upper", Title: "Error", WorkspacePath: workspace},
				{ID: "lower", Title: "error", WorkspacePath: workspace},
			},
			wantIDs: []string{"upper", "lower"},
		},
		{
			name:   "case sensitive distinguishes case",
			params: map[string]any{"query": "Error", "case_sensitive": true},
			summaries: []Summary{
				{ID: "upper", Title: "Error", WorkspacePath: workspace},
				{ID: "lower", Title: "error", WorkspacePath: workspace},
			},
			wantIDs: []string{"upper"},
			check: func(t *testing.T, got SearchRequest) {
				t.Helper()
				if !got.CaseSensitive {
					t.Fatal("case_sensitive was not propagated to the backend request")
				}
			},
		},
		{
			name:   "title field excludes body-only match",
			params: map[string]any{"query": "needle", "fields": []any{"title"}},
			summaries: []Summary{
				{ID: "body", Title: "other", Body: "needle", WorkspacePath: workspace},
				{ID: "title", Title: "needle", WorkspacePath: workspace},
			},
			wantIDs: []string{"title"},
		},
		{
			name: "exclude runtime drops reminder but keeps human body",
			params: map[string]any{
				"query": "needle", "fields": []any{"body"}, "exclude_runtime": true,
			},
			summaries: []Summary{
				{
					ID: "runtime",
					Body: `<system-reminder source="user_prompt_submit">
needle
</system-reminder>`,
					WorkspacePath: workspace,
				},
				{ID: "human", Body: "A real human message contains needle.", WorkspacePath: workspace},
			},
			wantIDs: []string{"human"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request SearchRequest
			called := false
			tool := NewSearchToolWithOptions(workspace, func(_ context.Context, got SearchRequest) ([]Summary, error) {
				called = true
				request = got
				return tt.summaries, nil
			})
			result, err := tool.Execute(context.Background(), tt.params)
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("Execute error = %v, want containing %q", err, tt.wantErrText)
				}
				if called {
					t.Fatal("backend called after parameter validation failed")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if tt.check != nil {
				tt.check(t, request)
			}
			rows := decodeHistoryOutput(t, result.Output)["results"].([]any)
			gotIDs := make([]string, len(rows))
			for i, raw := range rows {
				gotIDs[i] = raw.(map[string]any)["id"].(string)
			}
			if strings.Join(gotIDs, ",") != strings.Join(tt.wantIDs, ",") {
				t.Fatalf("result IDs = %v, want %v; output=%s", gotIDs, tt.wantIDs, result.Output)
			}
			if tt.name == "case sensitive distinguishes case" {
				if got := decodeHistoryOutput(t, result.Output)["query"]; got != "Error" {
					t.Fatalf("reported case-sensitive query = %v, want Error", got)
				}
			}
		})
	}
}

func TestHistorySearchSnippetsAreBoundedCappedAndUTF8Safe(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	body := "界" + strings.Repeat("a", 300) + "🚀one" + strings.Repeat("b", 500) +
		"🚀two" + strings.Repeat("c", 500) + "🚀three" + strings.Repeat("d", 300) + "界"
	tool := NewSearchToolWithOptions(workspace, func(context.Context, SearchRequest) ([]Summary, error) {
		return []Summary{{ID: "snips", Body: body, WorkspacePath: workspace}}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{
		"regex": `🚀(?:one|two|three)`, "fields": []any{"body"}, "snippet": true,
		"snippet_context": maxSnippetContext, "max_snippets": 2,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rows := decodeHistoryOutput(t, result.Output)["results"].([]any)
	if len(rows) != 1 {
		t.Fatalf("results = %#v", rows)
	}
	snippets := rows[0].(map[string]any)["snippets"].([]any)
	if len(snippets) != 2 {
		t.Fatalf("snippet count = %d, want cap 2: %#v", len(snippets), snippets)
	}
	for i, raw := range snippets {
		snippet := raw.(map[string]any)
		text := snippet["text"].(string)
		if snippet["field"] != "body" {
			t.Errorf("snippet %d field = %v, want body", i, snippet["field"])
		}
		if !strings.Contains(text, "🚀") {
			t.Errorf("snippet %d does not contain its match: %q", i, text)
		}
		if !utf8.ValidString(text) {
			t.Errorf("snippet %d split a UTF-8 rune: %q", i, text)
		}
		if got := utf8.RuneCountInString(text); got > maxSnippetChars {
			t.Errorf("snippet %d has %d runes, want <= %d", i, got, maxSnippetChars)
		}
	}
}

func TestHistorySearchOverFetchAndExhaustion(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tests := []struct {
		name          string
		summaries     func(int) []Summary
		wantIDs       []string
		wantExhausted bool
	}{
		{
			name: "post filter fills requested limit from over fetched candidates",
			summaries: func(fetchLimit int) []Summary {
				if fetchLimit != 2*candidateFetchMultiplier {
					t.Fatalf("backend limit = %d, want %d", fetchLimit, 2*candidateFetchMultiplier)
				}
				return []Summary{
					{ID: "discard-1", Title: "other", WorkspacePath: workspace},
					{ID: "discard-2", Title: "other", WorkspacePath: workspace},
					{ID: "keep-1", Title: "needle", WorkspacePath: workspace},
					{ID: "keep-2", Title: "needle again", WorkspacePath: workspace},
				}
			},
			wantIDs: []string{"keep-1", "keep-2"},
		},
		{
			name: "filled candidate pool reports possible incompleteness",
			summaries: func(fetchLimit int) []Summary {
				items := make([]Summary, fetchLimit)
				for i := range items {
					items[i] = Summary{ID: "discard", Title: "other", WorkspacePath: workspace}
				}
				return items
			},
			wantExhausted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewSearchToolWithOptions(workspace, func(_ context.Context, request SearchRequest) ([]Summary, error) {
				return tt.summaries(request.Limit), nil
			})
			result, err := tool.Execute(context.Background(), map[string]any{
				"query": "needle", "fields": []any{"title"}, "limit": 2,
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			decoded := decodeHistoryOutput(t, result.Output)
			rows := decoded["results"].([]any)
			gotIDs := make([]string, len(rows))
			for i, raw := range rows {
				gotIDs[i] = raw.(map[string]any)["id"].(string)
			}
			if strings.Join(gotIDs, ",") != strings.Join(tt.wantIDs, ",") {
				t.Fatalf("result IDs = %v, want %v; output=%s", gotIDs, tt.wantIDs, result.Output)
			}
			exhausted, _ := decoded["candidate_pool_exhausted"].(bool)
			if exhausted != tt.wantExhausted {
				t.Fatalf("candidate_pool_exhausted = %v, want %v; output=%s", exhausted, tt.wantExhausted, result.Output)
			}
			if tt.wantExhausted && decoded["incomplete"] != true {
				t.Fatalf("exhausted output must report incomplete: %s", result.Output)
			}
		})
	}
}

func TestHistorySearchSnippetOnlyPreservesBackendBodyMatch(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewSearchToolWithOptions(workspace, func(_ context.Context, request SearchRequest) ([]Summary, error) {
		if request.Limit != defaultSearchLimit {
			t.Fatalf("snippet-only request over-fetched %d candidates", request.Limit)
		}
		return []Summary{{
			ID: "indexed-body", SearchMatched: true, SearchScore: 42, WorkspacePath: workspace,
		}}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": "indexed", "snippet": true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rows := decodeHistoryOutput(t, result.Output)["results"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "indexed-body" {
		t.Fatalf("backend body match was dropped: %s", result.Output)
	}
}

func TestHistorySearchLegacyBackendPostFilterIsLocallyBounded(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	items := make([]Summary, candidateFetchCap+25)
	for i := range items {
		items[i] = Summary{ID: "discard", Title: "other", WorkspacePath: workspace}
	}
	tool := NewSearchTool(workspace, func(context.Context, string, string) ([]Summary, error) {
		return items, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "needle", "fields": []any{"title"}, "limit": maxSearchLimit,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	decoded := decodeHistoryOutput(t, result.Output)
	if got := decoded["candidates_examined"]; got != float64(candidateFetchCap) {
		t.Fatalf("candidates_examined = %v, want cap %d; output=%s", got, candidateFetchCap, result.Output)
	}
	if decoded["candidate_pool_exhausted"] != true || decoded["incomplete"] != true {
		t.Fatalf("legacy capped pool did not report exhaustion: %s", result.Output)
	}
}

type fakeSegmentSearcher struct {
	hits       []searchindex.SegmentHit
	stats      []searchindex.TermStat
	count      int
	gotQuery   searchindex.SegmentQuery
	gotFilter  searchindex.SegmentFilter
	gotOptions searchindex.StatsOptions
}

func (f *fakeSegmentSearcher) SearchSegments(_ context.Context, query searchindex.SegmentQuery) ([]searchindex.SegmentHit, error) {
	f.gotQuery = query
	out := make([]searchindex.SegmentHit, 0, len(f.hits))
	for _, hit := range f.hits {
		if query.Filter.WorkspacePath != "" && hit.WorkspacePath != query.Filter.WorkspacePath {
			continue
		}
		if query.Filter.ToolName != "" && hit.ToolName != query.Filter.ToolName {
			continue
		}
		if query.Filter.Kind != "" && hit.Kind != query.Filter.Kind {
			continue
		}
		if query.Filter.Outcome == searchindex.OutcomeFailed && !hit.Failed {
			continue
		}
		if query.Filter.Outcome == searchindex.OutcomeSucceeded && hit.Failed {
			continue
		}
		out = append(out, hit)
	}
	return out, nil
}

func (f *fakeSegmentSearcher) TermStats(_ context.Context, filter searchindex.SegmentFilter, opts searchindex.StatsOptions) ([]searchindex.TermStat, error) {
	f.gotFilter, f.gotOptions = filter, opts
	return append([]searchindex.TermStat(nil), f.stats...), nil
}

func (f *fakeSegmentSearcher) SegmentCount(_ context.Context, filter searchindex.SegmentFilter) (int, error) {
	f.gotFilter = filter
	return f.count, nil
}

func TestHistorySearchSegmentFilters(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	hits := []searchindex.SegmentHit{
		{Segment: searchindex.Segment{ConversationID: "message", WorkspacePath: workspace, Ordinal: 1, Kind: searchindex.SegmentMessage, Text: "needle prose"}},
		{Segment: searchindex.Segment{ConversationID: "bash-failed", WorkspacePath: workspace, Ordinal: 2, Kind: searchindex.SegmentToolResult, ToolName: "Bash", Failed: true, Text: "needle failed"}},
		{Segment: searchindex.Segment{ConversationID: "read-ok", WorkspacePath: workspace, Ordinal: 3, Kind: searchindex.SegmentToolResult, ToolName: "Read", Text: "needle succeeded"}},
	}
	summaries := []Summary{
		{ID: "message", Title: "Message", WorkspacePath: workspace},
		{ID: "bash-failed", Title: "Bash failed", WorkspacePath: workspace},
		{ID: "read-ok", Title: "Read succeeded", WorkspacePath: workspace},
	}
	tests := []struct {
		name    string
		params  map[string]any
		wantIDs string
		check   func(*testing.T, searchindex.SegmentFilter)
		wantErr string
	}{
		{
			name: "tool_name narrows correctly", params: map[string]any{"query": "needle", "tool_name": "Bash"}, wantIDs: "bash-failed",
			check: func(t *testing.T, f searchindex.SegmentFilter) {
				if f.ToolName != "Bash" {
					t.Fatalf("ToolName = %q", f.ToolName)
				}
			},
		},
		{
			name: "tool_outcome narrows correctly", params: map[string]any{"query": "needle", "tool_outcome": "failed"}, wantIDs: "bash-failed",
			check: func(t *testing.T, f searchindex.SegmentFilter) {
				if f.Outcome != searchindex.OutcomeFailed {
					t.Fatalf("Outcome = %q", f.Outcome)
				}
			},
		},
		{
			name: "segment_kind narrows correctly", params: map[string]any{"query": "needle", "segment_kind": "message"}, wantIDs: "message",
			check: func(t *testing.T, f searchindex.SegmentFilter) {
				if f.Kind != searchindex.SegmentMessage {
					t.Fatalf("Kind = %q", f.Kind)
				}
			},
		},
		{name: "invalid outcome is clear", params: map[string]any{"tool_outcome": "broken"}, wantErr: "tool_outcome must be one of any, failed, succeeded"},
		{name: "invalid kind is clear", params: map[string]any{"segment_kind": "attachment"}, wantErr: "segment_kind must be one of message, tool_call, tool_result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := &fakeSegmentSearcher{hits: hits}
			tool := NewSearchToolWithOptions(workspace, func(context.Context, SearchRequest) ([]Summary, error) {
				return summaries, nil
			}, index)
			result, err := tool.Execute(context.Background(), tt.params)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Execute error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			tt.check(t, index.gotQuery.Filter)
			rows := decodeHistoryOutput(t, result.Output)["results"].([]any)
			ids := make([]string, len(rows))
			for i, raw := range rows {
				row := raw.(map[string]any)
				ids[i] = row["id"].(string)
				if _, ok := row["matched_segment"].(map[string]any); !ok {
					t.Fatalf("missing matched_segment: %s", result.Output)
				}
			}
			if got := strings.Join(ids, ","); got != tt.wantIDs {
				t.Fatalf("IDs = %q, want %q; output=%s", got, tt.wantIDs, result.Output)
			}
		})
	}
}

func TestHistorySearchSegmentStats(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tests := []struct {
		name    string
		params  map[string]any
		index   *fakeSegmentSearcher
		check   func(*testing.T, map[string]any, *fakeSegmentSearcher)
		wantErr string
	}{
		{
			name:   "sorted deterministic and honours top_terms",
			params: map[string]any{"stats": true, "ngram": 2, "top_terms": 2, "tool_outcome": "failed"},
			index: &fakeSegmentSearcher{count: 12, stats: []searchindex.TermStat{
				{Term: "zeta phrase", Occurrences: 7, Segments: 4, Conversations: 3},
				{Term: "alpha phrase", Occurrences: 7, Segments: 5, Conversations: 2},
				{Term: "lower phrase", Occurrences: 2, Segments: 2, Conversations: 1},
			}},
			check: func(t *testing.T, out map[string]any, index *fakeSegmentSearcher) {
				if index.gotOptions.NGram != 2 || index.gotOptions.Top != 2 {
					t.Fatalf("StatsOptions = %+v", index.gotOptions)
				}
				rows := out["terms"].([]any)
				if len(rows) != 2 || rows[0].(map[string]any)["term"] != "alpha phrase" || rows[1].(map[string]any)["term"] != "zeta phrase" {
					t.Fatalf("terms not deterministically sorted/capped: %#v", rows)
				}
				row := rows[0].(map[string]any)
				for _, field := range []string{"term", "occurrences", "segments", "conversations"} {
					if _, ok := row[field]; !ok {
						t.Fatalf("row missing %q: %#v", field, row)
					}
				}
			},
		},
		{
			name:   "above cap is clamped and disclosed",
			params: map[string]any{"stats": true, "top_terms": searchindex.MaxStatsTop + 37},
			index:  &fakeSegmentSearcher{count: 1},
			check: func(t *testing.T, out map[string]any, index *fakeSegmentSearcher) {
				if index.gotOptions.Top != searchindex.MaxStatsTop || out["top_terms_capped"] != true {
					t.Fatalf("cap not enforced/disclosed: opts=%+v out=%#v", index.gotOptions, out)
				}
				if out["requested_top_terms"] != float64(searchindex.MaxStatsTop+37) {
					t.Fatalf("requested cap missing: %#v", out)
				}
			},
		},
		{
			name:   "empty table reports segments scanned",
			params: map[string]any{"stats": true, "segment_kind": "tool_result"},
			index:  &fakeSegmentSearcher{count: 9},
			check: func(t *testing.T, out map[string]any, _ *fakeSegmentSearcher) {
				if out["segments_scanned"] != float64(9) || len(out["terms"].([]any)) != 0 {
					t.Fatalf("empty stats lack corpus provenance: %#v", out)
				}
			},
		},
		{name: "ngram below range errors", params: map[string]any{"stats": true, "ngram": 0}, index: &fakeSegmentSearcher{}, wantErr: "ngram must be an integer between 1 and 5"},
		{name: "ngram above range errors", params: map[string]any{"stats": true, "ngram": 6}, index: &fakeSegmentSearcher{}, wantErr: "ngram must be an integer between 1 and 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewSearchToolWithOptions(workspace, func(context.Context, SearchRequest) ([]Summary, error) {
				return nil, fmt.Errorf("conversation backend called during stats")
			}, tt.index)
			result, err := tool.Execute(context.Background(), tt.params)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Execute error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			tt.check(t, decodeHistoryOutput(t, result.Output), tt.index)
		})
	}
}

func TestHistorySearchSegmentCompatibilityAndUnavailable(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	var got SearchRequest
	search := func(_ context.Context, request SearchRequest) ([]Summary, error) {
		got = request
		return []Summary{{ID: "legacy", Title: "Needle", WorkspacePath: workspace}}, nil
	}
	legacy := NewSearchToolWithOptions(workspace, search)
	configured := NewSearchToolWithOptions(workspace, search, &fakeSegmentSearcher{})
	params := map[string]any{"query": "needle"}
	before, err := legacy.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("legacy Execute: %v", err)
	}
	after, err := configured.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("configured Execute: %v", err)
	}
	if before.Output != after.Output {
		t.Fatalf("zero-valued segment fields changed bytes:\nbefore=%s\nafter=%s", before.Output, after.Output)
	}
	if got.ToolName != "" || got.ToolOutcome != "" || got.SegmentKind != "" || got.Stats || got.NGram != 0 || got.TopTerms != 0 {
		t.Fatalf("legacy request did not preserve new field zero values: %+v", got)
	}
	_, err = legacy.Execute(context.Background(), map[string]any{"stats": true})
	if err == nil || !strings.Contains(err.Error(), "segment index unavailable") {
		t.Fatalf("unavailable stats error = %v", err)
	}
}
