// Package acp implements the Agent Client Protocol (ACP) server for the Swarm
// headless engine. ACP is the protocol used by code editors like Zed to
// communicate with AI coding agents.
//
// Transport: newline-delimited JSON-RPC 2.0 over stdio.
//
// Key methods handled by the agent (incoming from editor):
//   - session/new           - create a new session
//   - session/prompt        - send a user message and receive streaming updates
//   - session/cancel        - cancel an ongoing prompt turn
//
// Notifications & requests sent by the agent (outgoing to editor):
//   - session/update        - stream real-time progress (text chunks, tool calls, plans)
//   - session/request_permission - request user approval for a tool action
//
// See: https://agentclientprotocol.org
package acp
