package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyticsBackfillDryRunCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runAnalyticsCLIWithWriters([]string{
		"backfill",
		"--dry-run",
		"--include", "conversations",
		"--limit", "1",
		"--json",
		"--conversations-dir", filepath.Join(home, "conversations"),
		"--swarm-dir", filepath.Join(home, ".swarm"),
		"--ledger", filepath.Join(home, "ledger.jsonl"),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runAnalyticsCLIWithWriters: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"dry_run": true`) {
		t.Fatalf("stdout missing dry_run JSON: %s", stdout.String())
	}
}
