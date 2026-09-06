package a2a

import (
	"context"
	"fmt"
	"sync"
	"time"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
)

// SimpleBackend is an in-memory backend that uses WorkspaceHub for peer discovery.
// It is a drop-in replacement for SQLiteBackend with no persistence for tasks/bindings.
type SimpleBackend struct {
	hub       *WorkspaceHub
	workspace string
	handle    string
	sessionID string

	mu          sync.RWMutex
	peers       map[string]PeerIdentity // session_id → peer
	handleIndex map[string]string       // handle → session_id (O(1) lookup)
	leases      map[string]*PresenceLease
	tasks       map[string]*a2apb.Task // task_id → task (ephemeral)
	bindings    map[string]RemoteTaskBinding
	projections map[string]bool // projection_key → recorded
}

// NewSimpleBackend creates a new in-memory backend.
func NewSimpleBackend(hub *WorkspaceHub, workspace, handle, sessionID string) *SimpleBackend {
	return &SimpleBackend{
		hub:         hub,
		workspace:   workspace,
		handle:      handle,
		sessionID:   sessionID,
		peers:       make(map[string]PeerIdentity),
		handleIndex: make(map[string]string),
		leases:      make(map[string]*PresenceLease),
		tasks:       make(map[string]*a2apb.Task),
		bindings:    make(map[string]RemoteTaskBinding),
		projections: make(map[string]bool),
	}
}

// Close cleans up resources.
func (b *SimpleBackend) Close() error {
	return nil
}

// RegisterPeer registers a peer in memory, maintaining the handle index.
func (b *SimpleBackend) RegisterPeer(ctx context.Context, peer PeerIdentity, ttlSeconds int) (*PresenceLease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	peer.RegisteredAt = now
	peer.LastSeenAt = now
	b.peers[peer.SessionID] = peer
	b.handleIndex[peer.Handle] = peer.SessionID

	lease := &PresenceLease{
		SessionID: peer.SessionID,
		TTL:       time.Duration(ttlSeconds) * time.Second,
		RenewedAt: now,
		ExpiresAt: now.Add(time.Duration(ttlSeconds) * time.Second),
	}
	b.leases[peer.SessionID] = lease

	return lease, nil
}

// RenewPeer updates a peer's lease.
func (b *SimpleBackend) RenewPeer(ctx context.Context, sessionID string, ttlSeconds int) (*PresenceLease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	lease, ok := b.leases[sessionID]
	if !ok {
		return nil, fmt.Errorf("lease not found for session: %s", sessionID)
	}

	now := time.Now()
	lease.RenewedAt = now
	lease.ExpiresAt = now.Add(time.Duration(ttlSeconds) * time.Second)

	if peer, ok := b.peers[sessionID]; ok {
		peer.LastSeenAt = now
		b.peers[sessionID] = peer
	}

	return lease, nil
}

// UpdatePeerConversation updates a peer's conversation ID.
func (b *SimpleBackend) UpdatePeerConversation(ctx context.Context, sessionID, conversationID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	peer, ok := b.peers[sessionID]
	if !ok {
		return fmt.Errorf("peer not found: %s", sessionID)
	}

	peer.ConversationID = conversationID
	peer.LastSeenAt = time.Now()
	b.peers[sessionID] = peer

	return nil
}

// ListPeers returns peers that are both registered locally and live on the hub,
// filtered by scopeKey. The hub is the authoritative source for liveness; the
// local peer map provides full identity (endpoint, metadata, scopeKey).
func (b *SimpleBackend) ListPeers(ctx context.Context, scopeKey string) ([]PeerIdentity, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()

	// No hub: fall back to locally registered peers only (filtered by scopeKey).
	if b.hub == nil {
		var peers []PeerIdentity
		for sid, peer := range b.peers {
			if scopeKey != "" && peer.ScopeKey != scopeKey {
				continue
			}
			peer.LastSeenAt = now
			b.peers[sid] = peer
			peers = append(peers, peer)
		}
		return peers, nil
	}

	// Hub is the liveness source: only include peers currently connected to it.
	liveHandles := b.hub.GetPeers()
	peers := make([]PeerIdentity, 0, len(liveHandles))

	for _, handle := range liveHandles {
		sid, ok := b.handleIndex[handle]
		if !ok {
			// Peer is live on hub but not yet registered locally; skip.
			continue
		}
		peer, ok := b.peers[sid]
		if !ok {
			continue
		}
		if scopeKey != "" && peer.ScopeKey != scopeKey {
			continue
		}
		peer.LastSeenAt = now
		b.peers[sid] = peer // persist the updated timestamp
		peers = append(peers, peer)
	}

	return peers, nil
}

// GetPeer retrieves a peer by sessionID.
func (b *SimpleBackend) GetPeer(ctx context.Context, sessionID string) (*PeerIdentity, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	peer, ok := b.peers[sessionID]
	if !ok {
		return nil, fmt.Errorf("peer not found: %s", sessionID)
	}

	peer.LastSeenAt = time.Now()
	b.peers[sessionID] = peer
	return &peer, nil
}

// SaveTask stores a task (ephemeral — no persistence).
func (b *SimpleBackend) SaveTask(ctx context.Context, ownerSessionID string, task *a2apb.Task) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if task.GetId() == "" {
		return fmt.Errorf("task ID is required")
	}

	b.tasks[task.GetId()] = task
	return nil
}

// GetTask retrieves a task by ID.
func (b *SimpleBackend) GetTask(ctx context.Context, ownerSessionID, taskID string) (*a2apb.Task, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	return task, nil
}

// ListTasks returns tasks owned by ownerSessionID (with pagination).
// Tasks are ephemeral and scoped to their owner session.
func (b *SimpleBackend) ListTasks(ctx context.Context, ownerSessionID string, filter TaskListFilter) ([]*a2apb.Task, string, int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var tasks []*a2apb.Task
	for _, task := range b.tasks {
		// Filter by owner: match on context ID which embeds the session, or
		// fall back to including all tasks when ownerSessionID is empty.
		if ownerSessionID != "" && task.GetContextId() != ownerSessionID {
			continue
		}
		tasks = append(tasks, task)
	}

	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 10
	}

	if len(tasks) > pageSize {
		tasks = tasks[:pageSize]
	}

	return tasks, "", len(tasks), nil
}

// SaveRemoteBinding stores a remote binding.
func (b *SimpleBackend) SaveRemoteBinding(ctx context.Context, binding RemoteTaskBinding) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", binding.LocalSessionID, binding.RemoteEndpoint, binding.RemoteTaskID)
	b.bindings[key] = binding

	return nil
}

// GetRemoteBinding retrieves a remote binding.
func (b *SimpleBackend) GetRemoteBinding(ctx context.Context, localSessionID, remoteEndpoint, remoteTaskID string) (*RemoteTaskBinding, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	key := fmt.Sprintf("%s:%s:%s", localSessionID, remoteEndpoint, remoteTaskID)
	binding, ok := b.bindings[key]
	if !ok {
		return nil, fmt.Errorf("binding not found")
	}

	return &binding, nil
}

// LookupBindingByRemoteTaskID finds the RemoteTaskBinding for a remote task ID
// scoped to a local session, regardless of remote endpoint. Returns (nil, nil)
// when no binding exists (note: this differs from GetRemoteBinding which
// returns an error — the reply-correlation path treats "not found" as a normal
// outcome that falls back to peer-handle resolution).
func (b *SimpleBackend) LookupBindingByRemoteTaskID(ctx context.Context, localSessionID, remoteTaskID string) (*RemoteTaskBinding, error) {
	if localSessionID == "" || remoteTaskID == "" {
		return nil, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Linear scan — SimpleBackend is for tests/dev only. The SQLite backend
	// has the indexed query.
	suffix := ":" + remoteTaskID
	for key, binding := range b.bindings {
		if binding.LocalSessionID == localSessionID && len(key) > len(suffix) && key[len(key)-len(suffix):] == suffix {
			return &binding, nil
		}
	}
	return nil, nil
}

// RecordProjection tracks if a projection was already made (idempotency).
func (b *SimpleBackend) RecordProjection(ctx context.Context, sessionID, conversationID, projectionKey string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", sessionID, conversationID, projectionKey)

	if b.projections[key] {
		return false, nil // Already recorded
	}

	b.projections[key] = true
	return true, nil // First time
}

// Ensure SimpleBackend implements Backend interface.
var _ Backend = (*SimpleBackend)(nil)
