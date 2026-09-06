package searchindex

import (
	"context"
	"errors"
	"fmt"
)

type engineOpener func(context.Context, string) (Engine, error)

// Open selects the requested index backend. Auto prefers Turso Database and
// falls back to portable SQLite FTS5 when the native library or Tantivy FTS
// capability is unavailable.
func Open(ctx context.Context, opts OpenOptions) (Engine, Backend, error) {
	return openWith(ctx, opts, openTursoEngine, openSQLiteEngine)
}

func openWith(ctx context.Context, opts OpenOptions, openTurso, openSQLite engineOpener) (Engine, Backend, error) {
	backend := opts.Backend
	if backend == "" {
		backend = BackendAuto
	}
	switch backend {
	case BackendTurso:
		engine, err := openTurso(ctx, opts.TursoPath)
		if err != nil {
			return nil, "", err
		}
		return engine, BackendTurso, nil
	case BackendSQLite:
		engine, err := openSQLite(ctx, opts.SQLitePath)
		if err != nil {
			return nil, "", err
		}
		return engine, BackendSQLite, nil
	case BackendAuto:
		tursoEngine, tursoErr := openTurso(ctx, opts.TursoPath)
		if tursoErr == nil {
			return tursoEngine, BackendTurso, nil
		}
		sqliteEngine, sqliteErr := openSQLite(ctx, opts.SQLitePath)
		if sqliteErr == nil {
			return sqliteEngine, BackendSQLite, nil
		}
		return nil, "", fmt.Errorf("%w: turso: %v; sqlite: %v", ErrUnavailable, tursoErr, sqliteErr)
	default:
		return nil, "", fmt.Errorf("%w: unknown backend %q", ErrUnavailable, backend)
	}
}

func openTursoEngine(ctx context.Context, path string) (Engine, error) {
	return OpenTurso(ctx, path)
}

func openSQLiteEngine(_ context.Context, path string) (Engine, error) {
	return OpenSQLite(path)
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
