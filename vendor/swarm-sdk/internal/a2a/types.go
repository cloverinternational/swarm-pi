package a2a

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/google/uuid"
)

const (
	// ProtocolVersion is the A2A version exposed by the phase-1 Swarm transport.
	ProtocolVersion = "1.0"

	// JSONRPCBinding is the only binding implemented in phase 1.
	JSONRPCBinding = "JSONRPC"

	// DefaultRPCPath is the JSON-RPC endpoint published in the agent card.
	DefaultRPCPath = "/rpc"

	// AgentCardPath is the required well-known discovery endpoint.
	AgentCardPath = "/.well-known/agent-card.json"

	// MessagePersistedMetadataKey marks projected peer messages already persisted by a sink.
	MessagePersistedMetadataKey = "a2a_persisted"

	// Swarm metadata keys piggyback on A2A metadata for local peer identity.
	MetadataSessionIDKey      = "swarm.session_id"
	MetadataHandleKey         = "swarm.handle"
	MetadataConversationIDKey = "swarm.conversation_id"

	// SwarmTypeKey is the metadata key whose value identifies the chatroom
	// operation that produced the message ("dm", "broadcast", or "" for raw RPC).
	// The recipient's runtime branches on this to pick the right inbound
	// handling path:
	//   - "dm":        async path (spawnAsyncTask) + reverse-DM reply
	//   - "broadcast": project-only, no agent execution
	//   - "":          default sync RPC behaviour (back-compat)
	SwarmTypeKey = "swarm_type"

	// Recognised values for SwarmTypeKey.
	SwarmTypeDM        = "dm"
	SwarmTypeBroadcast = "broadcast"

	// GlobalRegistryFilename is the filename for the shared A2A peer registry.
	// All peers in all workspaces use the same registry file for discovery.
	// Workspace separation is handled via scope_key filtering inside the registry.
	GlobalRegistryFilename = "a2a-registry.sqlite"
)

// GlobalRegistryPath returns the single global path for the A2A peer registry.
// All peers register here regardless of workspace. Workspace separation is
// handled via scope_key filtering in the database queries.
func GlobalRegistryPath() string {
	// Single canonical location under the unified ~/.swarm root.
	return paths.A2ARegistryFile()
}

// HookEmitter is the subset of hook manager functionality needed by the A2A runtime.
type HookEmitter interface {
	Emit(ctx context.Context, event hooks.Event) (*hooks.Event, error)
}

// ProjectionSink persists projected A2A traffic into the host conversation store.
type ProjectionSink func(ctx context.Context, msg *conversation.Message) (bool, error)

// RequestHandler produces an agent-authored reply for an inbound A2A request.
type RequestHandler func(ctx context.Context, req *InboundRequest) (*conversation.Message, error)

// Skill describes a published high-level A2A skill.
type Skill struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	Examples    []string
	InputModes  []string
	OutputModes []string
}

// AgentDescriptor is the cycle-free subset of agent definition data used to build cards.
type AgentDescriptor struct {
	ID                 string
	Name               string
	Description        string
	ProviderName       string
	ProviderURL        string
	Version            string
	ToolHints          []string
	DefaultInputModes  []string
	DefaultOutputModes []string
	SupportsStreaming  bool
	Skills             []Skill
	DocumentationURL   string
	IconURL            string
	Metadata           map[string]any
}

// CardOverrides lets hosts customize the generated A2A agent card.
type CardOverrides struct {
	Name                 string
	Description          string
	Version              string
	DocumentationURL     string
	IconURL              string
	DefaultInputModes    []string
	DefaultOutputModes   []string
	Skills               []Skill
	SecuritySchemes      map[string]*a2apb.SecurityScheme
	SecurityRequirements []*a2apb.SecurityRequirement
	Extensions           []*a2apb.AgentExtension
}

// Config configures A2A runtime behavior for a top-level agent session.
type Config struct {
	Handle         string
	SessionID      string
	WorkspacePath  string
	ProjectID      string
	ConversationID string
	// RegistryPath is deprecated - all peers use GlobalRegistryPath() automatically.
	// Workspace separation is handled via scope_key filtering in the database.
	RegistryPath string // Deprecated: ignored, uses GlobalRegistryPath()
	// SwarmName is the swarm to join for peer discovery (default: "default").
	// Peers in the same swarm can see each other.
	SwarmName     string
	ListenAddress string
	BaseURL       string
	RPCPath       string
	PresenceTTL   time.Duration
	Metadata      map[string]any
	Hooks         HookEmitter
	Descriptor    AgentDescriptor
	CardOverrides CardOverrides
}

// Clone returns a deep copy of the config.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	clone := *c
	clone.Metadata = cloneMap(c.Metadata)
	clone.Descriptor.Metadata = cloneMap(c.Descriptor.Metadata)
	clone.Descriptor.ToolHints = append([]string(nil), c.Descriptor.ToolHints...)
	clone.Descriptor.DefaultInputModes = append([]string(nil), c.Descriptor.DefaultInputModes...)
	clone.Descriptor.DefaultOutputModes = append([]string(nil), c.Descriptor.DefaultOutputModes...)
	if len(c.Descriptor.Skills) > 0 {
		clone.Descriptor.Skills = make([]Skill, len(c.Descriptor.Skills))
		copy(clone.Descriptor.Skills, c.Descriptor.Skills)
	}
	clone.CardOverrides.DefaultInputModes = append([]string(nil), c.CardOverrides.DefaultInputModes...)
	clone.CardOverrides.DefaultOutputModes = append([]string(nil), c.CardOverrides.DefaultOutputModes...)
	if len(c.CardOverrides.Skills) > 0 {
		clone.CardOverrides.Skills = make([]Skill, len(c.CardOverrides.Skills))
		copy(clone.CardOverrides.Skills, c.CardOverrides.Skills)
	}
	if len(c.CardOverrides.SecuritySchemes) > 0 {
		clone.CardOverrides.SecuritySchemes = make(map[string]*a2apb.SecurityScheme, len(c.CardOverrides.SecuritySchemes))
		maps.Copy(clone.CardOverrides.SecuritySchemes, c.CardOverrides.SecuritySchemes)
	}
	if len(c.CardOverrides.SecurityRequirements) > 0 {
		clone.CardOverrides.SecurityRequirements = append([]*a2apb.SecurityRequirement(nil), c.CardOverrides.SecurityRequirements...)
	}
	if len(c.CardOverrides.Extensions) > 0 {
		clone.CardOverrides.Extensions = append([]*a2apb.AgentExtension(nil), c.CardOverrides.Extensions...)
	}
	return &clone
}

// PeerIdentity identifies a top-level A2A agent session inside a Swarm workspace scope.
type PeerIdentity struct {
	Handle         string         `json:"handle"`
	SessionID      string         `json:"session_id"`
	ConversationID string         `json:"conversation_id,omitempty"`
	WorkspacePath  string         `json:"workspace_path,omitempty"`
	ProjectID      string         `json:"project_id,omitempty"`
	ScopeKey       string         `json:"scope_key"`
	EndpointURL    string         `json:"endpoint_url"`
	CardURL        string         `json:"card_url"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RegisteredAt   time.Time      `json:"registered_at"`
	LastSeenAt     time.Time      `json:"last_seen_at"`
	ExpiresAt      time.Time      `json:"expires_at"`
}

// PresenceLease tracks peer liveness in the local registry.
type PresenceLease struct {
	SessionID string        `json:"session_id"`
	ScopeKey  string        `json:"scope_key"`
	TTL       time.Duration `json:"ttl"`
	RenewedAt time.Time     `json:"renewed_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// TaskListFilter captures ListTasks filtering and pagination.
type TaskListFilter struct {
	ContextID            string
	Status               a2apb.TaskState
	PageSize             int
	PageToken            string
	HistoryLength        *int32
	StatusTimestampAfter *time.Time
	IncludeArtifacts     bool
}

// RemoteTaskBinding ties a remote task/context to a local main conversation.
type RemoteTaskBinding struct {
	LocalSessionID  string    `json:"local_session_id"`
	ConversationID  string    `json:"conversation_id"`
	RemoteEndpoint  string    `json:"remote_endpoint"`
	RemoteTaskID    string    `json:"remote_task_id,omitempty"`
	RemoteContextID string    `json:"remote_context_id,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// InboundRequest is the local runtime representation of an inbound A2A request.
type InboundRequest struct {
	Peer             PeerIdentity
	ConversationID   string
	Request          *a2apb.SendMessageRequest
	ProjectedMessage *conversation.Message
}

// NormalizeScopeKey derives the A2A discovery scope key.
func NormalizeScopeKey(projectID, workspacePath string) string {
	if trimmed := strings.TrimSpace(projectID); trimmed != "" {
		return "project:" + trimmed
	}
	if workspacePath == "" {
		return ""
	}
	abs, err := filepath.Abs(workspacePath)
	if err != nil {
		abs = workspacePath
	}
	return "workspace:" + filepath.Clean(abs)
}

// NormalizeHandle prepares a human-friendly stable peer handle.
func NormalizeHandle(handle string) string {
	return strings.TrimSpace(handle)
}

func defaultSessionID(sessionID string) string {
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		return trimmed
	}
	return "a2a-" + uuid.NewString()
}

func ensureID(id string) string {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return trimmed
	}
	return uuid.NewString()
}

func defaultRPCPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return DefaultRPCPath
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	maps.Copy(dst, src)
	return dst
}

// ============================================================================
// Swarm Chatroom Types
// ============================================================================

// SwarmStatus represents an agent's availability for swarm collaboration.
type SwarmStatus string

const (
	SwarmStatusIdle    SwarmStatus = "idle"    // Not actively working, accepting DMs
	SwarmStatusWorking SwarmStatus = "working" // Actively processing, accepting DMs (may be slow)
	SwarmStatusBusy    SwarmStatus = "busy"    // Deep focus, auto-decline help requests
	SwarmStatusAway    SwarmStatus = "away"    // User stepped away, pause notifications
)

// AgentSwarmStatus tracks an agent's status visible to other swarm peers.
type AgentSwarmStatus struct {
	Handle       string      `json:"handle"`                 // agent-tui-ABC123
	Model        string      `json:"model"`                  // claude-3-sonnet
	Status       SwarmStatus `json:"status"`                 // idle, working, busy, away
	CurrentTask  string      `json:"current_task,omitempty"` // Human-readable task description
	TaskStarted  time.Time   `json:"task_started"`           // When current task began
	RunningTime  string      `json:"running_time,omitempty"` // Computed human-readable duration
	Capabilities []string    `json:"capabilities,omitempty"` // Optional: ["code_review", "refactoring"]
}

// RunningTimeDuration returns the human-readable running time.
func (s *AgentSwarmStatus) RunningTimeDuration() string {
	if s.TaskStarted.IsZero() {
		return ""
	}
	duration := time.Since(s.TaskStarted)
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	return fmt.Sprintf("%dm %ds", int(duration.Minutes()), int(duration.Seconds())%60)
}

// SwarmEventType identifies the kind of swarm event.
type SwarmEventType string

const (
	SwarmEventPeerJoined       SwarmEventType = "swarm_peer_joined"
	SwarmEventPeerLeft         SwarmEventType = "swarm_peer_left"
	SwarmEventPeerStatusChange SwarmEventType = "swarm_peer_status_changed"
	SwarmEventDM               SwarmEventType = "swarm_dm"
	SwarmEventBroadcast        SwarmEventType = "swarm_broadcast"
	SwarmEventQuery            SwarmEventType = "swarm_query"
	SwarmEventQueryResponse    SwarmEventType = "swarm_query_response"
)

// SwarmEvent represents an event pushed to the agent about swarm activity.
type SwarmEvent struct {
	Type      SwarmEventType `json:"type"`
	Timestamp time.Time      `json:"timestamp"`

	// For PeerJoined
	Peer *AgentSwarmStatus `json:"peer,omitempty"`

	// For PeerLeft
	PeerHandle string `json:"peer_handle,omitempty"`

	// For PeerStatusChange
	OldStatus SwarmStatus `json:"old_status,omitempty"`
	NewStatus SwarmStatus `json:"new_status,omitempty"`
	OldTask   string      `json:"old_task,omitempty"`
	NewTask   string      `json:"new_task,omitempty"`

	// For DM and Broadcast
	From      string `json:"from,omitempty"`       // Sender handle
	FromModel string `json:"from_model,omitempty"` // Sender model
	FromTask  string `json:"from_task,omitempty"`  // Sender's current task
	Message   string `json:"message,omitempty"`    // Message content
	QueryID   string `json:"query_id,omitempty"`   // For query/response correlation
	Question  string `json:"question,omitempty"`   // For incoming queries
	Response  string `json:"response,omitempty"`   // For query responses
}

// SwarmPeerInfo combines peer identity with swarm status for list_peers.
type SwarmPeerInfo struct {
	Handle       string      `json:"handle"`
	Model        string      `json:"model"`
	Status       SwarmStatus `json:"status"`
	CurrentTask  string      `json:"current_task,omitempty"`
	RunningTime  string      `json:"running_time,omitempty"`
	Endpoint     string      `json:"endpoint"`
	Conversation string      `json:"conversation_id,omitempty"`
}

// SwarmListPeersResult is returned by swarm_list_peers tool.
type SwarmListPeersResult struct {
	Peers []SwarmPeerInfo   `json:"peers"`
	Self  *AgentSwarmStatus `json:"self"`
}

// SwarmDMResult is returned by swarm_dm tool.
type SwarmDMResult struct {
	Status         string `json:"status"` // "sent" or "queued"
	ConversationID string `json:"conversation_id"`
	QueuedReason   string `json:"queued_reason,omitempty"` // Reason if queued (peer unreachable)

	// Async-DM fields (populated when the DM was delivered to a live peer):
	TaskID     string `json:"task_id,omitempty"`     // The peer's task ID for this DM (used for reply correlation)
	PeerHandle string `json:"peer_handle,omitempty"` // Recipient handle
	PeerStatus string `json:"peer_status,omitempty"` // Recipient's swarm status at delivery time (e.g. "idle", "working")
	PeerTask   string `json:"peer_task,omitempty"`   // Recipient's current_task at delivery time
}

// SwarmBroadcastResult is returned by swarm_broadcast tool.
type SwarmBroadcastResult struct {
	Status     string `json:"status"` // "sent"
	Recipients int    `json:"recipients"`
}

// SwarmUpdateStatusResult is returned by swarm_update_status tool.
type SwarmUpdateStatusResult struct {
	Status       string `json:"status"` // "updated"
	PreviousTask string `json:"previous_task"`
	RunningTime  string `json:"running_time"`
}

// SwarmCallback is called when a swarm event occurs.
type SwarmCallback func(event SwarmEvent)
