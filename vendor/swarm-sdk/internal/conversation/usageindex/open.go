package usageindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	_ "turso.tech/database/tursogo"
)

var (
	tursoCapabilityOnce sync.Once
	tursoCapabilityErr  error
)

// Open selects the requested backend. Auto prefers the native Turso engine —
// the same engine the history search index already runs on — and falls back to
// the portable pure-Go SQLite driver when the native library is unavailable.
// The usage index needs no full-text search, so both backends run identical SQL.
func Open(ctx context.Context, opts OpenOptions) (*Store, Backend, error) {
	backend := opts.Backend
	if backend == "" {
		backend = BackendAuto
	}
	switch backend {
	case BackendTurso:
		store, err := openTurso(ctx, opts.TursoPath)
		if err != nil {
			return nil, "", err
		}
		return store, BackendTurso, nil
	case BackendSQLite:
		store, err := openSQLite(ctx, opts.SQLitePath)
		if err != nil {
			return nil, "", err
		}
		return store, BackendSQLite, nil
	case BackendAuto:
		store, tursoErr := openTurso(ctx, opts.TursoPath)
		if tursoErr == nil {
			return store, BackendTurso, nil
		}
		store, sqliteErr := openSQLite(ctx, opts.SQLitePath)
		if sqliteErr == nil {
			return store, BackendSQLite, nil
		}
		return nil, "", fmt.Errorf("%w: turso: %v; sqlite: %v", ErrUnavailable, tursoErr, sqliteErr)
	default:
		return nil, "", fmt.Errorf("%w: unknown backend %q", ErrUnavailable, backend)
	}
}

func openTurso(ctx context.Context, path string) (*Store, error) {
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
	params := url.Values{"_busy_timeout": {"5000"}}
	db, err := sql.Open("turso", path+"?"+params.Encode())
	if err != nil {
		return nil, unavailable("open Turso database", err)
	}
	// Turso connections own transaction and busy-timeout state; a single
	// connection keeps this small derived index free of cross-connection
	// visibility surprises while database/sql still serializes callers.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, unavailable("load Turso native library", err)
	}
	return newStore(ctx, db, BackendTurso)
}

// tursoRuntimeCapability probes the native library once per process. Unlike the
// search index this does not require Tantivy FTS: plain tables are enough.
func tursoRuntimeCapability() error {
	tursoCapabilityOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tursoCapabilityErr = probeTursoRuntime(ctx)
	})
	return tursoCapabilityErr
}

func probeTursoRuntime(ctx context.Context) error {
	db, err := sql.Open("turso", ":memory:?_busy_timeout=5000")
	if err != nil {
		return unavailable("open Turso capability database", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return unavailable("load Turso native library", err)
	}
	for _, statement := range []string{
		`CREATE TABLE swarm_usage_probe (id TEXT PRIMARY KEY, value INTEGER NOT NULL)`,
		`INSERT INTO swarm_usage_probe(id,value) VALUES('probe',1)
			ON CONFLICT(id) DO UPDATE SET value=excluded.value`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return unavailable("Turso native library rejected index schema", err)
		}
	}
	return nil
}

func openSQLite(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, unavailable("empty SQLite database path", nil)
	}
	dsn := path
	if path != ":memory:" {
		if err := ensureParentDir(path); err != nil {
			return nil, unavailable("create SQLite database directory", err)
		}
		params := url.Values{}
		params.Add("_pragma", "busy_timeout(5000)")
		params.Add("_pragma", "journal_mode(WAL)")
		dsn = "file:" + filepath.ToSlash(path) + "?" + params.Encode()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, unavailable("open SQLite database", err)
	}
	// Each pooled :memory: connection is a different database, and a single
	// writer keeps WAL contention predictable for the file-backed case too.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, unavailable("open SQLite database", err)
	}
	return newStore(ctx, db, BackendSQLite)
}

func ensureParentDir(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}

func unavailable(operation string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, operation)
	}
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %s: %v", ErrUnavailable, operation, err)
}
