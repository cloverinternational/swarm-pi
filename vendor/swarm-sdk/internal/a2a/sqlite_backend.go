package a2a

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

// SQLiteBackend persists local A2A discovery, task state, and projection receipts.
type SQLiteBackend struct {
	db *sql.DB
}

// SQLiteConfig configures the local A2A registry/task database.
type SQLiteConfig struct {
	Path string
}

const (
	sqliteBusyTimeoutMS        = 10000
	sqliteSchemaInitTimeout    = 10 * time.Second
	sqliteSchemaInitRetryDelay = 100 * time.Millisecond
)

// NewSQLiteBackend creates a SQLite/WAL backend for local A2A state.
func NewSQLiteBackend(cfg SQLiteConfig) (*SQLiteBackend, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, fmt.Errorf("a2a sqlite path is required")
	}
	dbPath := filepath.Clean(cfg.Path)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create a2a sqlite dir: %w", err)
	}

	dsn, err := sqliteDSN(dbPath)
	if err != nil {
		return nil, fmt.Errorf("build a2a sqlite dsn: %w", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open a2a sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := initSQLiteSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteBackend{db: db}, nil
}

func sqliteDSN(path string) (string, error) {
	query := url.Values{}
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMS))
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Set("_txlock", "immediate")
	return (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: query.Encode(),
	}).String(), nil
}

func initSQLiteSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS a2a_peers (
	session_id TEXT PRIMARY KEY,
	handle TEXT NOT NULL,
	conversation_id TEXT,
	workspace_path TEXT,
	project_id TEXT,
	scope_key TEXT NOT NULL,
	endpoint_url TEXT NOT NULL,
	card_url TEXT NOT NULL,
	metadata_json TEXT,
	registered_at INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL,
	ttl_seconds INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_a2a_peers_scope ON a2a_peers(scope_key, last_seen_at);

CREATE TABLE IF NOT EXISTS a2a_tasks (
	owner_session_id TEXT NOT NULL,
	task_id TEXT NOT NULL,
	context_id TEXT,
	status_state INTEGER NOT NULL,
	status_updated_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	task_json TEXT NOT NULL,
	PRIMARY KEY (owner_session_id, task_id)
);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_owner_updated ON a2a_tasks(owner_session_id, updated_at DESC, task_id DESC);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_owner_context ON a2a_tasks(owner_session_id, context_id);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_owner_status ON a2a_tasks(owner_session_id, status_state);

CREATE TABLE IF NOT EXISTS a2a_remote_bindings (
	local_session_id TEXT NOT NULL,
	conversation_id TEXT NOT NULL,
	remote_endpoint TEXT NOT NULL,
	remote_task_id TEXT NOT NULL,
	remote_context_id TEXT,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (local_session_id, remote_endpoint, remote_task_id)
);

CREATE TABLE IF NOT EXISTS a2a_projection_receipts (
	session_id TEXT NOT NULL,
	conversation_id TEXT NOT NULL,
	projection_key TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (session_id, conversation_id, projection_key)
);`

	deadline := time.Now().Add(sqliteSchemaInitTimeout)
	for {
		if _, err := db.Exec(schema); err != nil {
			if isSQLiteBusyError(err) && time.Now().Before(deadline) {
				time.Sleep(sqliteSchemaInitRetryDelay)
				continue
			}
			return fmt.Errorf("init a2a schema: %w", err)
		}
		return nil
	}
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "SQLITE_LOCKED") ||
		strings.Contains(msg, "DATABASE IS LOCKED")
}

func (b *SQLiteBackend) Close() error { return b.db.Close() }

func (b *SQLiteBackend) RegisterPeer(ctx context.Context, peer PeerIdentity, ttlSeconds int) (*PresenceLease, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 15
	}
	now := time.Now().UTC()
	peer.SessionID = defaultSessionID(peer.SessionID)
	peer.Handle = NormalizeHandle(peer.Handle)
	peer.ScopeKey = NormalizeScopeKey(peer.ProjectID, peer.WorkspacePath)
	if peer.ScopeKey == "" {
		return nil, fmt.Errorf("peer scope key is required")
	}
	if peer.Handle == "" {
		return nil, fmt.Errorf("peer handle is required")
	}
	if strings.TrimSpace(peer.EndpointURL) == "" {
		return nil, fmt.Errorf("peer endpoint url is required")
	}
	if strings.TrimSpace(peer.CardURL) == "" {
		return nil, fmt.Errorf("peer card url is required")
	}

	if _, err := b.db.ExecContext(ctx, `
INSERT INTO a2a_peers (
	session_id, handle, conversation_id, workspace_path, project_id, scope_key,
	endpoint_url, card_url, metadata_json, registered_at, last_seen_at, ttl_seconds
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
	handle = excluded.handle,
	conversation_id = excluded.conversation_id,
	workspace_path = excluded.workspace_path,
	project_id = excluded.project_id,
	scope_key = excluded.scope_key,
	endpoint_url = excluded.endpoint_url,
	card_url = excluded.card_url,
	metadata_json = excluded.metadata_json,
	last_seen_at = excluded.last_seen_at,
	ttl_seconds = excluded.ttl_seconds`,
		peer.SessionID,
		peer.Handle,
		nullIfEmpty(peer.ConversationID),
		nullIfEmpty(peer.WorkspacePath),
		nullIfEmpty(peer.ProjectID),
		peer.ScopeKey,
		peer.EndpointURL,
		peer.CardURL,
		mustJSON(peer.Metadata),
		now.Unix(),
		now.Unix(),
		ttlSeconds,
	); err != nil {
		return nil, fmt.Errorf("register a2a peer: %w", err)
	}

	return &PresenceLease{
		SessionID: peer.SessionID,
		ScopeKey:  peer.ScopeKey,
		TTL:       time.Duration(ttlSeconds) * time.Second,
		RenewedAt: now,
		ExpiresAt: now.Add(time.Duration(ttlSeconds) * time.Second),
	}, nil
}

func (b *SQLiteBackend) RenewPeer(ctx context.Context, sessionID string, ttlSeconds int) (*PresenceLease, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 15
	}
	now := time.Now().UTC()
	result, err := b.db.ExecContext(ctx, `
UPDATE a2a_peers SET last_seen_at = ?, ttl_seconds = ? WHERE session_id = ?`,
		now.Unix(),
		ttlSeconds,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("renew a2a peer: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("renew a2a peer rows: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("peer %q not found", sessionID)
	}
	scopeKey := ""
	if err := b.db.QueryRowContext(ctx, `SELECT scope_key FROM a2a_peers WHERE session_id = ?`, sessionID).Scan(&scopeKey); err != nil {
		return nil, fmt.Errorf("load a2a peer scope: %w", err)
	}
	return &PresenceLease{
		SessionID: sessionID,
		ScopeKey:  scopeKey,
		TTL:       time.Duration(ttlSeconds) * time.Second,
		RenewedAt: now,
		ExpiresAt: now.Add(time.Duration(ttlSeconds) * time.Second),
	}, nil
}

func (b *SQLiteBackend) UpdatePeerConversation(ctx context.Context, sessionID, conversationID string) error {
	if _, err := b.db.ExecContext(ctx, `UPDATE a2a_peers SET conversation_id = ? WHERE session_id = ?`, nullIfEmpty(conversationID), sessionID); err != nil {
		return fmt.Errorf("update a2a peer conversation: %w", err)
	}
	return nil
}

func (b *SQLiteBackend) ListPeers(ctx context.Context, scopeKey string) ([]PeerIdentity, error) {
	now := time.Now().UTC().Unix()
	rows, err := b.db.QueryContext(ctx, `
SELECT handle, session_id, COALESCE(conversation_id, ''), COALESCE(workspace_path, ''), COALESCE(project_id, ''),
	scope_key, endpoint_url, card_url, COALESCE(metadata_json, '{}'),
	registered_at, last_seen_at, ttl_seconds
FROM a2a_peers
WHERE scope_key = ? AND (last_seen_at + ttl_seconds) > ?
ORDER BY handle ASC, session_id ASC`,
		scopeKey,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("list a2a peers: %w", err)
	}
	defer rows.Close()

	peers := make([]PeerIdentity, 0)
	for rows.Next() {
		var (
			peer         PeerIdentity
			metadataJSON string
			registeredAt int64
			lastSeenAt   int64
			ttlSeconds   int64
		)
		if err := rows.Scan(
			&peer.Handle,
			&peer.SessionID,
			&peer.ConversationID,
			&peer.WorkspacePath,
			&peer.ProjectID,
			&peer.ScopeKey,
			&peer.EndpointURL,
			&peer.CardURL,
			&metadataJSON,
			&registeredAt,
			&lastSeenAt,
			&ttlSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan a2a peer: %w", err)
		}
		peer.Metadata = parseMap(metadataJSON)
		peer.RegisteredAt = time.Unix(registeredAt, 0).UTC()
		peer.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
		peer.ExpiresAt = peer.LastSeenAt.Add(time.Duration(ttlSeconds) * time.Second)
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate a2a peers: %w", err)
	}
	return peers, nil
}

func (b *SQLiteBackend) GetPeer(ctx context.Context, sessionID string) (*PeerIdentity, error) {
	row := b.db.QueryRowContext(ctx, `
SELECT handle, session_id, COALESCE(conversation_id, ''), COALESCE(workspace_path, ''), COALESCE(project_id, ''),
	scope_key, endpoint_url, card_url, COALESCE(metadata_json, '{}'),
	registered_at, last_seen_at, ttl_seconds
FROM a2a_peers WHERE session_id = ?`,
		sessionID,
	)
	var (
		peer         PeerIdentity
		metadataJSON string
		registeredAt int64
		lastSeenAt   int64
		ttlSeconds   int64
	)
	if err := row.Scan(
		&peer.Handle,
		&peer.SessionID,
		&peer.ConversationID,
		&peer.WorkspacePath,
		&peer.ProjectID,
		&peer.ScopeKey,
		&peer.EndpointURL,
		&peer.CardURL,
		&metadataJSON,
		&registeredAt,
		&lastSeenAt,
		&ttlSeconds,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get a2a peer: %w", err)
	}
	peer.Metadata = parseMap(metadataJSON)
	peer.RegisteredAt = time.Unix(registeredAt, 0).UTC()
	peer.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
	peer.ExpiresAt = peer.LastSeenAt.Add(time.Duration(ttlSeconds) * time.Second)
	return &peer, nil
}

func (b *SQLiteBackend) SaveTask(ctx context.Context, ownerSessionID string, task *a2apb.Task) error {
	if strings.TrimSpace(ownerSessionID) == "" {
		return fmt.Errorf("task owner session is required")
	}
	if task == nil || strings.TrimSpace(task.GetId()) == "" {
		return fmt.Errorf("task id is required")
	}
	now := time.Now().UTC()
	createdAt := now
	row := b.db.QueryRowContext(ctx, `SELECT created_at FROM a2a_tasks WHERE owner_session_id = ? AND task_id = ?`, ownerSessionID, task.GetId())
	var createdUnix int64
	switch err := row.Scan(&createdUnix); err {
	case nil:
		createdAt = time.Unix(createdUnix, 0).UTC()
	case sql.ErrNoRows:
	default:
		return fmt.Errorf("load existing task timestamp: %w", err)
	}
	statusUpdatedAt := now
	if ts := task.GetStatus().GetTimestamp(); ts != nil {
		statusUpdatedAt = ts.AsTime().UTC()
	}
	payload, err := protojson.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	if _, err := b.db.ExecContext(ctx, `
INSERT INTO a2a_tasks (
	owner_session_id, task_id, context_id, status_state, status_updated_at, created_at, updated_at, task_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(owner_session_id, task_id) DO UPDATE SET
	context_id = excluded.context_id,
	status_state = excluded.status_state,
	status_updated_at = excluded.status_updated_at,
	updated_at = excluded.updated_at,
	task_json = excluded.task_json`,
		ownerSessionID,
		task.GetId(),
		nullIfEmpty(task.GetContextId()),
		int32(task.GetStatus().GetState()),
		statusUpdatedAt.Unix(),
		createdAt.Unix(),
		now.Unix(),
		string(payload),
	); err != nil {
		return fmt.Errorf("save task: %w", err)
	}
	return nil
}

func (b *SQLiteBackend) GetTask(ctx context.Context, ownerSessionID, taskID string) (*a2apb.Task, error) {
	var payload string
	if err := b.db.QueryRowContext(ctx, `
SELECT task_json FROM a2a_tasks WHERE owner_session_id = ? AND task_id = ?`,
		ownerSessionID,
		taskID,
	).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get task: %w", err)
	}
	task := &a2apb.Task{}
	if err := protojson.Unmarshal([]byte(payload), task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return task, nil
}

func (b *SQLiteBackend) ListTasks(ctx context.Context, ownerSessionID string, filter TaskListFilter) ([]*a2apb.Task, string, int, error) {
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := 0
	if strings.TrimSpace(filter.PageToken) != "" {
		parsed, err := strconv.Atoi(filter.PageToken)
		if err != nil {
			return nil, "", 0, fmt.Errorf("invalid page token: %w", err)
		}
		offset = parsed
	}

	where := []string{"owner_session_id = ?"}
	args := []any{ownerSessionID}
	if trimmed := strings.TrimSpace(filter.ContextID); trimmed != "" {
		where = append(where, "context_id = ?")
		args = append(args, trimmed)
	}
	if filter.Status != a2apb.TaskState_TASK_STATE_UNSPECIFIED {
		where = append(where, "status_state = ?")
		args = append(args, int32(filter.Status))
	}
	if filter.StatusTimestampAfter != nil {
		where = append(where, "status_updated_at >= ?")
		args = append(args, filter.StatusTimestampAfter.UTC().Unix())
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM a2a_tasks WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, "", 0, fmt.Errorf("count tasks: %w", err)
	}

	queryArgs := append(append([]any(nil), args...), pageSize, offset)
	rows, err := b.db.QueryContext(ctx, `
SELECT task_json FROM a2a_tasks
WHERE `+whereSQL+`
ORDER BY updated_at DESC, task_id DESC
LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, "", 0, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]*a2apb.Task, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, "", 0, fmt.Errorf("scan task payload: %w", err)
		}
		task := &a2apb.Task{}
		if err := protojson.Unmarshal([]byte(payload), task); err != nil {
			return nil, "", 0, fmt.Errorf("unmarshal listed task: %w", err)
		}
		task = trimTask(task, filter.HistoryLength, filter.IncludeArtifacts)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, fmt.Errorf("iterate tasks: %w", err)
	}

	nextPageToken := ""
	if offset+len(tasks) < total {
		nextPageToken = strconv.Itoa(offset + len(tasks))
	}
	return tasks, nextPageToken, total, nil
}

func (b *SQLiteBackend) SaveRemoteBinding(ctx context.Context, binding RemoteTaskBinding) error {
	if strings.TrimSpace(binding.LocalSessionID) == "" {
		return fmt.Errorf("binding local session is required")
	}
	if strings.TrimSpace(binding.ConversationID) == "" {
		return fmt.Errorf("binding conversation id is required")
	}
	if strings.TrimSpace(binding.RemoteEndpoint) == "" {
		return fmt.Errorf("binding remote endpoint is required")
	}
	if strings.TrimSpace(binding.RemoteTaskID) == "" {
		return fmt.Errorf("binding remote task id is required")
	}
	now := time.Now().UTC()
	if _, err := b.db.ExecContext(ctx, `
INSERT INTO a2a_remote_bindings (
	local_session_id, conversation_id, remote_endpoint, remote_task_id, remote_context_id, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(local_session_id, remote_endpoint, remote_task_id) DO UPDATE SET
	conversation_id = excluded.conversation_id,
	remote_context_id = excluded.remote_context_id,
	updated_at = excluded.updated_at`,
		binding.LocalSessionID,
		binding.ConversationID,
		binding.RemoteEndpoint,
		binding.RemoteTaskID,
		nullIfEmpty(binding.RemoteContextID),
		now.Unix(),
	); err != nil {
		return fmt.Errorf("save remote binding: %w", err)
	}
	return nil
}

func (b *SQLiteBackend) GetRemoteBinding(ctx context.Context, localSessionID, remoteEndpoint, remoteTaskID string) (*RemoteTaskBinding, error) {
	row := b.db.QueryRowContext(ctx, `
SELECT local_session_id, conversation_id, remote_endpoint, remote_task_id, COALESCE(remote_context_id, ''), updated_at
FROM a2a_remote_bindings WHERE local_session_id = ? AND remote_endpoint = ? AND remote_task_id = ?`,
		localSessionID,
		remoteEndpoint,
		remoteTaskID,
	)
	var binding RemoteTaskBinding
	var updatedAt int64
	if err := row.Scan(
		&binding.LocalSessionID,
		&binding.ConversationID,
		&binding.RemoteEndpoint,
		&binding.RemoteTaskID,
		&binding.RemoteContextID,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get remote binding: %w", err)
	}
	binding.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &binding, nil
}

// LookupBindingByRemoteTaskID finds the RemoteTaskBinding for a remote task ID
// scoped to a local session, ignoring which remote endpoint it was sent to.
// Used by the reply-correlation path: when an inbound A2A message arrives with
// referenceTaskIds=[X], we look up which local conversation owns task X.
//
// If multiple bindings share the same (local_session_id, remote_task_id) we
// return the most-recently-updated one — this can happen if the same task ID
// was reused across peers, but should be rare in practice because task IDs
// are UUIDs.
func (b *SQLiteBackend) LookupBindingByRemoteTaskID(ctx context.Context, localSessionID, remoteTaskID string) (*RemoteTaskBinding, error) {
	if strings.TrimSpace(localSessionID) == "" || strings.TrimSpace(remoteTaskID) == "" {
		return nil, nil
	}
	row := b.db.QueryRowContext(ctx, `
SELECT local_session_id, conversation_id, remote_endpoint, remote_task_id, COALESCE(remote_context_id, ''), updated_at
FROM a2a_remote_bindings
WHERE local_session_id = ? AND remote_task_id = ?
ORDER BY updated_at DESC
LIMIT 1`,
		localSessionID,
		remoteTaskID,
	)
	var binding RemoteTaskBinding
	var updatedAt int64
	if err := row.Scan(
		&binding.LocalSessionID,
		&binding.ConversationID,
		&binding.RemoteEndpoint,
		&binding.RemoteTaskID,
		&binding.RemoteContextID,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup binding by remote task id: %w", err)
	}
	binding.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &binding, nil
}

func (b *SQLiteBackend) RecordProjection(ctx context.Context, sessionID, conversationID, projectionKey string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(projectionKey) == "" {
		return false, fmt.Errorf("projection receipt requires session, conversation, and key")
	}
	result, err := b.db.ExecContext(ctx, `
INSERT OR IGNORE INTO a2a_projection_receipts (
	session_id, conversation_id, projection_key, created_at
) VALUES (?, ?, ?, ?)`,
		sessionID,
		conversationID,
		projectionKey,
		time.Now().UTC().Unix(),
	)
	if err != nil {
		return false, fmt.Errorf("record projection receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("projection receipt rows: %w", err)
	}
	return rows > 0, nil
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func mustJSON(value any) string {
	if value == nil {
		return "{}"
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func parseMap(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := make(map[string]any)
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func trimTask(task *a2apb.Task, historyLength *int32, includeArtifacts bool) *a2apb.Task {
	if task == nil {
		return nil
	}
	clone := proto.Clone(task).(*a2apb.Task)
	if historyLength != nil {
		limit := int(*historyLength)
		switch {
		case limit <= 0:
			clone.History = nil
		case len(clone.History) > limit:
			clone.History = append([]*a2apb.Message(nil), clone.History[len(clone.History)-limit:]...)
		}
	}
	if !includeArtifacts {
		clone.Artifacts = nil
	}
	if clone.Status != nil && clone.Status.Timestamp == nil {
		clone.Status.Timestamp = timestamppb.New(time.Now().UTC())
	}
	return clone
}
