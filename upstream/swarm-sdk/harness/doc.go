// Package harness is a public, side-effect-free compiler for Swarm "harness"
// documents (apiVersion swarm.ai/v1alpha1, kind Harness).
//
// # Scope (Phase 1)
//
// This package implements ONLY parse -> validate -> resolve -> compile. It
// produces an immutable [Plan] describing exactly what an agent runtime should
// be, but it constructs NO runtime: no provider, no client, no tool instances,
// no MCP process, no goroutine, no network, and no filesystem writes. Turning a
// [Plan] into a live client is the responsibility of a later phase in a
// different package (client.WithHarnessPlan). Keeping compilation pure makes the
// document trivially testable and lets any caller inspect a plan before any
// resource is created.
//
// # Deliberate non-imports
//
// The package imports only the standard library and
// github.com/Swarm-Code/mono/swarm-sdk/configformat. It must never import
// client, the TUI, internal/agent, internal/mcp, internal/hooks, or
// internal/tools. That guarantee keeps the public document schema decoupled from
// the private composition root audited in the Phase 0 parity baseline.
//
// # Secrets
//
// Secret material (credential values, resolved prompt text) is tainted: it is
// stored behind explicit reveal accessors and is NEVER emitted by [Plan.Explain],
// [Plan.Digest], [Plan.Provenance], any Diagnostic, or JSON marshaling. Explain
// output carries only SHA-256 content hashes, byte counts, and manifest-relative
// reference tokens.
//
// # Capability policy
//
// [Catalog] exposes stable capability IDs and their default policy class derived
// from the Phase 0 PARITY_BASELINE authoritative policy table. IDs are stable
// dotted identifiers (for example forge.read), never Go type names. The catalog
// is metadata only; it constructs no tools.
package harness
