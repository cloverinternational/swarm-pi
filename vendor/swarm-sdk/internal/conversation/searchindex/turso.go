package searchindex

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "turso.tech/database/tursogo"
)

const (
	tursoSummaryIndex = "history_documents_summary_fts"
	tursoBodyIndex    = "history_documents_body_fts"
)

var (
	tursoCapabilityOnce sync.Once
	tursoCapabilityErr  error
)

// Turso is a local, in-process Turso Database index backed by the Rust engine
// and its native Tantivy full-text index. Conversation JSON remains the source
// of truth, so this database can always be rebuilt.
type Turso struct {
	db *sql.DB
}

func OpenTurso(ctx context.Context, path string) (*Turso, error) {
	if strings.TrimSpace(path) == "" {
		return nil, unavailable("empty Turso database path", nil)
	}
	if path != ":memory:" {
		if err := ensureParentDir(path); err != nil {
			return nil, unavailable("create Turso database directory", err)
		}
	}
	if err := tursoRuntimeCapability(); err != nil {
		return nil, err
	}
	params := url.Values{
		"experimental":  {"index_method"},
		"_busy_timeout": {"5000"},
	}
	dsn := path + "?" + params.Encode()
	db, err := sql.Open("turso", dsn)
	if err != nil {
		return nil, unavailable("open Turso database", err)
	}
	// Turso connections own transaction and busy-timeout state. A single
	// connection avoids cross-connection visibility surprises in this small
	// derived index and still permits concurrent callers through database/sql.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	engine := &Turso{db: db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, unavailable("load Turso native library", err)
	}
	if err := engine.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return engine, nil
}

func tursoRuntimeCapability() error {
	tursoCapabilityOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tursoCapabilityErr = probeTursoRuntime(ctx)
	})
	return tursoCapabilityErr
}

func probeTursoRuntime(ctx context.Context) error {
	db, err := sql.Open("turso", ":memory:?experimental=index_method&_busy_timeout=5000")
	if err != nil {
		return unavailable("open Turso capability database", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return unavailable("load Turso native library", err)
	}
	statements := []string{
		`CREATE TABLE swarm_fts_probe (title TEXT, body TEXT)`,
		`CREATE INDEX swarm_fts_probe_idx ON swarm_fts_probe USING fts (title, body)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return unavailable("Turso native library lacks Tantivy FTS", err)
		}
	}
	rows, err := db.QueryContext(ctx, `
		SELECT fts_score(title, body, ?1) AS score
		FROM swarm_fts_probe
		WHERE (title, body) MATCH ?1
		ORDER BY score DESC LIMIT ?`, "__swarm_fts_capability_probe__", 1)
	if err != nil {
		return unavailable("Turso native library lacks FTS query functions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var score float64
		if err := rows.Scan(&score); err != nil {
			return unavailable("scan Turso FTS capability result", err)
		}
	}
	if err := rows.Err(); err != nil {
		return unavailable("run Turso FTS capability query", err)
	}
	return nil
}

func ensureParentDir(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}

func (t *Turso) initialize(ctx context.Context) error {
	if _, err := t.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS history_index_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`); err != nil {
		return unavailable("initialize Turso metadata", err)
	}

	var version int
	err := t.db.QueryRowContext(ctx,
		`SELECT CAST(value AS INTEGER) FROM history_index_meta WHERE key='schema_version'`,
	).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return unavailable("read Turso schema version", err)
	}
	if err == sql.ErrNoRows || version != SchemaVersion {
		if err := t.rebuildSchema(ctx); err != nil {
			return err
		}
	} else if err := t.createSchema(ctx); err != nil {
		return err
	}
	return t.probeFTS(ctx)
}

func (t *Turso) createSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS history_documents (
			id TEXT PRIMARY KEY,
			workspace_path TEXT NOT NULL,
			title TEXT NOT NULL,
			preview TEXT NOT NULL,
			body TEXT NOT NULL,
			message_count INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			origin TEXT NOT NULL,
				body_indexed INTEGER NOT NULL DEFAULT 0,
				body_skipped INTEGER NOT NULL DEFAULT 0,
				file_mod_unix_nano INTEGER NOT NULL DEFAULT 0,
				file_size INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS ` + tursoSummaryIndex + `
			ON history_documents USING fts (title, preview)
			WITH (tokenizer='default', weights='title=8.0,preview=3.0')`,
		`CREATE INDEX IF NOT EXISTS ` + tursoBodyIndex + `
			ON history_documents USING fts (title, preview, body)
			WITH (tokenizer='default', weights='title=8.0,preview=3.0,body=1.0')`,
	}
	for _, statement := range statements {
		if _, err := t.db.ExecContext(ctx, statement); err != nil {
			return unavailable("initialize Turso Tantivy FTS", err)
		}
	}
	return nil
}

func (t *Turso) rebuildSchema(ctx context.Context) error {
	// This is a derived index. Rebuilding instead of migrating avoids retaining
	// stale Tantivy configuration when weights or indexed fields change.
	for _, statement := range []string{
		`DROP INDEX IF EXISTS ` + tursoSummaryIndex,
		`DROP INDEX IF EXISTS ` + tursoBodyIndex,
		`DROP TABLE IF EXISTS history_documents`,
	} {
		if _, err := t.db.ExecContext(ctx, statement); err != nil {
			return unavailable("reset Turso schema", err)
		}
	}
	if err := t.createSchema(ctx); err != nil {
		return err
	}
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO history_index_meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SchemaVersion)
	if err != nil {
		return unavailable("store Turso schema version", err)
	}
	return nil
}

func (t *Turso) probeFTS(ctx context.Context) error {
	// Creating the indexes proves the Rust library was built with the `fts`
	// feature. Preparing a real match/score query additionally catches library
	// releases that expose the module but not the query functions.
	rows, err := t.db.QueryContext(ctx, `
		SELECT fts_score(title, preview, ?1) AS score
		FROM history_documents
		WHERE (title, preview) MATCH ?1
		ORDER BY score DESC LIMIT ?`, "__swarm_fts_capability_probe__", 1)
	if err != nil {
		return unavailable("probe Turso Tantivy query functions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var score float64
		if err := rows.Scan(&score); err != nil {
			return unavailable("scan Turso Tantivy capability probe", err)
		}
	}
	if err := rows.Err(); err != nil {
		return unavailable("run Turso Tantivy capability probe", err)
	}
	return nil
}

func (t *Turso) Upsert(ctx context.Context, d Document) error {
	if strings.TrimSpace(d.ID) == "" {
		return errorsNew("document id is required")
	}
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO history_documents
				(id,workspace_path,title,preview,body,message_count,updated_at,origin,body_indexed,body_skipped,file_mod_unix_nano,file_size)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_path=excluded.workspace_path,title=excluded.title,preview=excluded.preview,
			body=excluded.body,message_count=excluded.message_count,updated_at=excluded.updated_at,
				origin=excluded.origin,body_indexed=excluded.body_indexed,body_skipped=excluded.body_skipped,
				file_mod_unix_nano=excluded.file_mod_unix_nano,file_size=excluded.file_size`,
		d.ID, d.WorkspacePath, d.Title, d.Preview, d.Body, d.MessageCount,
		d.UpdatedAt.UnixNano(), d.Origin, boolInt(d.BodyIndexed), boolInt(d.BodySkipped),
		d.FileModUnixNano, d.FileSize)
	if err != nil {
		return fmt.Errorf("searchindex: Turso upsert: %w", err)
	}
	return nil
}

func (t *Turso) Delete(ctx context.Context, id string) error {
	if _, err := t.db.ExecContext(ctx, `DELETE FROM history_documents WHERE id=?`, id); err != nil {
		return fmt.Errorf("searchindex: Turso delete: %w", err)
	}
	return nil
}

func (t *Turso) State(ctx context.Context, id string) (Document, bool, error) {
	row := t.db.QueryRowContext(ctx, `
			SELECT id,workspace_path,title,preview,message_count,updated_at,origin,body_indexed,body_skipped,file_mod_unix_nano,file_size
		FROM history_documents WHERE id=?`, id)
	var d Document
	var updated int64
	var bodyIndexed, bodySkipped int
	err := row.Scan(&d.ID, &d.WorkspacePath, &d.Title, &d.Preview, &d.MessageCount, &updated, &d.Origin,
		&bodyIndexed, &bodySkipped, &d.FileModUnixNano, &d.FileSize)
	if err == sql.ErrNoRows {
		return Document{}, false, nil
	}
	if err != nil {
		return Document{}, false, fmt.Errorf("searchindex: Turso state: %w", err)
	}
	d.UpdatedAt = time.Unix(0, updated)
	d.BodyIndexed = bodyIndexed != 0
	d.BodySkipped = bodySkipped != 0
	return d, true, nil
}

func (t *Turso) IDs(ctx context.Context, workspace string) ([]string, error) {
	statement := `SELECT id FROM history_documents`
	var args []any
	if workspace != "" {
		statement += ` WHERE workspace_path=?`
		args = append(args, workspace)
	}
	rows, err := t.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("searchindex: Turso list IDs: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// States returns every indexed document's freshness state in one query, keyed
// by ID. Mirrors SQLite.States; see that method for why the body column is
// excluded and why SyncFiles must not do a per-file lookup.
func (t *Turso) States(ctx context.Context, workspace string) (map[string]Document, error) {
	statement := `
		SELECT id,workspace_path,title,preview,message_count,updated_at,origin,body_indexed,body_skipped,file_mod_unix_nano,file_size
		FROM history_documents`
	var args []any
	if workspace != "" {
		statement += ` WHERE workspace_path=?`
		args = append(args, workspace)
	}
	rows, err := t.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("searchindex: Turso states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	states := make(map[string]Document)
	for rows.Next() {
		var d Document
		var updated int64
		var bodyIndexed, bodySkipped int
		if err := rows.Scan(&d.ID, &d.WorkspacePath, &d.Title, &d.Preview, &d.MessageCount, &updated, &d.Origin,
			&bodyIndexed, &bodySkipped, &d.FileModUnixNano, &d.FileSize); err != nil {
			return nil, err
		}
		d.UpdatedAt = time.Unix(0, updated)
		d.BodyIndexed = bodyIndexed != 0
		d.BodySkipped = bodySkipped != 0
		states[d.ID] = d
	}
	return states, rows.Err()
}

func (t *Turso) Search(ctx context.Context, q Query) ([]Result, error) {
	if q.Limit <= 0 {
		q.Limit = 10
	}
	where, args := tursoSearchFilters(q)
	scoreExpr := "0.0"
	text := strings.TrimSpace(q.Text)
	if text != "" {
		columns := "title, preview"
		if q.SearchBody {
			columns = "title, preview, body"
		}
		query := literalTursoQuery(text)
		scoreExpr = "fts_score(" + columns + ", ?1)"
		where = append([]string{"(" + columns + ") MATCH ?1"}, where...)
		args = append([]any{query}, args...)
	}
	orderColumn := "search_score"
	switch q.Sort {
	case "recency":
		orderColumn = "updated_at"
	case "message_count":
		orderColumn = "message_count"
	}
	order := "DESC"
	if q.Order == "asc" {
		order = "ASC"
	}
	statement := `
			SELECT id,workspace_path,title,preview,body,message_count,updated_at,origin,body_indexed,body_skipped,file_mod_unix_nano,file_size,` + scoreExpr + ` AS search_score
		FROM history_documents`
	if len(where) > 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	statement += " ORDER BY " + orderColumn + " " + order + ", updated_at DESC, id ASC LIMIT ?"
	args = append(args, q.Limit)
	rows, err := t.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("searchindex: Turso search: %w", err)
	}
	defer rows.Close()
	results := make([]Result, 0, q.Limit)
	for rows.Next() {
		var r Result
		var updated int64
		var bodyIndexed, bodySkipped int
		if err := rows.Scan(&r.ID, &r.WorkspacePath, &r.Title, &r.Preview, &r.Body, &r.MessageCount, &updated, &r.Origin,
			&bodyIndexed, &bodySkipped, &r.FileModUnixNano, &r.FileSize, &r.Score); err != nil {
			return nil, err
		}
		r.UpdatedAt = time.Unix(0, updated)
		r.BodyIndexed = bodyIndexed != 0
		r.BodySkipped = bodySkipped != 0
		results = append(results, r)
	}
	return results, rows.Err()
}

func literalTursoQuery(text string) string {
	terms := strings.Fields(text)
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		// Tantivy phrases escape quotes with a backslash. Quoting every token and
		// joining with AND makes tool input literal instead of query syntax.
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(term)
		quoted = append(quoted, `"`+escaped+`"`)
	}
	return strings.Join(quoted, " AND ")
}

func tursoSearchFilters(q Query) ([]string, []any) {
	var where []string
	var args []any
	if q.WorkspacePath != "" {
		where = append(where, "workspace_path=?")
		args = append(args, q.WorkspacePath)
	}
	if q.Origin != "" {
		where = append(where, "origin=?")
		args = append(args, q.Origin)
	}
	if q.ExcludeHeadless {
		where = append(where, "origin<>?")
		args = append(args, "headless")
	}
	if q.MinMessages > 0 {
		where = append(where, "message_count>=?")
		args = append(args, q.MinMessages)
	}
	return where, args
}

func (t *Turso) Reset(ctx context.Context) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("searchindex: begin Turso reset: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_documents`); err != nil {
		return fmt.Errorf("searchindex: reset Turso documents: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO history_index_meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SchemaVersion); err != nil {
		return fmt.Errorf("searchindex: reset Turso version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("searchindex: commit Turso reset: %w", err)
	}
	return nil
}

func (t *Turso) ReplaceSegments(context.Context, string, []Segment) error {
	return fmt.Errorf("%w: Turso segment indexing is not implemented", ErrSegmentsUnsupported)
}

func (t *Turso) DeleteSegments(context.Context, string) error {
	return fmt.Errorf("%w: Turso segment indexing is not implemented", ErrSegmentsUnsupported)
}

func (t *Turso) SearchSegments(context.Context, SegmentQuery) ([]SegmentHit, error) {
	return nil, fmt.Errorf("%w: Turso segment search is not implemented", ErrSegmentsUnsupported)
}

func (t *Turso) TermStats(context.Context, SegmentFilter, StatsOptions) ([]TermStat, error) {
	return nil, fmt.Errorf("%w: Turso segment statistics are not implemented", ErrSegmentsUnsupported)
}

func (t *Turso) SegmentCount(context.Context, SegmentFilter) (int, error) {
	return 0, fmt.Errorf("%w: Turso segment counting is not implemented", ErrSegmentsUnsupported)
}

func (t *Turso) Close() error { return t.db.Close() }
