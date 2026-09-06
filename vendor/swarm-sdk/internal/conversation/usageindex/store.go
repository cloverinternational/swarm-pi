package usageindex

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Store is the backend-agnostic index. Turso and SQLite run identical SQL here
// because the usage index needs plain tables only, so there is exactly one
// implementation of the schema and the queries.
type Store struct {
	db      *sql.DB
	backend Backend
}

func newStore(ctx context.Context, db *sql.DB, backend Backend) (*Store, error) {
	store := &Store{db: db, backend: backend}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Backend reports which engine actually opened, for diagnostics.
func (s *Store) Backend() Backend { return s.backend }

func (s *Store) Close() error { return s.db.Close() }

// schemaStatements are executed one at a time: the Turso driver cannot be
// relied upon to accept multi-statement Exec calls.
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS usage_files (
		path TEXT PRIMARY KEY,
		size INTEGER NOT NULL,
		mtime_ns INTEGER NOT NULL,
		conversation_id TEXT NOT NULL,
		parent_id TEXT NOT NULL,
		lineage_root TEXT NOT NULL,
		active INTEGER NOT NULL,
		unreadable INTEGER NOT NULL,
		response_count INTEGER NOT NULL,
		total_tokens INTEGER NOT NULL,
		scanned_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS usage_files_conversation ON usage_files(conversation_id)`,
	`CREATE TABLE IF NOT EXISTS usage_responses (
		path TEXT NOT NULL,
		seq INTEGER NOT NULL,
		dedup_key TEXT NOT NULL,
		lineage_scoped INTEGER NOT NULL,
		conversation_id TEXT NOT NULL,
		ts INTEGER NOT NULL,
		day INTEGER NOT NULL,
		fresh_input INTEGER NOT NULL,
		cache_write INTEGER NOT NULL,
		cache_write_5m INTEGER NOT NULL,
		cache_write_1h INTEGER NOT NULL,
		cache_read INTEGER NOT NULL,
		output INTEGER NOT NULL,
		other INTEGER NOT NULL,
		total INTEGER NOT NULL,
		schema_kind INTEGER NOT NULL,
		PRIMARY KEY (path, seq)
	)`,
	`CREATE INDEX IF NOT EXISTS usage_responses_path ON usage_responses(path)`,
	// Recompute walks responses grouped by conversation in turn order; this
	// index lets the engine stream them instead of sorting 200k rows.
	`CREATE INDEX IF NOT EXISTS usage_responses_conversation
		ON usage_responses(conversation_id, ts, seq)`,
	`CREATE TABLE IF NOT EXISTS usage_rollup_daily (
		day INTEGER PRIMARY KEY,
		fresh_input INTEGER NOT NULL,
		cache_write INTEGER NOT NULL,
		cache_read INTEGER NOT NULL,
		output INTEGER NOT NULL,
		responses INTEGER NOT NULL,
		cache_capable_responses INTEGER NOT NULL DEFAULT 0,
		cache_capable_fresh_input INTEGER NOT NULL DEFAULT 0,
		cache_capable_cache_write INTEGER NOT NULL DEFAULT 0,
		cache_capable_cache_read INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS usage_findings (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		ts INTEGER NOT NULL,
		tokens INTEGER NOT NULL,
		ratio REAL NOT NULL,
		gap_seconds INTEGER NOT NULL,
		cause TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS usage_totals (
		id INTEGER PRIMARY KEY,
		fresh_input INTEGER NOT NULL,
		cache_write INTEGER NOT NULL,
		cache_read INTEGER NOT NULL,
		output INTEGER NOT NULL,
		other INTEGER NOT NULL,
		total INTEGER NOT NULL,
		conversations INTEGER NOT NULL,
		responses INTEGER NOT NULL,
		duplicate_files INTEGER NOT NULL,
		duplicate_responses INTEGER NOT NULL,
		unreadable_files INTEGER NOT NULL,
		computed_at INTEGER NOT NULL,
		cache_capable_responses INTEGER NOT NULL DEFAULT 0,
		cache_capable_fresh_input INTEGER NOT NULL DEFAULT 0,
		cache_capable_cache_write INTEGER NOT NULL DEFAULT 0,
		cache_capable_cache_read INTEGER NOT NULL DEFAULT 0
	)`,
}

var dropStatements = []string{
	`DROP TABLE IF EXISTS usage_totals`,
	`DROP TABLE IF EXISTS usage_findings`,
	`DROP TABLE IF EXISTS usage_rollup_daily`,
	`DROP TABLE IF EXISTS usage_responses`,
	`DROP TABLE IF EXISTS usage_files`,
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func execAll(ctx context.Context, exec execer, statements []string, operation string) error {
	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return unavailable(operation, err)
		}
	}
	return nil
}

func (s *Store) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS usage_index_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`); err != nil {
		return unavailable("initialize usage index metadata", err)
	}
	var version int
	err := s.db.QueryRowContext(ctx,
		`SELECT CAST(value AS INTEGER) FROM usage_index_meta WHERE key='schema_version'`,
	).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return unavailable("read usage index schema version", err)
	}
	if errors.Is(err, sql.ErrNoRows) || version != SchemaVersion {
		return s.rebuildSchema(ctx)
	}
	return execAll(ctx, s.db, schemaStatements, "initialize usage index schema")
}

// rebuildSchema drops everything and starts over. The index is derived from
// conversation JSON, so rebuilding is always cheaper and safer than migrating.
func (s *Store) rebuildSchema(ctx context.Context) error {
	if err := execAll(ctx, s.db, dropStatements, "reset usage index schema"); err != nil {
		return err
	}
	if err := execAll(ctx, s.db, schemaStatements, "initialize usage index schema"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_index_meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		strconv.Itoa(SchemaVersion)); err != nil {
		return unavailable("store usage index schema version", err)
	}
	return nil
}

// FileStates returns the identity of every indexed file so a stat-only walk can
// decide what changed without opening anything.
func (s *Store) FileStates(ctx context.Context) (map[string]FileState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path,size,mtime_ns FROM usage_files`)
	if err != nil {
		return nil, unavailable("read indexed file states", err)
	}
	defer rows.Close()
	states := make(map[string]FileState)
	for rows.Next() {
		var state FileState
		if err := rows.Scan(&state.Path, &state.Size, &state.ModTimeNS); err != nil {
			return nil, unavailable("scan indexed file state", err)
		}
		states[state.Path] = state
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable("read indexed file states", err)
	}
	return states, nil
}

// ReplaceFile atomically swaps everything the index knows about one file.
func (s *Store) ReplaceFile(ctx context.Context, record FileRecord) error {
	return s.ReplaceFiles(ctx, []FileRecord{record})
}

// ReplaceFiles writes a batch of parsed files in a single transaction. The
// first build of a large store writes tens of thousands of files, and one
// transaction (and one fsync) per file dominated that cost.
func (s *Store) ReplaceFiles(ctx context.Context, records []FileRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable("begin usage index write", err)
	}
	defer func() { _ = tx.Rollback() }()

	clear, err := tx.PrepareContext(ctx, `DELETE FROM usage_responses WHERE path=?`)
	if err != nil {
		return unavailable("prepare response cleanup", err)
	}
	defer clear.Close()
	insert, err := tx.PrepareContext(ctx, `
		INSERT INTO usage_responses
			(path,seq,dedup_key,lineage_scoped,conversation_id,ts,day,
			 fresh_input,cache_write,cache_write_5m,cache_write_1h,cache_read,
			 output,other,total,schema_kind)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return unavailable("prepare response insert", err)
	}
	defer insert.Close()
	upsert, err := tx.PrepareContext(ctx, `
		INSERT INTO usage_files
			(path,size,mtime_ns,conversation_id,parent_id,lineage_root,active,unreadable,
			 response_count,total_tokens,scanned_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
			size=excluded.size, mtime_ns=excluded.mtime_ns,
			conversation_id=excluded.conversation_id, parent_id=excluded.parent_id,
			unreadable=excluded.unreadable,
			response_count=excluded.response_count, total_tokens=excluded.total_tokens,
			scanned_at=excluded.scanned_at`)
	if err != nil {
		return unavailable("prepare file upsert", err)
	}
	defer upsert.Close()

	now := time.Now().Unix()
	for _, record := range records {
		if _, err := clear.ExecContext(ctx, record.Path); err != nil {
			return unavailable("clear stale responses", err)
		}
		for _, response := range record.Responses {
			ts := int64(0)
			if !response.Timestamp.IsZero() {
				ts = response.Timestamp.Unix()
			}
			if _, err := insert.ExecContext(ctx,
				record.Path, response.Seq, response.DedupKey, boolInt(response.LineageScoped),
				response.ConversationID, ts, dayOf(ts),
				response.FreshInput, response.CacheWrite, response.CacheWrite5m, response.CacheWrite1h,
				response.CacheRead, response.Output, response.Other, response.Total, int(response.Schema),
			); err != nil {
				return unavailable("insert response usage", err)
			}
		}
		if _, err := upsert.ExecContext(ctx,
			record.Path, record.Size, record.ModTimeNS, record.ConversationID, record.ParentID,
			record.ConversationID, 1, boolInt(record.Unreadable),
			len(record.Responses), record.TotalTokens, now,
		); err != nil {
			return unavailable("record indexed file", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return unavailable("commit usage index write", err)
	}
	return nil
}

// RemoveFiles drops files that disappeared from disk, along with their rows.
func (s *Store) RemoveFiles(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	const chunk = 200
	for start := 0; start < len(paths); start += chunk {
		end := min(start+chunk, len(paths))
		batch := paths[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, path := range batch {
			args = append(args, path)
		}
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM usage_responses WHERE path IN (`+placeholders+`)`, args...); err != nil {
			return unavailable("delete stale responses", err)
		}
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM usage_files WHERE path IN (`+placeholders+`)`, args...); err != nil {
			return unavailable("delete stale files", err)
		}
	}
	return nil
}

type fileRow struct {
	Path           string
	ConversationID string
	ParentID       string
	LineageRoot    string
	Active         bool
	Unreadable     bool
	ResponseCount  int64
	TotalTokens    int64
}

func (s *Store) loadFileRows(ctx context.Context) ([]fileRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path,conversation_id,parent_id,lineage_root,active,unreadable,response_count,total_tokens
		FROM usage_files ORDER BY path`)
	if err != nil {
		return nil, unavailable("read indexed files", err)
	}
	defer rows.Close()
	var records []fileRow
	for rows.Next() {
		var record fileRow
		var active int
		var unreadable int
		if err := rows.Scan(&record.Path, &record.ConversationID, &record.ParentID,
			&record.LineageRoot, &active, &unreadable, &record.ResponseCount, &record.TotalTokens); err != nil {
			return nil, unavailable("scan indexed file", err)
		}
		record.Active = active != 0
		record.Unreadable = unreadable != 0
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable("read indexed files", err)
	}
	return records, nil
}

// applyFileResolution writes back which duplicate copy of a conversation wins
// and which lineage root each file belongs to. Only genuinely changed rows are
// written so a steady-state sync stays close to zero work.
func (s *Store) applyFileResolution(ctx context.Context, updates []fileRow) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable("begin file resolution", err)
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.PrepareContext(ctx,
		`UPDATE usage_files SET active=?, lineage_root=? WHERE path=?`)
	if err != nil {
		return unavailable("prepare file resolution", err)
	}
	defer statement.Close()
	for _, update := range updates {
		if _, err := statement.ExecContext(ctx, boolInt(update.Active), update.LineageRoot, update.Path); err != nil {
			return unavailable("apply file resolution", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return unavailable("commit file resolution", err)
	}
	return nil
}

// aggregateRow is one indexed response joined to its file's resolution state.
type aggregateRow struct {
	ConversationID string
	LineageRoot    string
	DedupKey       string
	LineageScoped  bool
	Seq            int
	TS             int64
	Day            int64
	FreshInput     int64
	CacheWrite     int64
	CacheWrite5m   int64
	CacheWrite1h   int64
	CacheRead      int64
	Output         int64
	Other          int64
	Total          int64
	Schema         CacheSchema
}

// eachActiveResponse streams the winning copy of every response in stable
// per-conversation order, which is what both dedup and detection need.
func (s *Store) eachActiveResponse(ctx context.Context, visit func(aggregateRow) error) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.conversation_id, f.lineage_root, r.dedup_key, r.lineage_scoped, r.seq, r.ts, r.day,
		       r.fresh_input, r.cache_write, r.cache_write_5m, r.cache_write_1h, r.cache_read,
		       r.output, r.other, r.total, r.schema_kind
		FROM usage_responses r
		JOIN usage_files f ON f.path = r.path
		WHERE f.active = 1
		ORDER BY r.conversation_id, r.ts, r.seq`)
	if err != nil {
		return unavailable("read indexed responses", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row aggregateRow
		var lineageScoped int
		if err := rows.Scan(&row.ConversationID, &row.LineageRoot, &row.DedupKey, &lineageScoped,
			&row.Seq, &row.TS, &row.Day, &row.FreshInput, &row.CacheWrite, &row.CacheWrite5m,
			&row.CacheWrite1h, &row.CacheRead, &row.Output, &row.Other, &row.Total, &row.Schema); err != nil {
			return unavailable("scan indexed response", err)
		}
		row.LineageScoped = lineageScoped != 0
		if err := visit(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return unavailable("read indexed responses", err)
	}
	return nil
}

func (s *Store) writeAggregates(ctx context.Context, summary Summary, daily []DayBucket, findings []Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable("begin aggregate write", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range []string{
		`DELETE FROM usage_rollup_daily`,
		`DELETE FROM usage_findings`,
		`DELETE FROM usage_totals`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return unavailable("clear stale aggregates", err)
		}
	}

	dayInsert, err := tx.PrepareContext(ctx, `
		INSERT INTO usage_rollup_daily(day,fresh_input,cache_write,cache_read,output,responses,cache_capable_responses,cache_capable_fresh_input,cache_capable_cache_write,cache_capable_cache_read)
		VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return unavailable("prepare daily rollup insert", err)
	}
	defer dayInsert.Close()
	for _, bucket := range daily {
		if _, err := dayInsert.ExecContext(ctx, bucket.Day.Unix()/secondsPerDay,
			bucket.FreshInput, bucket.CacheWrite, bucket.CacheRead, bucket.Output, bucket.Responses,
			bucket.CacheCapableResponses, bucket.CacheCapableFreshInput,
			bucket.CacheCapableCacheWrite, bucket.CacheCapableCacheRead); err != nil {
			return unavailable("insert daily rollup", err)
		}
	}

	findingInsert, err := tx.PrepareContext(ctx, `
		INSERT INTO usage_findings(kind,conversation_id,ts,tokens,ratio,gap_seconds,cause)
		VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return unavailable("prepare finding insert", err)
	}
	defer findingInsert.Close()
	for _, finding := range findings {
		ts := int64(0)
		if !finding.Timestamp.IsZero() {
			ts = finding.Timestamp.Unix()
		}
		if _, err := findingInsert.ExecContext(ctx, string(finding.Kind), finding.ConversationID,
			ts, finding.Tokens, finding.Ratio, finding.GapSeconds, string(finding.Cause)); err != nil {
			return unavailable("insert finding", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO usage_totals
			(id,fresh_input,cache_write,cache_read,output,other,total,conversations,responses,
			 duplicate_files,duplicate_responses,unreadable_files,computed_at,
			 cache_capable_responses,cache_capable_fresh_input,cache_capable_cache_write,cache_capable_cache_read)
		VALUES(1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		summary.FreshInput, summary.CacheWrite, summary.CacheRead, summary.Output, summary.Other,
		summary.TotalTokens, summary.ConversationCount, summary.ResponseCount,
		summary.DuplicateFiles, summary.DuplicateResponses, summary.UnreadableFiles,
		time.Now().Unix(),
		summary.CacheCapableResponses, summary.CacheCapableFreshInput,
		summary.CacheCapableCacheWrite, summary.CacheCapableCacheRead); err != nil {
		return unavailable("store usage totals", err)
	}
	if err := tx.Commit(); err != nil {
		return unavailable("commit aggregates", err)
	}
	return nil
}

// Summary returns the stored roll-up. It is a single-row read: the expensive
// work happened during sync, and only when something on disk changed.
func (s *Store) Summary(ctx context.Context) (Summary, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT fresh_input,cache_write,cache_read,output,other,total,conversations,responses,
		       duplicate_files,duplicate_responses,unreadable_files,
		       cache_capable_responses,cache_capable_fresh_input,cache_capable_cache_write,cache_capable_cache_read
		FROM usage_totals WHERE id=1`)
	var summary Summary
	err := row.Scan(&summary.FreshInput, &summary.CacheWrite, &summary.CacheRead, &summary.Output,
		&summary.Other, &summary.TotalTokens, &summary.ConversationCount, &summary.ResponseCount,
		&summary.DuplicateFiles, &summary.DuplicateResponses, &summary.UnreadableFiles,
		&summary.CacheCapableResponses, &summary.CacheCapableFreshInput,
		&summary.CacheCapableCacheWrite, &summary.CacheCapableCacheRead)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, nil
	}
	if err != nil {
		return Summary{}, unavailable("read usage totals", err)
	}
	return summary, nil
}

// Daily returns per-day usage, newest last, limited to the most recent days.
func (s *Store) Daily(ctx context.Context, days int) ([]DayBucket, error) {
	if days <= 0 {
		days = 14
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT day,fresh_input,cache_write,cache_read,output,responses,
		       cache_capable_responses,cache_capable_fresh_input,cache_capable_cache_write,cache_capable_cache_read
		FROM usage_rollup_daily ORDER BY day DESC LIMIT ?`, days)
	if err != nil {
		return nil, unavailable("read daily rollup", err)
	}
	defer rows.Close()
	var buckets []DayBucket
	for rows.Next() {
		var bucket DayBucket
		var day int64
		if err := rows.Scan(&day, &bucket.FreshInput, &bucket.CacheWrite, &bucket.CacheRead,
			&bucket.Output, &bucket.Responses,
			&bucket.CacheCapableResponses, &bucket.CacheCapableFreshInput,
			&bucket.CacheCapableCacheWrite, &bucket.CacheCapableCacheRead); err != nil {
			return nil, unavailable("scan daily rollup", err)
		}
		bucket.Day = time.Unix(day*secondsPerDay, 0).UTC()
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable("read daily rollup", err)
	}
	// Query order is newest-first for the LIMIT; callers chart oldest-first.
	for left, right := 0, len(buckets)-1; left < right; left, right = left+1, right-1 {
		buckets[left], buckets[right] = buckets[right], buckets[left]
	}
	return buckets, nil
}

// Findings returns the largest recorded anomalies of one kind.
func (s *Store) Findings(ctx context.Context, kind FindingKind, limit int) ([]Finding, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind,conversation_id,ts,tokens,ratio,gap_seconds,cause
		FROM usage_findings WHERE kind=?
		ORDER BY tokens DESC, ts DESC LIMIT ?`, string(kind), limit)
	if err != nil {
		return nil, unavailable("read findings", err)
	}
	defer rows.Close()
	var findings []Finding
	for rows.Next() {
		var finding Finding
		var kindValue, cause string
		var ts int64
		if err := rows.Scan(&kindValue, &finding.ConversationID, &ts, &finding.Tokens,
			&finding.Ratio, &finding.GapSeconds, &cause); err != nil {
			return nil, unavailable("scan finding", err)
		}
		finding.Kind = FindingKind(kindValue)
		finding.Cause = FindingCause(cause)
		if ts > 0 {
			finding.Timestamp = time.Unix(ts, 0)
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable("read findings", err)
	}
	return findings, nil
}

const secondsPerDay = 86400

func dayOf(ts int64) int64 {
	if ts <= 0 {
		return 0
	}
	return ts / secondsPerDay
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
