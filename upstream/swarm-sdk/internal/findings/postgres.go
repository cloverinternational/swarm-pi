package findings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

// PostgresCache implements Cache using PostgreSQL with pgvector
type PostgresCache struct {
	db     *sql.DB
	config PostgresConfig
}

// PostgresConfig holds PostgreSQL connection configuration
type PostgresConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	SSLMode  string
}

// DefaultPostgresConfig returns sensible defaults
func DefaultPostgresConfig() PostgresConfig {
	return PostgresConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "swarm_findings",
		User:     "swarm",
		Password: "swarm",
		SSLMode:  "disable",
	}
}

// ConnectionString returns the PostgreSQL connection string
func (c PostgresConfig) ConnectionString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

// NewPostgresCache creates a new PostgreSQL-backed cache
func NewPostgresCache(config PostgresConfig) (*PostgresCache, error) {
	db, err := sql.Open("postgres", config.ConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	pc := &PostgresCache{
		db:     db,
		config: config,
	}

	// Ensure schema exists
	if err := pc.ensureSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	return pc, nil
}

// ensureSchema creates tables if they don't exist
func (pc *PostgresCache) ensureSchema(ctx context.Context) error {
	// Enable pgvector extension
	_, err := pc.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`)
	if err != nil {
		// Log but continue - might not have pgvector
		fmt.Printf("[findings] Note: pgvector extension not available: %v\n", err)
	}

	// Create findings table
	schema := `
CREATE TABLE IF NOT EXISTS findings (
    finding_id UUID PRIMARY KEY,
    tool_name TEXT NOT NULL,
    tool_input JSONB,
    tool_output JSONB,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    agent_id TEXT,
    conversation_id TEXT,
    context_summary TEXT,
    tags TEXT[],
    embedding VECTOR(384),  -- For semantic search (optional)
    parent_finding_id UUID,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX IF NOT EXISTS idx_findings_tool_name ON findings(tool_name);
CREATE INDEX IF NOT EXISTS idx_findings_agent_id ON findings(agent_id);
CREATE INDEX IF NOT EXISTS idx_findings_timestamp ON findings(timestamp);
CREATE INDEX IF NOT EXISTS idx_findings_tags ON findings USING GIN(tags);

-- Full-text search index
CREATE INDEX IF NOT EXISTS idx_findings_search ON findings 
    USING gin(to_tsvector('english', tool_name || ' ' || COALESCE(context_summary, '')));

-- Trigger for updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

DROP TRIGGER IF EXISTS update_findings_updated_at ON findings;
CREATE TRIGGER update_findings_updated_at
    BEFORE UPDATE ON findings
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
`

	_, err = pc.db.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// Write persists a finding to PostgreSQL
func (pc *PostgresCache) Write(ctx context.Context, finding Finding) error {
	if finding.FindingID == "" {
		finding.FindingID = uuid.New().String()
	}

	if finding.Timestamp.IsZero() {
		finding.Timestamp = time.Now()
	}

	// Serialize JSON fields
	toolInput, _ := json.Marshal(finding.ToolInput)
	toolOutput, _ := json.Marshal(finding.ToolOutput)
	metadata, _ := json.Marshal(finding.Metadata)

	// Handle embedding - convert to string for pgvector
	var embeddingVal any
	if len(finding.Embedding) > 0 {
		embeddingVal = vectorToString(finding.Embedding)
	} else {
		embeddingVal = nil
	}

	// Handle parent_finding_id - NULL if empty
	var parentFindingIDVal any
	if finding.ParentFindingID != "" {
		parentFindingIDVal = finding.ParentFindingID
	} else {
		parentFindingIDVal = nil
	}

	query := `
INSERT INTO findings (
    finding_id, tool_name, tool_input, tool_output, timestamp,
    agent_id, conversation_id, context_summary, tags, embedding,
    parent_finding_id, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (finding_id) DO UPDATE SET
    tool_name = EXCLUDED.tool_name,
    tool_input = EXCLUDED.tool_input,
    tool_output = EXCLUDED.tool_output,
    timestamp = EXCLUDED.timestamp,
    agent_id = EXCLUDED.agent_id,
    conversation_id = EXCLUDED.conversation_id,
    context_summary = EXCLUDED.context_summary,
    tags = EXCLUDED.tags,
    embedding = EXCLUDED.embedding,
    parent_finding_id = EXCLUDED.parent_finding_id,
    metadata = EXCLUDED.metadata
`

	_, err := pc.db.ExecContext(ctx, query,
		finding.FindingID,
		finding.ToolName,
		toolInput,
		toolOutput,
		finding.Timestamp,
		finding.AgentID,
		finding.ConversationID,
		finding.ContextSummary,
		pq.Array(finding.Tags),
		embeddingVal,
		parentFindingIDVal,
		metadata,
	)

	if err != nil {
		return fmt.Errorf("failed to insert finding: %w", err)
	}

	return nil
}

// Read retrieves a finding by ID
func (pc *PostgresCache) Read(ctx context.Context, findingID string) (Finding, error) {
	query := `
SELECT finding_id, tool_name, tool_input, tool_output, timestamp,
       agent_id, conversation_id, context_summary, tags, embedding,
       parent_finding_id, metadata
FROM findings
WHERE finding_id = $1
`

	row := pc.db.QueryRowContext(ctx, query, findingID)

	return pc.scanFinding(row)
}

// Query searches findings with filters
func (pc *PostgresCache) Query(ctx context.Context, query FindingQuery) ([]FindingResult, error) {
	// Build dynamic query
	sql := `
SELECT finding_id, tool_name, tool_input, tool_output, timestamp,
       agent_id, conversation_id, context_summary, tags, embedding,
       parent_finding_id, metadata,
       ts_rank(to_tsvector('english', tool_name || ' ' || COALESCE(context_summary, '')), 
               plainto_tsquery('english', $1)) as relevance
FROM findings
WHERE 1=1
`
	args := []any{query.TextQuery}
	argCount := 1

	// Add filters
	if query.ToolName != "" {
		argCount++
		sql += fmt.Sprintf(" AND tool_name = $%d", argCount)
		args = append(args, query.ToolName)
	}

	if query.AgentID != "" {
		argCount++
		sql += fmt.Sprintf(" AND agent_id = $%d", argCount)
		args = append(args, query.AgentID)
	}

	if len(query.Tags) > 0 {
		argCount++
		sql += fmt.Sprintf(" AND tags && $%d", argCount)
		args = append(args, pq.Array(query.Tags))
	}

	if query.TimeRange != nil {
		argCount++
		sql += fmt.Sprintf(" AND timestamp BETWEEN $%d AND $%d", argCount, argCount+1)
		args = append(args, query.TimeRange.Start, query.TimeRange.End)
		argCount += 2
	}

	// Full-text search
	if query.TextQuery != "" {
		sql += ` AND to_tsvector('english', tool_name || ' ' || COALESCE(context_summary, '')) 
                 @@ plainto_tsquery('english', $1)`
	}

	// Order by relevance or timestamp
	if query.TextQuery != "" {
		sql += " ORDER BY relevance DESC, timestamp DESC"
	} else {
		sql += " ORDER BY timestamp DESC"
	}

	// Limit
	if query.Limit > 0 {
		argCount++
		sql += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, query.Limit)
	}

	// Offset
	if query.Offset > 0 {
		argCount++
		sql += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, query.Offset)
	}

	rows, err := pc.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query findings: %w", err)
	}
	defer rows.Close()

	var results []FindingResult
	for rows.Next() {
		f, score, err := pc.scanFindingWithScore(rows)
		if err != nil {
			continue // Skip problematic rows
		}

		results = append(results, FindingResult{
			Finding:   f,
			Score:     score,
			MatchType: "database",
		})
	}

	return results, nil
}

// List returns all findings with pagination
func (pc *PostgresCache) List(ctx context.Context, limit, offset int) ([]Finding, error) {
	query := `
SELECT finding_id, tool_name, tool_input, tool_output, timestamp,
       agent_id, conversation_id, context_summary, tags, embedding,
       parent_finding_id, metadata
FROM findings
ORDER BY timestamp DESC
LIMIT $1 OFFSET $2
`

	rows, err := pc.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list findings: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		f, err := pc.scanFinding(rows)
		if err != nil {
			continue
		}
		findings = append(findings, f)
	}

	return findings, nil
}

// Delete removes a finding
func (pc *PostgresCache) Delete(ctx context.Context, findingID string) error {
	query := `DELETE FROM findings WHERE finding_id = $1`
	_, err := pc.db.ExecContext(ctx, query, findingID)
	return err
}

// Close releases resources
func (pc *PostgresCache) Close() error {
	return pc.db.Close()
}

// Stats returns database statistics
func (pc *PostgresCache) Stats(ctx context.Context) (map[string]any, error) {
	var count int
	err := pc.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM findings").Scan(&count)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"total_findings": count,
		"database":       pc.config.Database,
		"host":           pc.config.Host,
	}, nil
}

// scanFinding scans a single finding from a row
func (pc *PostgresCache) scanFinding(scanner interface {
	Scan(dest ...any) error
}) (Finding, error) {
	var f Finding
	var toolInput, toolOutput, metadata []byte
	var embedding any

	err := scanner.Scan(
		&f.FindingID,
		&f.ToolName,
		&toolInput,
		&toolOutput,
		&f.Timestamp,
		&f.AgentID,
		&f.ConversationID,
		&f.ContextSummary,
		pq.Array(&f.Tags),
		&embedding,
		&f.ParentFindingID,
		&metadata,
	)
	if err != nil {
		return f, err
	}

	// Deserialize JSON
	if len(toolInput) > 0 {
		json.Unmarshal(toolInput, &f.ToolInput)
	}
	if len(toolOutput) > 0 {
		json.Unmarshal(toolOutput, &f.ToolOutput)
	}
	if len(metadata) > 0 {
		json.Unmarshal(metadata, &f.Metadata)
	}

	return f, nil
}

// scanFindingWithScore scans a finding with relevance score
func (pc *PostgresCache) scanFindingWithScore(rows *sql.Rows) (Finding, float64, error) {
	var f Finding
	var toolInput, toolOutput, metadata []byte
	var embedding any
	var score float64

	err := rows.Scan(
		&f.FindingID,
		&f.ToolName,
		&toolInput,
		&toolOutput,
		&f.Timestamp,
		&f.AgentID,
		&f.ConversationID,
		&f.ContextSummary,
		pq.Array(&f.Tags),
		&embedding,
		&f.ParentFindingID,
		&metadata,
		&score,
	)
	if err != nil {
		return f, 0, err
	}

	// Deserialize JSON
	if len(toolInput) > 0 {
		json.Unmarshal(toolInput, &f.ToolInput)
	}
	if len(toolOutput) > 0 {
		json.Unmarshal(toolOutput, &f.ToolOutput)
	}
	if len(metadata) > 0 {
		json.Unmarshal(metadata, &f.Metadata)
	}

	return f, score, nil
}

// vectorToString converts float slice to pgvector format
func vectorToString(v []float32) string {
	return fmt.Sprintf("%v", v)
}
