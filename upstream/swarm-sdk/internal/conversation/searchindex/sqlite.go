package searchindex

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLite struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLite, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty database path", ErrUnavailable)
	}
	dsn := path
	if path != ":memory:" {
		params := url.Values{}
		params.Add("_pragma", "busy_timeout(5000)")
		params.Add("_pragma", "journal_mode(WAL)")
		dsn = "file:" + filepath.ToSlash(path) + "?" + params.Encode()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if path == ":memory:" {
		// Each pooled :memory: connection is a different database.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	engine := &SQLite{db: db}
	if err := engine.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return engine, nil
}

func (s *SQLite) initialize(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS history_index_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
			)`)
	if err != nil {
		return fmt.Errorf("%w: initialize sqlite metadata: %v", ErrUnavailable, err)
	}
	var version int
	err = s.db.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM history_index_meta WHERE key='schema_version'`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("%w: read schema version: %v", ErrUnavailable, err)
	}
	if err == sql.ErrNoRows || version != SchemaVersion {
		return s.rebuildSchema(ctx)
	}
	if err := createSQLiteSchema(ctx, s.db); err != nil {
		return fmt.Errorf("%w: initialize sqlite FTS5: %v", ErrUnavailable, err)
	}
	return nil
}

type sqliteExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func createSQLiteSchema(ctx context.Context, exec sqliteExecer) error {
	_, err := exec.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS history_documents (
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
		);
		-- Covering index for States, which scans every row on every search to
		-- decide what needs re-reading. Listing exactly the projected columns
		-- lets SQLite answer from this compact B-tree and never touch the wide
		-- history_documents rows or their body overflow pages. Once segments
		-- grew the database to ~2.9 GB that page traversal cost ~390ms per
		-- search; this is the fix. IF NOT EXISTS means existing v4 databases
		-- gain it on the next open without a schema-version bump.
		CREATE INDEX IF NOT EXISTS history_documents_freshness
			ON history_documents(workspace_path, id, body_indexed, body_skipped, file_mod_unix_nano, file_size);
		CREATE VIRTUAL TABLE IF NOT EXISTS history_documents_fts USING fts5(
			title, preview, body,
			content='history_documents',
			content_rowid='rowid',
			tokenize='unicode61'
		);
		CREATE TRIGGER IF NOT EXISTS history_documents_ai AFTER INSERT ON history_documents BEGIN
			INSERT INTO history_documents_fts(rowid,title,preview,body)
			VALUES (new.rowid,new.title,new.preview,new.body);
		END;
		CREATE TRIGGER IF NOT EXISTS history_documents_ad AFTER DELETE ON history_documents BEGIN
			INSERT INTO history_documents_fts(history_documents_fts,rowid,title,preview,body)
			VALUES ('delete',old.rowid,old.title,old.preview,old.body);
		END;
		CREATE TRIGGER IF NOT EXISTS history_documents_au AFTER UPDATE ON history_documents BEGIN
			INSERT INTO history_documents_fts(history_documents_fts,rowid,title,preview,body)
			VALUES ('delete',old.rowid,old.title,old.preview,old.body);
			INSERT INTO history_documents_fts(rowid,title,preview,body)
			VALUES (new.rowid,new.title,new.preview,new.body);
		END;
			CREATE TABLE IF NOT EXISTS history_segments (
				conversation_id TEXT NOT NULL,
				ordinal INTEGER NOT NULL,
				workspace_path TEXT NOT NULL,
				message_id TEXT NOT NULL,
				role TEXT NOT NULL,
				kind TEXT NOT NULL,
				tool_name TEXT NOT NULL,
				call_id TEXT NOT NULL,
				failed INTEGER NOT NULL,
				runtime INTEGER NOT NULL,
				timestamp INTEGER NOT NULL,
				text TEXT NOT NULL,
				PRIMARY KEY(conversation_id, ordinal)
			);
			CREATE VIRTUAL TABLE IF NOT EXISTS history_segments_fts USING fts5(
				text,
				content='history_segments',
				content_rowid='rowid',
				tokenize='unicode61'
			);
			CREATE TRIGGER IF NOT EXISTS history_segments_ai AFTER INSERT ON history_segments BEGIN
				INSERT INTO history_segments_fts(rowid,text) VALUES (new.rowid,new.text);
			END;
			CREATE TRIGGER IF NOT EXISTS history_segments_ad AFTER DELETE ON history_segments BEGIN
				INSERT INTO history_segments_fts(history_segments_fts,rowid,text)
				VALUES ('delete',old.rowid,old.text);
			END;
			CREATE TRIGGER IF NOT EXISTS history_segments_au AFTER UPDATE ON history_segments BEGIN
				INSERT INTO history_segments_fts(history_segments_fts,rowid,text)
				VALUES ('delete',old.rowid,old.text);
				INSERT INTO history_segments_fts(rowid,text) VALUES (new.rowid,new.text);
			END;
			-- The primary key already supports replace/delete by conversation_id.
			-- Keep secondary indexes aligned with real filters: workspace, tool,
			-- role, and time each get selective lookups, while (kind,failed)
			-- jointly serves both kind and outcome queries. Standalone failed and
			-- runtime indexes would be low-selectivity write overhead.
			CREATE INDEX IF NOT EXISTS history_segments_workspace_idx
				ON history_segments(workspace_path);
			CREATE INDEX IF NOT EXISTS history_segments_tool_name_idx
				ON history_segments(tool_name);
			CREATE INDEX IF NOT EXISTS history_segments_kind_failed_idx
				ON history_segments(kind, failed);
			CREATE INDEX IF NOT EXISTS history_segments_role_idx
				ON history_segments(role);
			CREATE INDEX IF NOT EXISTS history_segments_timestamp_idx
				ON history_segments(timestamp);
	`)
	return err
}

func (s *SQLite) rebuildSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: begin sqlite schema rebuild: %v", ErrUnavailable, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DROP TRIGGER IF EXISTS history_documents_ai;
		DROP TRIGGER IF EXISTS history_documents_ad;
		DROP TRIGGER IF EXISTS history_documents_au;
			DROP TRIGGER IF EXISTS history_segments_ai;
			DROP TRIGGER IF EXISTS history_segments_ad;
			DROP TRIGGER IF EXISTS history_segments_au;
			DROP TABLE IF EXISTS history_segments_fts;
			DROP TABLE IF EXISTS history_segments;
		DROP TABLE IF EXISTS history_documents_fts;
		DROP TABLE IF EXISTS history_documents;`); err != nil {
		return fmt.Errorf("%w: drop stale sqlite schema: %v", ErrUnavailable, err)
	}
	if err := createSQLiteSchema(ctx, tx); err != nil {
		return fmt.Errorf("%w: rebuild sqlite FTS5: %v", ErrUnavailable, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO history_index_meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SchemaVersion); err != nil {
		return fmt.Errorf("%w: store sqlite schema version: %v", ErrUnavailable, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit sqlite schema rebuild: %v", ErrUnavailable, err)
	}
	return nil
}

func (s *SQLite) Upsert(ctx context.Context, d Document) error {
	if strings.TrimSpace(d.ID) == "" {
		return errorsNew("document id is required")
	}
	_, err := s.db.ExecContext(ctx, `
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
	return err
}

func errorsNew(message string) error { return fmt.Errorf("searchindex: %s", message) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *SQLite) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM history_documents WHERE id=?`, id)
	return err
}

func (s *SQLite) State(ctx context.Context, id string) (Document, bool, error) {
	row := s.db.QueryRowContext(ctx, `
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
		return Document{}, false, err
	}
	d.UpdatedAt = time.Unix(0, updated)
	d.BodyIndexed = bodyIndexed != 0
	d.BodySkipped = bodySkipped != 0
	return d, true, nil
}

func (s *SQLite) IDs(ctx context.Context, workspace string) ([]string, error) {
	query := `SELECT id FROM history_documents`
	var args []any
	if workspace != "" {
		query += ` WHERE workspace_path=?`
		args = append(args, workspace)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
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
// by ID. The body column is deliberately excluded: SyncFiles only needs the
// metadata to decide whether to re-parse a file, and selecting bodies here
// would pull the entire indexed corpus into memory on every search.
// States returns only the columns SyncFiles needs to decide whether a file must
// be re-read. It deliberately does NOT select title/preview/body: those live in
// the same wide row, and once segments grew this database to ~2.9 GB, scanning
// them dragged in overflow pages and cost ~390ms per search on the real corpus.
// With the freshness-only projection the scan is served entirely from the
// covering index created in initialize(). Callers needing a whole document must
// ask for it explicitly with State.
func (s *SQLite) States(ctx context.Context, workspace string) (map[string]Document, error) {
	query := `
		SELECT id,workspace_path,body_indexed,body_skipped,file_mod_unix_nano,file_size
		FROM history_documents`
	var args []any
	if workspace != "" {
		query += ` WHERE workspace_path=?`
		args = append(args, workspace)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	states := make(map[string]Document)
	for rows.Next() {
		var d Document
		var bodyIndexed, bodySkipped int
		if err := rows.Scan(&d.ID, &d.WorkspacePath,
			&bodyIndexed, &bodySkipped, &d.FileModUnixNano, &d.FileSize); err != nil {
			return nil, err
		}
		d.BodyIndexed = bodyIndexed != 0
		d.BodySkipped = bodySkipped != 0
		states[d.ID] = d
	}
	return states, rows.Err()
}

func (s *SQLite) Search(ctx context.Context, q Query) ([]Result, error) {
	if q.Limit <= 0 {
		q.Limit = 10
	}
	where, args := searchFilters(q)
	score := "0.0"
	from := "history_documents d"
	if strings.TrimSpace(q.Text) != "" {
		if q.SearchBody {
			from = "history_documents_fts f JOIN history_documents d ON d.rowid=f.rowid"
			where = append([]string{"history_documents_fts MATCH ?"}, where...)
			args = append([]any{literalFTSQuery(q.Text)}, args...)
			score = "-bm25(history_documents_fts, 8.0, 3.0, 1.0)"
		} else {
			from = "history_documents_fts f JOIN history_documents d ON d.rowid=f.rowid"
			where = append([]string{"history_documents_fts MATCH ?"}, where...)
			args = append([]any{metadataFTSQuery(q.Text)}, args...)
			score = "-bm25(history_documents_fts, 8.0, 3.0, 0.0)"
		}
	}
	orderColumn := score
	switch q.Sort {
	case "recency":
		orderColumn = "d.updated_at"
	case "message_count":
		orderColumn = "d.message_count"
	}
	order := "DESC"
	if q.Order == "asc" {
		order = "ASC"
	}
	statement := fmt.Sprintf(`
			SELECT d.id,d.workspace_path,d.title,d.preview,d.body,d.message_count,d.updated_at,d.origin,d.body_indexed,d.body_skipped,d.file_mod_unix_nano,d.file_size,%s
		FROM %s`, score, from)
	if len(where) > 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	statement += fmt.Sprintf(" ORDER BY %s %s, d.updated_at DESC, d.id ASC LIMIT ?", orderColumn, order)
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
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

func literalFTSQuery(text string) string {
	terms := strings.Fields(text)
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND ")
}

func metadataFTSQuery(text string) string {
	terms := strings.Fields(text)
	clauses := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted := `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
		clauses = append(clauses, "(title:"+quoted+" OR preview:"+quoted+")")
	}
	return strings.Join(clauses, " AND ")
}

func searchFilters(q Query) ([]string, []any) {
	var where []string
	var args []any
	if q.WorkspacePath != "" {
		where = append(where, "d.workspace_path=?")
		args = append(args, q.WorkspacePath)
	}
	if q.Origin != "" {
		where = append(where, "d.origin=?")
		args = append(args, q.Origin)
	}
	if q.ExcludeHeadless {
		where = append(where, "d.origin<>?")
		args = append(args, "headless")
	}
	if q.MinMessages > 0 {
		where = append(where, "d.message_count>=?")
		args = append(args, q.MinMessages)
	}
	return where, args
}

func (s *SQLite) Reset(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
			DELETE FROM history_segments;
		DELETE FROM history_documents;
			INSERT INTO history_segments_fts(history_segments_fts) VALUES('rebuild');
		INSERT INTO history_documents_fts(history_documents_fts) VALUES('rebuild');
		INSERT INTO history_index_meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SchemaVersion)
	return err
}

func (s *SQLite) Close() error { return s.db.Close() }
