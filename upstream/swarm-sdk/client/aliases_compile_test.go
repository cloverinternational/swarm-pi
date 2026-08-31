package client_test

// This is a compile-oriented test (T203). Its purpose is to prove that a
// consumer can use the full client facade — options, callbacks, and the types
// flowing through them — while importing ONLY the client package (plus the
// standard library). If any cross-package type used in an exported client
// signature were missing from client/aliases.go, this file would fail to
// compile. It performs no network or disk I/O.

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// handleUpdate is shaped exactly like the callback SubscribeUpdates accepts:
// func(ctx context.Context, u client.IntermediateUpdate) error. Inside it we
// type-switch over the concrete update variants — every variant a consumer
// might observe must be nameable through the client package alone.
func handleUpdate(_ context.Context, u client.IntermediateUpdate) error {
	switch v := u.(type) {
	case client.ContentUpdate:
		_ = v.Content
	case client.ToolCallUpdate:
		_ = v.Name
	case client.ToolResultUpdate:
		_ = v.Output
	case client.ThinkingUpdate:
		_ = v.Content
	case client.AssistantMessageUpdate:
		_ = v.Content
	case client.TokenCountUpdate,
		client.TurnUsageUpdate,
		client.CompactionNeededUpdate,
		client.CompactionDoneUpdate,
		client.HookExecutionUpdate,
		client.ToolOutputChunk,
		client.HookOutputChunk,
		client.SubAgentUpdate,
		client.FallbackUpdate,
		client.ExhaustedUpdate,
		client.HeartbeatUpdate,
		client.SubAgentCompleteUpdate:
		_ = v.UpdateType()
	default:
		_ = v.UpdateType()
	}
	return nil
}

// TestAliasesCompile exercises the facade types and option constructors using
// only client.* names. It is a build-time assertion; the body intentionally
// avoids constructing a live Client (no network, no ~/.swarm access).
func TestAliasesCompile(t *testing.T) {
	// Callback type matches SubscribeUpdates' parameter exactly.
	var cb func(ctx context.Context, u client.IntermediateUpdate) error = handleUpdate
	_ = cb

	// Option values built entirely from client-aliased config types.
	opts := []client.Option{
		client.WithCompactionConfig(client.AutoCompactionConfig{}),
		client.WithOperatingMode(&client.OperatingMode{}),
		client.WithCodeModeConfig(&client.CodeModeConfig{}),
		client.WithAutoModeHookConfig(&client.AutoModeConfig{}),
		client.WithRecapHookConfig(&client.RecapConfig{}),
		client.WithAutogenSkills(&client.AutogenSkillsConfig{}),
	}
	_ = opts

	// Declare vars of every re-exported type to assert they resolve through the
	// client package. These are zero values; no behavior is invoked.
	var (
		_ client.Tool
		_ client.ToolResult
		_ client.Registry
		_ client.Permission
		_ client.PermissionChecker
		_ client.Hook
		_ client.Definition
		_ client.ExecuteRequest
		_ client.ExecuteResponse
		_ client.AutoCompactionConfig
		_ client.IntermediateUpdate
		_ client.Conversation
		_ client.Message
		_ client.ConversationCreateOptions
		_ client.GetMessagesOptions
		_ client.ConversationManagerType
		_ client.Storage
		_ client.ProviderInterface
		_ client.ChatRequest
		_ client.ChatResponse
		_ client.StreamChunk
		_ client.Capabilities
		_ client.OperatingMode
		_ client.RuntimeManager
		_ client.CompactionResult
		_ client.ConfigManager
		_ client.Logger
		_ client.Tracer
		_ client.AutoModeConfig
		_ client.RecapConfig
		_ client.AutogenSkillsConfig
		_ client.CodeModeConfig
	)
}

// Compile-time interface satisfaction: each concrete variant alias must satisfy
// the IntermediateUpdate alias when referenced solely through the client package.
var (
	_ client.IntermediateUpdate = client.ContentUpdate{}
	_ client.IntermediateUpdate = client.ToolCallUpdate{}
	_ client.IntermediateUpdate = client.ToolResultUpdate{}
	_ client.IntermediateUpdate = client.ThinkingUpdate{}
	_ client.IntermediateUpdate = client.AssistantMessageUpdate{}
)
