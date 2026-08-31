// Package swarm provides tools for A2A (Agent-to-Agent) swarm management.
// This is an optional package that can be imported to enable swarm capabilities.
package swarm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// SwarmParams are the parameters for swarm operations.
type SwarmParams struct {
	// Action specifies the operation to perform.
	// Options: "create", "join", "list", "send", "broadcast", "status", "sync"
	Action string `json:"action" description:"Action to perform: create, join, list, send, broadcast, status, sync" required:"true"`

	// Handle is the unique identifier for a peer (required for create, send, join).
	Handle string `json:"handle,omitempty" description:"Unique identifier for a peer (required for create/send/join)"`

	// Username is a display name for the agent when joining (optional for join).
	Username string `json:"username,omitempty" description:"Display name when joining the swarm (optional, defaults to handle)"`

	// Target is the handle of the peer to send a message to (required for send).
	Target string `json:"target,omitempty" description:"Target peer handle (required for send action)"`

	// Name is a human-readable name (optional for create).
	Name string `json:"name,omitempty" description:"Human-readable name for the peer"`

	// Description explains what this peer does (optional).
	Description string `json:"description,omitempty" description:"Description of what this peer does"`

	// SystemPrompt defines the peer's behavior (optional).
	SystemPrompt string `json:"system_prompt,omitempty" description:"System prompt defining peer behavior"`

	// Model specifies the LLM model (optional).
	Model string `json:"model,omitempty" description:"LLM model to use (uses default if empty)"`

	// Tools are the tools the peer can use (optional for create).
	Tools []string `json:"tools,omitempty" description:"Tools the peer can use"`

	// Message is the content to send (required for send/broadcast).
	Message string `json:"message,omitempty" description:"Message content (required for send/broadcast)"`

	// Status is the current status (optional for status action).
	// Options: "idle", "working", "busy", "away"
	Status string `json:"status,omitempty" description:"Current status: idle, working, busy, away (for status action)"`

	// CurrentTask describes what the agent is currently working on.
	CurrentTask string `json:"current_task,omitempty" description:"Description of current task being worked on"`

	// CompletedTasks lists tasks that have been finished.
	CompletedTasks []string `json:"completed_tasks,omitempty" description:"List of completed task descriptions"`
}

// SwarmCommunicator is the interface for swarm communication.
type SwarmCommunicator interface {
	// CreatePeer creates a new A2A peer with the given configuration.
	CreatePeer(ctx context.Context, config PeerConfig) (*PeerInfo, error)

	// JoinSwarm enables A2A mode and joins the swarm with the given handle.
	JoinSwarm(ctx context.Context, handle string, username string) error

	// ListSwarmPeers returns all peers in the swarm with their status.
	ListSwarmPeers(ctx context.Context) ([]PeerStatus, error)

	// SendMessage sends a direct message to a specific peer.
	SendMessage(ctx context.Context, targetHandle string, message string) error

	// Broadcast sends a message to all peers in the swarm.
	Broadcast(ctx context.Context, message string) error

	// UpdateStatus updates the local agent's status and current task.
	UpdateStatus(ctx context.Context, status string, currentTask string) error

	// SyncTasks syncs completed tasks with the swarm.
	SyncTasks(ctx context.Context, completedTasks []string) error

	// GetLocalHandle returns the local agent's A2A handle.
	GetLocalHandle() string

	// GetLocalStatus returns the local agent's current status.
	GetLocalStatus() AgentStatus

	// IsA2AEnabled returns true if A2A is enabled.
	IsA2AEnabled() bool
}

// PeerConfig configures a new A2A peer.
type PeerConfig struct {
	Handle       string
	Name         string
	Description  string
	SystemPrompt string
	Model        string
	Tools        []string
}

// PeerInfo contains basic information about a peer.
type PeerInfo struct {
	Handle      string `json:"handle"`
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Status      string `json:"status"`
	IsLocal     bool   `json:"is_local"`
	ProcessID   int    `json:"process_id,omitempty"`   // OS process ID when spawned as separate process
	StoragePath string `json:"storage_path,omitempty"` // Path to peer's storage directory
}

// PeerStatus contains detailed status information about a peer.
type PeerStatus struct {
	Handle         string   `json:"handle"`
	Name           string   `json:"name"`
	Status         string   `json:"status"` // idle, working, busy, away
	CurrentTask    string   `json:"current_task"`
	CompletedTasks []string `json:"completed_tasks"`
	LastSeen       string   `json:"last_seen"`
	Endpoint       string   `json:"endpoint"`
	IsLocal        bool     `json:"is_local"`
}

// AgentStatus represents the local agent's status.
type AgentStatus struct {
	Handle         string   `json:"handle"`
	Status         string   `json:"status"`
	CurrentTask    string   `json:"current_task"`
	CompletedTasks []string `json:"completed_tasks"`
}

// SwarmTool provides swarm management and communication capabilities.
type SwarmTool struct {
	logger       observability.Logger
	tracer       observability.Tracer
	communicator SwarmCommunicator
}

// NewSwarmTool creates a new Swarm tool instance.
func NewSwarmTool(logger observability.Logger, tracer observability.Tracer) *SwarmTool {
	if logger == nil {
		logger = observability.NewNopLogger()
	}
	if tracer == nil {
		tracer = observability.NewNoopTracer()
	}

	return &SwarmTool{
		logger: logger,
		tracer: tracer,
	}
}

// SetCommunicator sets the swarm communicator.
func (t *SwarmTool) SetCommunicator(c SwarmCommunicator) {
	t.communicator = c
}

// Name returns the tool name.
func (t *SwarmTool) Name() string {
	return "swarm"
}

// Description returns the tool description.
func (t *SwarmTool) Description() string {
	return `Manage and communicate with A2A (Agent-to-Agent) swarm peers.

This tool enables agents to create peers, communicate with other agents, and coordinate
distributed task execution within a project-based swarm.

Actions:
- create: Create a new peer agent with specific capabilities
- join: Enable A2A mode and join the swarm with a chosen handle
- list: List all peers in the swarm and their current status/tasks
- send: Send a direct message to a specific peer
- broadcast: Send a message to all peers in the swarm
- status: Update your current status and task for others to see
- sync: Sync completed tasks with the swarm

Use this to coordinate multi-agent workflows, share progress, and collaborate
on complex tasks across the swarm. Each peer can see what others are working
on and completed tasks are visible to the entire swarm.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *SwarmTool) Parameters() any {
	return tools.SchemaFor[SwarmParams]()
}

// Execute runs the swarm tool.
func (t *SwarmTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.execute")
	defer span.End()

	if t.communicator == nil {
		return tools.NewToolResult("Swarm communicator not configured").
			WithMetadata("success", false).
			WithMetadata("error", "communicator not set"), nil
	}

	// Extract action
	action, ok := params["action"].(string)
	if !ok || action == "" {
		return tools.NewToolResult("Missing required parameter: action").
			WithMetadata("success", false).
			WithMetadata("error", "action is required"), nil
	}

	// Join is special - it enables A2A mode, so skip the enabled check for it
	if action != "join" && !t.communicator.IsA2AEnabled() {
		return tools.NewToolResult("A2A is not enabled. Use 'join' action to enable A2A and join the swarm.").
			WithMetadata("success", false).
			WithMetadata("error", "A2A not enabled"), nil
	}

	switch action {
	case "create":
		return t.executeCreate(ctx, params)
	case "join":
		return t.executeJoin(ctx, params)
	case "list":
		return t.executeList(ctx, params)
	case "send":
		return t.executeSend(ctx, params)
	case "broadcast":
		return t.executeBroadcast(ctx, params)
	case "status":
		return t.executeStatus(ctx, params)
	case "sync":
		return t.executeSync(ctx, params)
	default:
		return tools.NewToolResult(fmt.Sprintf("Unknown action: %s. Valid actions: create, join, list, send, broadcast, status, sync", action)).
			WithMetadata("success", false).
			WithMetadata("error", "unknown action"), nil
	}
}

// executeCreate handles the "create" action.
func (t *SwarmTool) executeCreate(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.create")
	defer span.End()

	handle, _ := params["handle"].(string)
	if handle == "" {
		return tools.NewToolResult("Missing required parameter: handle").
			WithMetadata("success", false).
			WithMetadata("error", "handle is required"), nil
	}

	name, _ := params["name"].(string)
	description, _ := params["description"].(string)
	systemPrompt, _ := params["system_prompt"].(string)
	model, _ := params["model"].(string)

	// Parse tools array
	var toolsList []string
	if toolsRaw, ok := params["tools"].([]any); ok {
		for _, t := range toolsRaw {
			if s, ok := t.(string); ok {
				toolsList = append(toolsList, s)
			}
		}
	}

	config := PeerConfig{
		Handle:       handle,
		Name:         name,
		Description:  description,
		SystemPrompt: systemPrompt,
		Model:        model,
		Tools:        toolsList,
	}

	t.logger.Info(ctx, "swarm.creating_peer",
		observability.F("handle", handle),
		observability.F("name", name))

	peerInfo, err := t.communicator.CreatePeer(ctx, config)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("Failed to create peer: %v", err)).
			WithMetadata("success", false).
			WithMetadata("error", err.Error()), nil
	}

	result := fmt.Sprintf("✅ Created peer '%s'", peerInfo.Handle)
	if peerInfo.Name != "" {
		result += fmt.Sprintf(" (%s)", peerInfo.Name)
	}
	result += fmt.Sprintf("\n📍 Endpoint: %s\n💡 This peer is now part of the swarm and can communicate with other agents.", peerInfo.Endpoint)

	return tools.NewToolResult(result).
		WithMetadata("success", true).
		WithMetadata("handle", peerInfo.Handle).
		WithMetadata("endpoint", peerInfo.Endpoint), nil
}

// executeList handles the "list" action.
func (t *SwarmTool) executeList(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.list")
	defer span.End()

	peers, err := t.communicator.ListSwarmPeers(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("Failed to list peers: %v", err)).
			WithMetadata("success", false).
			WithMetadata("error", err.Error()), nil
	}

	localHandle := t.communicator.GetLocalHandle()
	localStatus := t.communicator.GetLocalStatus()

	if len(peers) == 0 {
		return tools.NewToolResult("🐝 No peers in the swarm yet.\n\nYou can create peers using the 'create' action.").
			WithMetadata("success", true).
			WithMetadata("count", 0).
			WithMetadata("local_handle", localHandle), nil
	}

	// Build the response
	var result strings.Builder
	result.WriteString(fmt.Sprintf("🐝 Swarm Status - %d peers connected\n", len(peers)))
	result.WriteString(fmt.Sprintf("👤 You: %s (%s)\n", localHandle, localStatus.Status))
	if localStatus.CurrentTask != "" {
		result.WriteString(fmt.Sprintf("   Working on: %s\n", localStatus.CurrentTask))
	}
	if len(localStatus.CompletedTasks) > 0 {
		result.WriteString(fmt.Sprintf("   ✅ Completed: %d tasks\n", len(localStatus.CompletedTasks)))
	}
	result.WriteString("\n👥 Other Peers:\n")

	for _, peer := range peers {
		if peer.IsLocal {
			continue // Skip self in the list
		}
		result.WriteString(fmt.Sprintf("\n• %s", peer.Handle))
		if peer.Name != "" {
			result.WriteString(fmt.Sprintf(" (%s)", peer.Name))
		}
		statusEmoji := "🟢"
		switch peer.Status {
		case "working":
			statusEmoji = "🟡"
		case "busy":
			statusEmoji = "🔴"
		case "away":
			statusEmoji = "⚪"
		}
		result.WriteString(fmt.Sprintf(" %s %s\n", statusEmoji, peer.Status))
		if peer.CurrentTask != "" {
			result.WriteString(fmt.Sprintf("  📋 Working on: %s\n", peer.CurrentTask))
		}
		if len(peer.CompletedTasks) > 0 {
			result.WriteString(fmt.Sprintf("  ✅ Completed: %d tasks\n", len(peer.CompletedTasks)))
		}
		if peer.LastSeen != "" {
			result.WriteString(fmt.Sprintf("  ⏱️ Last seen: %s\n", peer.LastSeen))
		}
	}

	return tools.NewToolResult(result.String()).
		WithMetadata("success", true).
		WithMetadata("count", len(peers)).
		WithMetadata("local_handle", localHandle).
		WithMetadata("peers", peers), nil
}

// executeSend handles the "send" action.
func (t *SwarmTool) executeSend(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.send")
	defer span.End()

	target, _ := params["target"].(string)
	if target == "" {
		return tools.NewToolResult("Missing required parameter: target (peer handle to send message to)").
			WithMetadata("success", false).
			WithMetadata("error", "target is required"), nil
	}

	message, _ := params["message"].(string)
	if message == "" {
		return tools.NewToolResult("Missing required parameter: message").
			WithMetadata("success", false).
			WithMetadata("error", "message is required"), nil
	}

	t.logger.Info(ctx, "swarm.sending_message",
		observability.F("target", target),
		observability.F("message_length", len(message)))

	err := t.communicator.SendMessage(ctx, target, message)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("Failed to send message: %v", err)).
			WithMetadata("success", false).
			WithMetadata("error", err.Error()), nil
	}

	localHandle := t.communicator.GetLocalHandle()
	result := fmt.Sprintf("📨 Message sent to %s\n\nFrom: %s\nTo: %s\nTime: %s\n\n💬 %s",
		target, localHandle, target, time.Now().Format("15:04:05"), message)

	return tools.NewToolResult(result).
		WithMetadata("success", true).
		WithMetadata("target", target).
		WithMetadata("timestamp", time.Now().Unix()), nil
}

// executeBroadcast handles the "broadcast" action.
func (t *SwarmTool) executeBroadcast(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.broadcast")
	defer span.End()

	message, _ := params["message"].(string)
	if message == "" {
		return tools.NewToolResult("Missing required parameter: message").
			WithMetadata("success", false).
			WithMetadata("error", "message is required"), nil
	}

	t.logger.Info(ctx, "swarm.broadcasting",
		observability.F("message_length", len(message)))

	err := t.communicator.Broadcast(ctx, message)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("Failed to broadcast: %v", err)).
			WithMetadata("success", false).
			WithMetadata("error", err.Error()), nil
	}

	localHandle := t.communicator.GetLocalHandle()
	result := fmt.Sprintf("📢 Broadcast to swarm\n\nFrom: %s\nTime: %s\n\n💬 %s",
		localHandle, time.Now().Format("15:04:05"), message)

	return tools.NewToolResult(result).
		WithMetadata("success", true).
		WithMetadata("timestamp", time.Now().Unix()), nil
}

// executeStatus handles the "status" action.
func (t *SwarmTool) executeStatus(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.status")
	defer span.End()

	status, _ := params["status"].(string)
	if status == "" {
		status = "working" // Default status
	}

	currentTask, _ := params["current_task"].(string)

	t.logger.Info(ctx, "swarm.updating_status",
		observability.F("status", status),
		observability.F("task", currentTask))

	err := t.communicator.UpdateStatus(ctx, status, currentTask)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("Failed to update status: %v", err)).
			WithMetadata("success", false).
			WithMetadata("error", err.Error()), nil
	}

	statusEmoji := "🟢"
	switch status {
	case "working":
		statusEmoji = "🟡"
	case "busy":
		statusEmoji = "🔴"
	case "away":
		statusEmoji = "⚪"
	}

	localHandle := t.communicator.GetLocalHandle()
	result := fmt.Sprintf("📊 Status updated\n\n%s %s: %s\n", statusEmoji, localHandle, status)
	if currentTask != "" {
		result += fmt.Sprintf("📋 Current task: %s\n", currentTask)
	}
	result += "\n💡 Other agents can now see your updated status in the swarm."

	return tools.NewToolResult(result).
		WithMetadata("success", true).
		WithMetadata("status", status).
		WithMetadata("current_task", currentTask), nil
}

// executeSync handles the "sync" action.
func (t *SwarmTool) executeSync(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.sync")
	defer span.End()

	// Parse completed tasks
	var completedTasks []string
	if tasksRaw, ok := params["completed_tasks"].([]any); ok {
		for _, t := range tasksRaw {
			if s, ok := t.(string); ok {
				completedTasks = append(completedTasks, s)
			}
		}
	}

	t.logger.Info(ctx, "swarm.syncing_tasks",
		observability.F("completed_count", len(completedTasks)))

	err := t.communicator.SyncTasks(ctx, completedTasks)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("Failed to sync tasks: %v", err)).
			WithMetadata("success", false).
			WithMetadata("error", err.Error()), nil
	}

	localHandle := t.communicator.GetLocalHandle()
	var result strings.Builder
	result.WriteString(fmt.Sprintf("🔄 Task sync completed\n\n%s has synced %d completed tasks with the swarm.\n",
		localHandle, len(completedTasks)))

	if len(completedTasks) > 0 {
		result.WriteString("\n✅ Recently completed:\n")
		for i, task := range completedTasks {
			if i >= 5 { // Show max 5
				result.WriteString(fmt.Sprintf("\n... and %d more", len(completedTasks)-5))
				break
			}
			result.WriteString(fmt.Sprintf("  • %s\n", task))
		}
	}

	return tools.NewToolResult(result.String()).
		WithMetadata("success", true).
		WithMetadata("synced_count", len(completedTasks)), nil
}

// executeJoin handles the "join" action.
func (t *SwarmTool) executeJoin(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "swarm.join")
	defer span.End()

	handle, _ := params["handle"].(string)
	if handle == "" {
		return tools.NewToolResult("Missing required parameter: handle").
			WithMetadata("success", false).
			WithMetadata("error", "handle is required to join the swarm"), nil
	}

	// Username defaults to handle if not provided
	username, _ := params["username"].(string)
	if username == "" {
		username = handle
	}

	t.logger.Info(ctx, "swarm.joining",
		observability.F("handle", handle),
		observability.F("username", username))

	// Idempotent: already in the swarm — return current info as success.
	if t.communicator.IsA2AEnabled() {
		currentHandle := t.communicator.GetLocalHandle()
		if currentHandle != "" {
			result := fmt.Sprintf("🌐 Already in swarm as '%s'\n\n", currentHandle)
			result += "A2A is active. You can use:\n"
			result += "  • List swarm members: action=list\n"
			result += "  • Broadcast messages: action=broadcast, message=\"...\"\n"
			result += "  • Send direct messages: action=send, target=<handle>, message=\"...\"\n"
			result += "  • Update your status: action=status, status=\"working\", current_task=\"...\"\n"
			result += "  • Sync completed tasks: action=sync, completed_tasks=[\"...\"]"
			return tools.NewToolResult(result).
				WithMetadata("success", true).
				WithMetadata("handle", currentHandle).
				WithMetadata("already_joined", true), nil
		}
	}

	// Join the swarm (this enables A2A)
	err := t.communicator.JoinSwarm(ctx, handle, username)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("Failed to join swarm: %v", err)).
			WithMetadata("success", false).
			WithMetadata("error", err.Error()), nil
	}

	result := fmt.Sprintf("🌐 Joined swarm as '%s' (%s)\n\n", username, handle)
	result += "A2A mode is now enabled. You can now:\n"
	result += "  • List swarm members: action=list\n"
	result += "  • Broadcast messages: action=broadcast, message=\"...\"\n"
	result += "  • Send direct messages: action=send, target=<handle>, message=\"...\"\n"
	result += "  • Update your status: action=status, status=\"working\", current_task=\"...\"\n"
	result += "  • Sync completed tasks: action=sync, completed_tasks=[\"...\"]"

	return tools.NewToolResult(result).
		WithMetadata("success", true).
		WithMetadata("handle", handle).
		WithMetadata("username", username).
		WithMetadata("a2a_enabled", true), nil
}

// IsSafe returns true - this tool is safe for autonomous use.
func (t *SwarmTool) IsSafe() bool {
	return true
}

// IsReadOnly returns false - this tool modifies state.
func (t *SwarmTool) IsReadOnly() bool {
	return false
}

// Categories returns the tool categories.
func (t *SwarmTool) Categories() []string {
	return []string{"a2a", "swarm", "communication", "collaboration"}
}

// RequiresPermission returns the required permissions.
func (t *SwarmTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// IsIdempotent returns false.
func (t *SwarmTool) IsIdempotent() bool {
	return false
}

// SupportsParallel returns true.
func (t *SwarmTool) SupportsParallel() bool {
	return true
}
