package a2a

import (
	"context"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
)

// Backend persists local peer discovery, task state, bindings, and projection receipts.
type Backend interface {
	Close() error

	RegisterPeer(ctx context.Context, peer PeerIdentity, ttlSeconds int) (*PresenceLease, error)
	RenewPeer(ctx context.Context, sessionID string, ttlSeconds int) (*PresenceLease, error)
	UpdatePeerConversation(ctx context.Context, sessionID, conversationID string) error
	ListPeers(ctx context.Context, scopeKey string) ([]PeerIdentity, error)
	GetPeer(ctx context.Context, sessionID string) (*PeerIdentity, error)

	SaveTask(ctx context.Context, ownerSessionID string, task *a2apb.Task) error
	GetTask(ctx context.Context, ownerSessionID, taskID string) (*a2apb.Task, error)
	ListTasks(ctx context.Context, ownerSessionID string, filter TaskListFilter) ([]*a2apb.Task, string, int, error)

	SaveRemoteBinding(ctx context.Context, binding RemoteTaskBinding) error
	GetRemoteBinding(ctx context.Context, localSessionID, remoteEndpoint, remoteTaskID string) (*RemoteTaskBinding, error)
	// LookupBindingByRemoteTaskID finds the RemoteTaskBinding for a given remote task ID
	// scoped to a local session, regardless of which remote endpoint it was sent to.
	// This is used by the inbound-reply path: when a peer sends a reply with
	// referenceTaskIds=[X], we want to find which local conversation X belongs to.
	// Returns (nil, nil) when no binding exists.
	LookupBindingByRemoteTaskID(ctx context.Context, localSessionID, remoteTaskID string) (*RemoteTaskBinding, error)

	RecordProjection(ctx context.Context, sessionID, conversationID, projectionKey string) (bool, error)
}
