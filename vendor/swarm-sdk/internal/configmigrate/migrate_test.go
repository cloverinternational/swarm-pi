package configmigrate

import (
	"os"
	"path/filepath"
	"testing"
)

// setupRoots wires SWARM_HOME (dest) and the homeDir hook (legacy source root
// parent) to temp dirs, and returns (legacyRoot, destRoot).
func setupRoots(t *testing.T) (string, string) {
	t.Helper()
	dest := t.TempDir()
	home := t.TempDir()
	t.Setenv("SWARM_HOME", dest)

	orig := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = orig })

	legacy := filepath.Join(home, legacyDirName)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	return legacy, dest
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func contains(list []string, substr string) bool {
	for _, v := range list {
		if filepath.Base(v) == substr || v == substr {
			return true
		}
		if len(substr) > 0 && filepathHasSuffix(v, substr) {
			return true
		}
	}
	return false
}

func filepathHasSuffix(p, suffix string) bool {
	return len(p) >= len(suffix) && p[len(p)-len(suffix):] == suffix
}

func TestMigrateMovesFiles(t *testing.T) {
	legacy, dest := setupRoots(t)

	writeFile(t, filepath.Join(legacy, "providers.json"), `{"p":1}`)
	writeFile(t, filepath.Join(legacy, "config.yaml"), "key: value\n")
	writeFile(t, filepath.Join(legacy, "anthropic_oauth.json"), `{"tok":"x"}`)
	writeFile(t, filepath.Join(legacy, "skills", "foo", "SKILL.md"), "# skill\n")
	writeFile(t, filepath.Join(legacy, "vault", "secret.age"), "cipher")
	writeFile(t, filepath.Join(legacy, "swarms", "alpha", "peers", "peer.json"), "{}")
	writeFile(t, filepath.Join(legacy, "deepwiki", "requests", "req.jsonl"), "{}\n")
	writeFile(t, filepath.Join(legacy, "findings", "cache", "findings.jsonl"), "{}\n")
	writeFile(t, filepath.Join(legacy, "analytics-spool", "events.jsonl"), "{}\n")
	writeFile(t, filepath.Join(legacy, "tui_accounts.json"), `{"accounts":[]}`)
	writeFile(t, filepath.Join(legacy, "cloud.json"), `{"device_id":"d"}`)
	writeFile(t, filepath.Join(legacy, "cloud_tokens.json"), `{"token":"t"}`)

	rep, err := Migrate()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	checks := []string{
		filepath.Join(dest, "config", "providers.json"),
		filepath.Join(dest, "config", "config.yaml"),
		filepath.Join(dest, "config", "oauth", "anthropic.json"),
		filepath.Join(dest, "skills", "foo", "SKILL.md"),
		filepath.Join(dest, "vault", "secret.age"),
		filepath.Join(dest, "swarms", "alpha", "peers", "peer.json"),
		filepath.Join(dest, "deepwiki", "requests", "req.jsonl"),
		filepath.Join(dest, "findings", "cache", "findings.jsonl"),
		filepath.Join(dest, "analytics-spool", "events.jsonl"),
		filepath.Join(dest, "tui_accounts.json"),
		filepath.Join(dest, "cloud.json"),
		filepath.Join(dest, "cloud_tokens.json"),
	}
	for _, p := range checks {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected migrated file %s: %v", p, err)
		}
	}
	if len(rep.Moved) == 0 {
		t.Fatal("expected Moved entries")
	}
	// Marker was written.
	if _, err := os.Stat(filepath.Join(dest, "config", markerName)); err != nil {
		t.Fatalf("marker not written: %v", err)
	}
}

func TestMigrateSkipsBakAndCorrupt(t *testing.T) {
	legacy, dest := setupRoots(t)

	writeFile(t, filepath.Join(legacy, "providers.json.bak_123"), "old")
	writeFile(t, filepath.Join(legacy, "credentials.json.corrupt_1"), "bad")
	writeFile(t, filepath.Join(legacy, "hooks.json_empty"), "")
	writeFile(t, filepath.Join(legacy, "mcp_servers.json.clobbered.9"), "x")

	rep, err := Migrate()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// None of these should land in dest under their canonical names.
	shouldNotExist := []string{
		filepath.Join(dest, "config", "providers.json"),
		filepath.Join(dest, "config", "credentials.json"),
		filepath.Join(dest, "config", "hooks.json"),
		filepath.Join(dest, "config", "mcp_servers.json"),
	}
	for _, p := range shouldNotExist {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("skip file wrongly migrated to %s", p)
		}
	}
	if len(rep.Skipped) < 4 {
		t.Fatalf("expected >=4 skipped, got %d: %v", len(rep.Skipped), rep.Skipped)
	}
}

func TestMigrateBareOAuthUsesAnthropicDestination(t *testing.T) {
	legacy, dest := setupRoots(t)
	writeFile(t, filepath.Join(legacy, "oauth.json"), `{"access_token":"x"}`)
	if _, err := Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "config", "oauth", "anthropic.json")); err != nil {
		t.Fatalf("anthropic OAuth destination missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "config", "oauth", "default.json")); err == nil {
		t.Fatal("bare oauth.json migrated to generic default provider")
	}
}

func TestMigrateDestWinsOnConflict(t *testing.T) {
	legacy, dest := setupRoots(t)

	// Pre-existing dest file.
	destProviders := filepath.Join(dest, "config", "providers.json")
	writeFile(t, destProviders, `{"dest":"original"}`)

	// Legacy differs.
	writeFile(t, filepath.Join(legacy, "providers.json"), `{"legacy":"newer"}`)

	rep, err := Migrate()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got, _ := os.ReadFile(destProviders)
	if string(got) != `{"dest":"original"}` {
		t.Fatalf("dest was overwritten: %q", got)
	}
	if !contains(rep.Conflicts, "providers.json") {
		t.Fatalf("expected providers.json conflict, got %v", rep.Conflicts)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	legacy, _ := setupRoots(t)
	writeFile(t, filepath.Join(legacy, "providers.json"), `{"p":1}`)

	if _, err := Migrate(); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	rep2, err := Migrate()
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(rep2.Moved) != 0 || len(rep2.Conflicts) != 0 || len(rep2.Skipped) != 0 {
		t.Fatalf("second run not a no-op: %+v", rep2)
	}
}

func TestMigrateDuplicateProvidersPicksLarger(t *testing.T) {
	legacy, dest := setupRoots(t)

	// Root providers.json is small; config/providers.json is larger.
	writeFile(t, filepath.Join(legacy, "providers.json"), `{"a":1}`)
	writeFile(t, filepath.Join(legacy, "config", "providers.json"), `{"a":1,"b":2,"c":3,"bigger":true}`)

	if _, err := Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "config", "providers.json"))
	if err != nil {
		t.Fatalf("read migrated providers: %v", err)
	}
	if string(got) != `{"a":1,"b":2,"c":3,"bigger":true}` {
		t.Fatalf("did not pick larger providers.json, got %q", got)
	}
}

func TestMigrateSourceMissingNoOp(t *testing.T) {
	dest := t.TempDir()
	home := t.TempDir() // no .swarmos inside
	t.Setenv("SWARM_HOME", dest)
	orig := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = orig })

	rep, err := Migrate()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(rep.Moved)+len(rep.Skipped)+len(rep.Conflicts) != 0 {
		t.Fatalf("expected empty report, got %+v", rep)
	}
	// No marker should be written when source is absent.
	if _, err := os.Stat(filepath.Join(dest, "config", markerName)); err == nil {
		t.Fatal("marker written despite missing source")
	}
}

func TestRunNeverPanics(t *testing.T) {
	setupRoots(t)
	Run() // must not panic
}
