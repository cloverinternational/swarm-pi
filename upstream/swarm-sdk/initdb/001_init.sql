-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Create findings table
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
    embedding VECTOR(768),  -- Nomic-embed dimension
    parent_finding_id UUID,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX IF NOT EXISTS idx_findings_tool_name ON findings(tool_name);
CREATE INDEX IF NOT EXISTS idx_findings_agent_id ON findings(agent_id);
CREATE INDEX IF NOT EXISTS idx_findings_timestamp ON findings(timestamp DESC);
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
