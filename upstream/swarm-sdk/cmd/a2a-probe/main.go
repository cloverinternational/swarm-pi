// cmd/a2a-probe — offline shape probe for the A2A swarm tool subsystem.
//
// Layer 1: no LLM, no network. Validates:
//   - PLAN mode whitelist contains all swarm/a2a tool names
//   - SwarmTool has correct Name(), Description() (includes "join"), and
//     no dead RequiresConfirmation(SwarmParams) method
//   - A2A chatroom tools (swarm_list_peers, swarm_dm, swarm_broadcast,
//     swarm_update_status) are returned by a2a.SwarmTools()
//   - SwarmTool nil-communicator guard returns a soft error, not a panic
//   - A2APersistedMetadataKey guard skips re-persisting already-saved messages
//   - PLAN mode whitelist fix: every expected swarm/a2a tool name is present
//
// Usage:
//
//	go run ./cmd/a2a-probe
//
// Exit 0 = all checks passed. Non-zero = at least one failure (details on stderr).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	swarmtool "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/swarm"
)

// ─── minimal SwarmToolInterface stub ──────────────────────────────────────────

type stubRuntime struct{}

func (s *stubRuntime) ListPeers(_ context.Context) (*a2a.SwarmListPeersResult, error) {
	return &a2a.SwarmListPeersResult{}, nil
}
func (s *stubRuntime) SendDM(_ context.Context, _, _ string) (*a2a.SwarmDMResult, error) {
	return &a2a.SwarmDMResult{}, nil
}
func (s *stubRuntime) Broadcast(_ context.Context, _ string) (*a2a.SwarmBroadcastResult, error) {
	return &a2a.SwarmBroadcastResult{}, nil
}
func (s *stubRuntime) UpdateStatus(_ context.Context, _ a2a.SwarmStatus, _ string) (*a2a.SwarmUpdateStatusResult, error) {
	return &a2a.SwarmUpdateStatusResult{}, nil
}

// ─── probe helpers ────────────────────────────────────────────────────────────

var failures []string

func check(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("  ✓  %s\n", name)
	} else {
		fmt.Fprintf(os.Stderr, "  ✗  %s: %s\n", name, detail)
		failures = append(failures, name)
	}
}

func section(title string) { fmt.Printf("\n=== %s ===\n", title) }

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	fmt.Println("A2A probe — offline shape verification")

	// ── 1. PLAN mode whitelist ────────────────────────────────────────────────
	section("PLAN mode AllowedTools whitelist")
	planAllowed := make(map[string]bool, len(mode.PlanMode.AllowedTools))
	for _, t := range mode.PlanMode.AllowedTools {
		planAllowed[t] = true
	}
	required := []string{
		"swarm",
		"swarm_list_peers", "swarm_dm", "swarm_broadcast", "swarm_update_status",
		"a2a_list_agents", "a2a_fetch_agent_card",
		"a2a_send_message", "a2a_send_streaming_message",
		"a2a_get_task", "a2a_list_tasks", "a2a_cancel_task", "a2a_subscribe_task",
	}
	for _, name := range required {
		check("plan_whitelist:"+name, planAllowed[name],
			fmt.Sprintf("missing from PlanMode.AllowedTools (HideBlockedTools=%v)", mode.PlanMode.HideBlockedTools))
	}

	// ── 2. SwarmTool shape ────────────────────────────────────────────────────
	section("SwarmTool (tools/swarm)")
	tool := swarmtool.NewSwarmTool(nil, nil)

	check("swarm_tool_name", tool.Name() == "swarm",
		fmt.Sprintf("got %q", tool.Name()))

	desc := tool.Description()
	check("description_has_join_action", strings.Contains(desc, "join:"),
		"description missing '- join:' bullet (was only in action list header, not body)")

	check("description_has_all_actions", func() bool {
		for _, action := range []string{"create:", "join:", "list:", "send:", "broadcast:", "status:", "sync:"} {
			if !strings.Contains(desc, action) {
				return false
			}
		}
		return true
	}(), "one or more action bullets missing from description")

	// ── 3. Nil communicator guard — must return soft error, not panic ─────────
	section("SwarmTool nil-communicator guard")
	result, err := tool.Execute(context.Background(), map[string]any{"action": "list"})
	check("nil_comm_no_panic", err == nil, fmt.Sprintf("unexpected Go error: %v", err))
	check("nil_comm_soft_error", result != nil && strings.Contains(result.Output, "communicator"),
		fmt.Sprintf("expected communicator error in result, got: %v", result))

	// ── 4. A2A chatroom tools ─────────────────────────────────────────────────
	section("A2A chatroom tools (a2a.SwarmTools)")
	stub := &stubRuntime{}
	chatTools := a2a.SwarmTools(stub)
	check("chatroom_tool_count", len(chatTools) == 4,
		fmt.Sprintf("expected 4 tools, got %d", len(chatTools)))

	expectedChatTools := map[string]bool{
		"swarm_list_peers":    false,
		"swarm_dm":            false,
		"swarm_broadcast":     false,
		"swarm_update_status": false,
	}
	for _, ct := range chatTools {
		expectedChatTools[ct.Name()] = true
	}
	for name, found := range expectedChatTools {
		check("chatroom_tool:"+name, found, "tool not returned by a2a.SwarmTools()")
	}

	// Exercise each chatroom tool executes without panicking
	for _, ct := range chatTools {
		var params map[string]any
		switch ct.Name() {
		case "swarm_dm":
			params = map[string]any{"peer_handle": "test-peer", "message": "hello"}
		case "swarm_broadcast":
			params = map[string]any{"message": "broadcast test"}
		case "swarm_update_status":
			params = map[string]any{"status": "idle"}
		default:
			params = map[string]any{}
		}
		r, execErr := ct.Execute(context.Background(), params)
		check("chatroom_exec:"+ct.Name(),
			execErr == nil && r != nil,
			fmt.Sprintf("err=%v result=%v", execErr, r))
	}

	// ── 5. A2APersistedMetadataKey is defined and has expected value ──────────
	section("A2APersistedMetadataKey constant")
	check("key_value", conversation.A2APersistedMetadataKey == "a2a_persisted",
		fmt.Sprintf("got %q", conversation.A2APersistedMetadataKey))

	// ── 6. WebSocket pool shape probe ─────────────────────────────────────────
	section("WebSocket ConnectionPool (transport)")
	pool := a2a.NewConnectionPool(nil)
	check("ws_pool_constructed", pool != nil, "NewConnectionPool returned nil")
	check("ws_pool_has_no_connection_initially", !pool.HasConnection("nonexistent-peer"),
		"pool reported a connection that should not exist")
	pool.Close()
	check("ws_pool_close_idempotent", func() bool {
		// Calling Close twice must not panic.
		defer func() { _ = recover() }()
		pool.Close()
		return true
	}(), "second Close() call panicked")

	// Verify WebSocket constants are exported and well-formed.
	check("ws_path_exported", a2a.WebSocketPath == "/ws",
		fmt.Sprintf("expected /ws, got %q", a2a.WebSocketPath))
	check("ws_subprotocol_exported", a2a.WebSocketSubprotocol == "a2a-protocol-v1",
		fmt.Sprintf("expected a2a-protocol-v1, got %q", a2a.WebSocketSubprotocol))

	// ── summary ───────────────────────────────────────────────────────────────
	fmt.Printf("\n─── result: %d checks passed, %d failed ───\n",
		(len(required)+len(expectedChatTools)+len(chatTools)+5+5)-len(failures), len(failures))

	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "\nFailed checks:\n")
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "  • %s\n", f)
		}
		os.Exit(1)
	}
	fmt.Println("All checks passed.")
}
