package projectmemory

import (
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sync"
)

// Database handles project memory storage
type Database struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewDatabase creates or opens the project memory database
func NewDatabase(projectDir string) (*Database, error) {
	swarmDir := filepath.Join(projectDir, ".swarm")
	if err := os.MkdirAll(swarmDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .swarm directory: %w", err)
	}

	dbPath := filepath.Join(swarmDir, "project_memory.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Initialize schema
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Database{db: db}, nil
}

func initSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS context_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		context_key TEXT UNIQUE NOT NULL,
		value TEXT NOT NULL,
		description TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_by TEXT
	);
	
	CREATE INDEX IF NOT EXISTS idx_context_key ON context_entries(context_key);
	CREATE INDEX IF NOT EXISTS idx_updated_at ON context_entries(updated_at);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to initialize schema: %w", err)
	}
	return nil
}

// ContextEntry represents a context entry
type ContextEntry struct {
	Key         string
	Value       string
	Description string
	UpdatedAt   string
	UpdatedBy   string
}

// GetEntry retrieves a context entry by key
func (d *Database) Entry(key string) (*ContextEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var entry ContextEntry
	err := d.db.QueryRow(`
		SELECT context_key, value, description, updated_at, updated_by 
		FROM context_entries 
		WHERE context_key = ?
	`, key).Scan(&entry.Key, &entry.Value, &entry.Description, &entry.UpdatedAt, &entry.UpdatedBy)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("context entry not found: %s", key)
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// ListEntries retrieves all context entries
func (d *Database) ListEntries() ([]ContextEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT context_key, value, description, updated_at, updated_by 
		FROM context_entries 
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ContextEntry
	for rows.Next() {
		var entry ContextEntry
		if err := rows.Scan(&entry.Key, &entry.Value, &entry.Description, &entry.UpdatedAt, &entry.UpdatedBy); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// SetEntry creates or updates a context entry
func (d *Database) SetEntry(key, value, description, updatedBy string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		INSERT INTO context_entries (context_key, value, description, updated_by, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(context_key) DO UPDATE SET 
			value = excluded.value,
			description = excluded.description,
			updated_by = excluded.updated_by,
			updated_at = CURRENT_TIMESTAMP
	`, key, value, description, updatedBy)
	return err
}

// DeleteEntry removes a context entry
func (d *Database) DeleteEntry(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	result, err := d.db.Exec("DELETE FROM context_entries WHERE context_key = ?", key)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("context entry not found: %s", key)
	}
	return nil
}

// Close closes the database connection
func (d *Database) Close() error {
	return d.db.Close()
}
