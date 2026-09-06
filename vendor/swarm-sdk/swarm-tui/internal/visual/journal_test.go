package visual

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournal_AppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "screens.jsonl")

	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if err := j.AppendCreate(&Screen{
		ID:        "s1",
		Kind:      ScreenKindPending,
		Title:     "Which?",
		AgentID:   "a1",
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := j.AppendResolve("s1", []string{"a"}, false); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	screens, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := screens["s1"]
	if !ok {
		t.Fatalf("missing s1")
	}
	if s.Kind != ScreenKindResolved {
		t.Errorf("kind = %s, want resolved", s.Kind)
	}
	if len(s.Answer) != 1 || s.Answer[0] != "a" {
		t.Errorf("answer = %v, want [a]", s.Answer)
	}
}

func TestJournal_ReplayMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "never-existed.jsonl")
	screens, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(screens) != 0 {
		t.Errorf("got %d screens, want 0", len(screens))
	}
}

func TestJournal_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "screens.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = j.AppendCreate(&Screen{ID: "s1", Kind: ScreenKindPending, CreatedAt: time.Now()})
	_ = j.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("perm = %o, want 0600", mode)
	}
}
