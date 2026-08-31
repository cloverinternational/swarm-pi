package backfill

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ledgerEntry struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Source      string    `json:"source,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	EnqueuedAt  time.Time `json:"enqueued_at"`
}

type ledger struct {
	path    string
	entries map[string]ledgerEntry
	stats   LedgerStats
}

func loadLedger(path string) (*ledger, error) {
	l := &ledger{
		path:    path,
		entries: make(map[string]ledgerEntry),
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry ledgerEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.ID == "" {
			continue
		}
		l.entries[entry.ID] = entry
		l.stats.Entries++
		switch entry.Kind {
		case "event":
			l.stats.Events++
		case "artifact":
			l.stats.Artifacts++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *ledger) has(id string) bool {
	if l == nil || id == "" {
		return false
	}
	_, ok := l.entries[id]
	return ok
}

func (l *ledger) append(entry ledgerEntry) error {
	if l == nil || entry.ID == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("create ledger dir: %w", err)
	}
	if entry.EnqueuedAt.IsZero() {
		entry.EnqueuedAt = time.Now().UTC()
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal ledger entry: %w", err)
	}
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write ledger: %w", err)
	}
	l.entries[entry.ID] = entry
	l.stats.Entries++
	switch entry.Kind {
	case "event":
		l.stats.Events++
	case "artifact":
		l.stats.Artifacts++
	}
	return nil
}

func (l *ledger) Stats() LedgerStats {
	if l == nil {
		return LedgerStats{}
	}
	return l.stats
}
