// Package session provides an SDK-native abstraction over the headless
// engine + bridge layer that previously lived in swarm-tui/headless/core
// and swarm-tui/headless/sdk.
//
// A Session encapsulates:
//
//   - An execution loop driven by *client.Client (input -> agent.Execute,
//     output -> fan-out subscribers).
//   - An in-memory state snapshot (active conversation, model/provider,
//     mode, tokens, error) reflecting AppState in the legacy engine.
//   - Conversation lifecycle (create/load/list/delete/switch, edit message).
//   - Profile / model / provider switching.
//   - Manual compaction.
//
// Design priorities:
//
//  1. Strongly-typed methods replace the legacy engine's
//     SendEvent(InputEvent) dispatch loop. Each input variant becomes a
//     named method with explicit parameters, so the IPC/ACP servers no
//     longer have to switch on event-type strings.
//
//  2. Stream-based output. Subscribe(handler) returns an unsubscribe
//     function and supports multiple concurrent subscribers. The session
//     fan-outs both agent.IntermediateUpdate events (from client.Client)
//     and session-level events (conversation switch, profile switch, etc.)
//     that aren't part of the agent stream.
//
//  3. Snapshot accessors. Snapshot() returns an immutable State value;
//     callers never share a *AppState pointer with internal goroutines.
//
//  4. Composability. session.New(...) takes its dependencies explicitly
//     (client, conversation manager, config provider) — no global
//     singletons.
//
//  5. Lifecycle. Start(ctx) / Stop(ctx) are idempotent and release every
//     internal goroutine. goleak-clean.
//
// Cutover path:
//
//   - Step 3.1 (this step): ship the Session interface + ClientSession
//     implementation behind the SWARM_USE_LEGACY_ENGINE rollback flag.
//     Existing IPC/ACP servers continue using core.Engine; the new
//     abstraction is available for callers that opt in.
//   - Step 3.2: rewrite headless/cmd/ipc-server and headless/cmd/acp-server
//     against session.Session, then delete swarm-tui/headless/core and
//     swarm-tui/headless/sdk.
package session
