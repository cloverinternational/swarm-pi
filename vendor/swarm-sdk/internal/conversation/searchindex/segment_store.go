package searchindex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var ErrSegmentsUnsupported = errors.New("conversation segments unsupported")

const (
	maxSegmentSearchLimit = MaxStatsTop
	maxStatsScanSegments  = 100000
	segmentSelectColumns  = "s.conversation_id,s.workspace_path,s.ordinal,s.message_id,s.role,s.kind,s.tool_name,s.call_id,s.failed,s.runtime,s.timestamp,s.text"
)

func (s *SQLite) ReplaceSegments(ctx context.Context, conversationID string, segments []Segment) error {
	if strings.TrimSpace(conversationID) == "" {
		return errorsNew("segment conversation id is required")
	}
	if len(segments) > MaxSegmentsPerConversation {
		return fmt.Errorf("searchindex: conversation %q has %d segments; maximum is %d",
			conversationID, len(segments), MaxSegmentsPerConversation)
	}
	for index, segment := range segments {
		if segment.ConversationID != "" && segment.ConversationID != conversationID {
			return fmt.Errorf("searchindex: segment %d belongs to conversation %q, not %q",
				index, segment.ConversationID, conversationID)
		}
		if len(segment.Text) > MaxSegmentTextBytes {
			return fmt.Errorf("searchindex: segment %d text is %d bytes; maximum is %d",
				index, len(segment.Text), MaxSegmentTextBytes)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("searchindex: begin segment replacement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_segments WHERE conversation_id=?`, conversationID); err != nil {
		return fmt.Errorf("searchindex: delete replaced segments: %w", err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO history_segments(
			conversation_id,ordinal,workspace_path,message_id,role,kind,tool_name,
			call_id,failed,runtime,timestamp,text
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("searchindex: prepare segment insert: %w", err)
	}
	defer func() { _ = statement.Close() }()
	for _, segment := range segments {
		if !utf8.ValidString(segment.Text) {
			return errorsNew("segment text must be valid UTF-8")
		}
		if _, err := statement.ExecContext(ctx,
			conversationID, segment.Ordinal, segment.WorkspacePath, segment.MessageID,
			segment.Role, segment.Kind, segment.ToolName, segment.CallID,
			boolInt(segment.Failed), boolInt(segment.Runtime), segmentTimestamp(segment.Timestamp),
			segment.Text,
		); err != nil {
			return fmt.Errorf("searchindex: insert segment %d: %w", segment.Ordinal, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("searchindex: commit segment replacement: %w", err)
	}
	return nil
}

func (s *SQLite) DeleteSegments(ctx context.Context, conversationID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM history_segments WHERE conversation_id=?`, conversationID); err != nil {
		return fmt.Errorf("searchindex: delete segments: %w", err)
	}
	return nil
}

func (s *SQLite) SegmentCount(ctx context.Context, filter SegmentFilter) (int, error) {
	where, args := segmentFilterSQL(filter, "s")
	query := `SELECT COUNT(*) FROM history_segments s`
	if len(where) != 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("searchindex: count segments: %w", err)
	}
	return count, nil
}

func (s *SQLite) SearchSegments(ctx context.Context, query SegmentQuery) ([]SegmentHit, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultSegmentLimit
	}
	if limit > maxSegmentSearchLimit {
		limit = maxSegmentSearchLimit
	}
	where, args := segmentFilterSQL(query.Filter, "s")
	from := `history_segments s`
	score := `0.0`
	if strings.TrimSpace(query.Text) != "" {
		from = `history_segments_fts f JOIN history_segments s ON s.rowid=f.rowid`
		where = append([]string{`history_segments_fts MATCH ?`}, where...)
		// query.Text is caller-authored plain text, not FTS5 query syntax --
		// literalFTSQuery quotes each token so a hyphenated identifier
		// (e.g. "claude-code") can never be parsed as FTS5's own query
		// grammar (bareword/column-filter/NOT-operator syntax). Passing the
		// raw text here (unlike Search() in sqlite.go, which already quotes
		// via literalFTSQuery) let a plain hyphenated query reach SQLite as
		// unescaped query syntax and fail with "no such column: <word>"
		// instead of matching or returning zero results (issue #279).
		args = append([]any{literalFTSQuery(query.Text)}, args...)
		score = `-bm25(history_segments_fts)`
	}
	statement := `SELECT ` + segmentSelectColumns + `,` + score + ` FROM ` + from
	if len(where) != 0 {
		statement += ` WHERE ` + strings.Join(where, ` AND `)
	}
	statement += ` ORDER BY ` + score + ` DESC,s.timestamp DESC,s.conversation_id ASC,s.ordinal ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("searchindex: search segments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := make([]SegmentHit, 0, limit)
	for rows.Next() {
		var hit SegmentHit
		var failed, runtime int
		var timestamp int64
		if err := rows.Scan(
			&hit.ConversationID, &hit.WorkspacePath, &hit.Ordinal, &hit.MessageID,
			&hit.Role, &hit.Kind, &hit.ToolName, &hit.CallID, &failed, &runtime,
			&timestamp, &hit.Text, &hit.Score,
		); err != nil {
			return nil, fmt.Errorf("searchindex: scan segment hit: %w", err)
		}
		hit.Failed = failed != 0
		hit.Runtime = runtime != 0
		hit.Timestamp = unixNanoTime(timestamp)
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searchindex: iterate segment hits: %w", err)
	}
	return hits, nil
}

type termAccumulator struct {
	occurrences   int
	segments      int
	conversations map[string]struct{}
}

func (s *SQLite) TermStats(ctx context.Context, filter SegmentFilter, options StatsOptions) ([]TermStat, error) {
	ngram := options.NGram
	if ngram <= 1 {
		ngram = 1
	}
	if ngram > MaxStatsNGram {
		ngram = MaxStatsNGram
	}
	top := options.Top
	if top <= 0 {
		top = DefaultStatsTop
	}
	if top > MaxStatsTop {
		top = MaxStatsTop
	}
	minCount := options.MinCount
	if minCount <= 0 {
		minCount = 1
	}

	// fts5vocab('row') is intentionally not used here. It can quickly provide
	// corpus-wide occurrence and segment counts, but cannot honor SegmentFilter
	// or report distinct conversations. A bounded row scan keeps all requested
	// metrics exact and uses the identical tokenizer for unigrams and n-grams.
	where, args := segmentFilterSQL(filter, "s")
	statement := `SELECT s.conversation_id,s.text FROM history_segments s`
	if len(where) != 0 {
		statement += ` WHERE ` + strings.Join(where, ` AND `)
	}
	statement += ` ORDER BY s.conversation_id,s.ordinal LIMIT ?`
	args = append(args, maxStatsScanSegments+1)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("searchindex: query segments for statistics: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]*termAccumulator)
	scanned := 0
	truncated := false
	for rows.Next() {
		if scanned == maxStatsScanSegments {
			truncated = true
			break
		}
		var conversationID, text string
		if err := rows.Scan(&conversationID, &text); err != nil {
			return nil, fmt.Errorf("searchindex: scan segment statistics: %w", err)
		}
		scanned++
		tokens := unicode61Tokens(text)
		seen := make(map[string]struct{})
		for index := 0; index+ngram <= len(tokens); index++ {
			parts := tokens[index : index+ngram]
			if !statsTermAllowed(parts, options) {
				continue
			}
			term := strings.Join(parts, " ")
			accumulator := counts[term]
			if accumulator == nil {
				accumulator = &termAccumulator{conversations: make(map[string]struct{})}
				counts[term] = accumulator
			}
			accumulator.occurrences++
			seen[term] = struct{}{}
			accumulator.conversations[conversationID] = struct{}{}
		}
		for term := range seen {
			counts[term].segments++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searchindex: iterate segment statistics: %w", err)
	}

	stats := make([]TermStat, 0, len(counts))
	for term, accumulator := range counts {
		if accumulator.occurrences < minCount {
			continue
		}
		stats = append(stats, TermStat{
			Term:          term,
			Occurrences:   accumulator.occurrences,
			Segments:      accumulator.segments,
			Conversations: len(accumulator.conversations),
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Occurrences != stats[j].Occurrences {
			return stats[i].Occurrences > stats[j].Occurrences
		}
		return stats[i].Term < stats[j].Term
	})
	if len(stats) > top {
		stats = stats[:top]
	}
	if truncated {
		return stats, fmt.Errorf(
			"searchindex: statistics stopped after %d segments (scan cap reached)",
			maxStatsScanSegments,
		)
	}
	return stats, nil
}

func segmentFilterSQL(filter SegmentFilter, alias string) ([]string, []any) {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	var where []string
	var args []any
	add := func(name string, value any) {
		where = append(where, column(name)+`=?`)
		args = append(args, value)
	}
	if filter.WorkspacePath != "" {
		add("workspace_path", filter.WorkspacePath)
	}
	if filter.ConversationID != "" {
		add("conversation_id", filter.ConversationID)
	}
	if filter.ToolName != "" {
		add("tool_name", filter.ToolName)
	}
	if filter.Kind != "" {
		add("kind", filter.Kind)
	}
	if filter.Role != "" {
		add("role", filter.Role)
	}
	// Outcome has meaning only for tool results. Keeping both implications here
	// ensures search, count, and statistics can never disagree.
	switch filter.Outcome {
	case OutcomeFailed:
		add("kind", SegmentToolResult)
		add("failed", 1)
	case OutcomeSucceeded:
		add("kind", SegmentToolResult)
		add("failed", 0)
	}
	if filter.ExcludeRuntime {
		add("runtime", 0)
	}
	if !filter.Since.IsZero() {
		where = append(where, column("timestamp")+`>=?`)
		args = append(args, filter.Since.UnixNano())
	}
	if !filter.Until.IsZero() {
		where = append(where, column("timestamp")+`<=?`)
		args = append(args, filter.Until.UnixNano())
	}
	return where, args
}

func unicode61Tokens(text string) []string {
	// unicode61 defaults to Unicode letter/number/private-use token classes,
	// lower-casing, and removal of Latin diacritics. NFD plus dropping combining
	// marks mirrors that behavior closely for the Go statistics path.
	text = strings.ToLower(norm.NFD.String(text))
	var tokens []string
	var token strings.Builder
	flush := func() {
		if token.Len() != 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	for _, r := range text {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r) {
			token.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func statsTermAllowed(parts []string, options StatsOptions) bool {
	for _, part := range parts {
		if len([]rune(part)) < options.MinTermLength {
			return false
		}
		if options.ExcludeStopwords {
			if _, found := englishStopwords[part]; found {
				return false
			}
		}
	}
	return true
}

var englishStopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
	"but": {}, "by": {}, "for": {}, "from": {}, "had": {}, "has": {}, "have": {},
	"he": {}, "her": {}, "his": {}, "i": {}, "in": {}, "is": {}, "it": {},
	"its": {}, "me": {}, "my": {}, "not": {}, "of": {}, "on": {}, "or": {},
	"our": {}, "she": {}, "that": {}, "the": {}, "their": {}, "them": {},
	"they": {}, "this": {}, "to": {}, "was": {}, "we": {}, "were": {},
	"will": {}, "with": {}, "you": {}, "your": {},
}

func unixNanoTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value)
}

func segmentTimestamp(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}
