package searchindex

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenAutoFallsBackToSQLite(t *testing.T) {
	var calls []Backend
	failed := func(context.Context, string) (Engine, error) {
		calls = append(calls, BackendTurso)
		return nil, errors.New("native FTS unavailable")
	}
	fallback := func(context.Context, string) (Engine, error) {
		calls = append(calls, BackendSQLite)
		return OpenSQLite(":memory:")
	}
	engine, backend, err := openWith(context.Background(), OpenOptions{Backend: BackendAuto}, failed, fallback)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if backend != BackendSQLite {
		t.Fatalf("backend = %q", backend)
	}
	if len(calls) != 2 || calls[0] != BackendTurso || calls[1] != BackendSQLite {
		t.Fatalf("calls = %v", calls)
	}
}

func TestOpenStrictBackendDoesNotSilentlySwitch(t *testing.T) {
	sentinel := errors.New("strict failure")
	failed := func(context.Context, string) (Engine, error) { return nil, sentinel }
	unexpected := func(context.Context, string) (Engine, error) {
		t.Fatal("fallback opener called in strict mode")
		return nil, nil
	}
	_, _, err := openWith(context.Background(), OpenOptions{Backend: BackendTurso}, failed, unexpected)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAutoProvidesWorkingEngine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	engine, backend, err := Open(ctx, OpenOptions{
		Backend:    BackendAuto,
		TursoPath:  filepath.Join(dir, "history.turso"),
		SQLitePath: filepath.Join(dir, "history.sqlite"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if backend != BackendSQLite {
		t.Fatalf("backend = %q, want %q for pinned tursogo v0.7.1", backend, BackendSQLite)
	}
	assertEngineContract(t, ctx, engine)
}

func TestOpenTursoCapabilityIsExplicit(t *testing.T) {
	ctx := context.Background()
	engine, err := OpenTurso(ctx, filepath.Join(t.TempDir(), "history.turso"))
	if engine != nil {
		engine.Close()
		t.Fatal("pinned tursogo v0.7.1 unexpectedly exposed FTS")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error does not wrap ErrUnavailable: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "fts") {
		t.Fatalf("capability error does not identify FTS: %v", err)
	}
}

func TestLiteralTursoQueryNeutralizesSyntax(t *testing.T) {
	got := literalTursoQuery(`alpha OR title:"beta"`)
	want := `"alpha" AND "OR" AND "title:\"beta\""`
	if got != want {
		t.Fatalf("literalTursoQuery() = %q, want %q", got, want)
	}
}

func assertEngineContract(t *testing.T, ctx context.Context, engine Engine) {
	t.Helper()
	docs := []Document{
		{ID: "main", WorkspacePath: "/work", Title: "Report Studio", Preview: "PDF workflow", Body: "orchestrator pipeline", MessageCount: 642, UpdatedAt: time.Unix(2, 0), Origin: "interactive", BodyIndexed: true},
		{ID: "agent", WorkspacePath: "/work", Title: "Worker", Body: "orchestrator pipeline", MessageCount: 12, UpdatedAt: time.Unix(3, 0), Origin: "subagent", BodyIndexed: true},
	}
	for _, doc := range docs {
		if err := engine.Upsert(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	results, err := engine.Search(ctx, Query{
		Text: "orchestrator", SearchBody: true, WorkspacePath: "/work",
		Origin: "interactive", MinMessages: 20, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "main" {
		t.Fatalf("results = %#v", results)
	}
	metadataOnly, err := engine.Search(ctx, Query{
		Text: "orchestrator", SearchBody: false, WorkspacePath: "/work", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metadataOnly) != 0 {
		t.Fatalf("body leaked into metadata search: %#v", metadataOnly)
	}
	state, ok, err := engine.State(ctx, "main")
	if err != nil || !ok || !state.BodyIndexed {
		t.Fatalf("state=%+v ok=%v err=%v", state, ok, err)
	}
	if err := engine.Delete(ctx, "agent"); err != nil {
		t.Fatal(err)
	}
	ids, err := engine.IDs(ctx, "/work")
	if err != nil || len(ids) != 1 || ids[0] != "main" {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if err := engine.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	ids, err = engine.IDs(ctx, "")
	if err != nil || len(ids) != 0 {
		t.Fatalf("ids after reset=%v err=%v", ids, err)
	}
}
