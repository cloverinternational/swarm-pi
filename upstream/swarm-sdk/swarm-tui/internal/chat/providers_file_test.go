package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAtomicWriteProvidersJSON_RefusesEmptyAndInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	if err := atomicWriteProvidersJSON(path, []byte(""), 0600); err == nil {
		t.Fatal("expected error writing empty data")
	}
	if err := atomicWriteProvidersJSON(path, []byte("   \n"), 0600); err == nil {
		t.Fatal("expected error writing whitespace data")
	}
	if err := atomicWriteProvidersJSON(path, []byte("{not json"), 0600); err == nil {
		t.Fatal("expected error writing invalid JSON")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("refused writes must not create the file")
	}
}

func TestAtomicWriteProvidersJSON_WritesAndRollsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	v1 := []byte(`[{"name":"one"}]`)
	if err := atomicWriteProvidersJSON(path, v1, 0600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(v1) {
		t.Fatalf("content = %q, want %q", got, v1)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("perm = %v (err %v), want 0600", fi.Mode().Perm(), err)
	}

	v2 := []byte(`[{"name":"one"},{"name":"two"}]`)
	if err := atomicWriteProvidersJSON(path, v2, 0600); err != nil {
		t.Fatalf("second write: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("expected rolling backup: %v", err)
	}
	if string(bak) != string(v1) {
		t.Fatalf("backup = %q, want previous content %q", bak, v1)
	}
	// No temp litter left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestReadProvidersJSONWithRecovery_HealthyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")
	want := []byte(`[{"name":"x"}]`)
	os.WriteFile(path, want, 0600)

	got, err := readProvidersJSONWithRecovery(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("got %q err %v, want %q", got, err, want)
	}
}

func TestReadProvidersJSONWithRecovery_MissingNoBackup(t *testing.T) {
	dir := t.TempDir()
	if _, err := readProvidersJSONWithRecovery(filepath.Join(dir, "providers.json")); !os.IsNotExist(err) {
		t.Fatalf("want IsNotExist, got %v", err)
	}
}

func TestReadProvidersJSONWithRecovery_EmptyFileRestoresNewestBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	// The disaster: a 0-byte providers.json (truncate-then-crash).
	os.WriteFile(path, nil, 0600)
	// Two backups: an older valid one and a newer valid one (mirrors the real
	// ~/.swarmos backup naming zoo).
	oldBak := path + ".backup"
	newBak := path + ".bak-1782848029"
	os.WriteFile(oldBak, []byte(`[{"name":"old"}]`), 0600)
	os.WriteFile(newBak, []byte(`[{"name":"new"}]`), 0600)
	past := time.Now().Add(-24 * time.Hour)
	os.Chtimes(oldBak, past, past)

	got, err := readProvidersJSONWithRecovery(path)
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	var arr []map[string]any
	if json.Unmarshal(got, &arr) != nil || len(arr) != 1 || arr[0]["name"] != "new" {
		t.Fatalf("restored %q, want newest backup content", got)
	}

	// The bad file was quarantined and the restored content is now in place.
	quarantined, _ := filepath.Glob(path + ".corrupt_*")
	if len(quarantined) != 1 {
		t.Fatalf("expected 1 quarantined file, got %v", quarantined)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || string(onDisk) != string(got) {
		t.Fatalf("providers.json not restored in place: %q err %v", onDisk, err)
	}
}

func TestReadProvidersJSONWithRecovery_CorruptFileSkipsCorruptBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	os.WriteFile(path, []byte("{torn"), 0600)
	os.WriteFile(path+".bak_zzz", []byte("also torn"), 0600) // newer but invalid
	good := path + ".bak_good"
	os.WriteFile(good, []byte(`[{"name":"ok"}]`), 0600)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(good, past, past)

	got, err := readProvidersJSONWithRecovery(path)
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	if !strings.Contains(string(got), `"ok"`) {
		t.Fatalf("restored %q, want the valid backup", got)
	}
}

func TestReadProvidersJSONWithRecovery_EmptyFileNoBackupErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")
	os.WriteFile(path, nil, 0600)

	if _, err := readProvidersJSONWithRecovery(path); err == nil {
		t.Fatal("expected error for empty file with no backups")
	}
}
