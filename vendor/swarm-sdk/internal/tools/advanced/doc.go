// Package advanced provides an experimental, provider-agnostic framework for
// advanced tool use features inspired by Anthropic's Tool Use Examples, Deferred
// Tool Loading, Programmatic Tool Calling, and Tool Search patterns.
//
// # Design Principles
//
//   - Provider-agnostic: all features work with Anthropic, OpenAI, Gemini, and
//     any future provider. Provider-specific details are handled by existing
//     translator layers; this package only enriches the canonical provider.Tool
//     Metadata map.
//   - Additive-only: no existing SDK interfaces, structs, or functions are
//     modified. All extension points are opt-in interfaces that existing
//     tools.Tool implementations can adopt at their own pace.
//   - Composable: each feature (deferred loading, examples, state, permissions,
//     search) is independently usable. Consumers pick what they need.
//
// # Features
//
// Deferrable — Tools implement Deferrable to signal that their definition
// should NOT be included in the initial LLM context. A companion ToolSearchTool
// lets the model discover and load deferred tools on demand, dramatically
// reducing baseline token consumption.
//
// InputExamples — The existing tools.ToolWithExamples interface is already in
// the SDK but nothing propagates examples to providers. The bridge.go helper
// reads examples from any tool implementing ToolWithExamples and writes them
// into provider.Tool.Metadata["input_examples"] so provider translators can
// emit them in their native format.
//
// AppState — A thread-safe shared state container that tools can read and
// mutate during execution, enabling multi-step programmatic workflows without
// round-tripping through the LLM for each intermediate result.
//
// AdvancedPermissions — A three-state (allow / deny / ask) permission model
// with structured reasons, suggestions, and optional input transformation,
// extending the existing binary PermissionChecker.
//
// ToolSearchTool — A built-in tool implementing tools.Tool that searches all
// registered tools (including deferred ones) by name, description, category,
// and tags. It is always eagerly loaded so the model can discover deferred
// tools without them consuming context tokens.
//
// ToolBuilder — A fluent API for constructing tools with advanced metadata
// in a single expression, avoiding boilerplate.
//
// Bridge — EnrichProviderTool / EnrichProviderTools functions that take an
// existing provider.Tool plus its underlying tools.Tool and populate the
// Metadata map with all advanced fields. Each provider's existing translate
// layer can then inspect Metadata and emit what it supports.
//
// # Integration
//
// The recommended integration point is after agent.convertToProviderTool and
// before the provider.ChatRequest is dispatched:
//
//	providerTool := agent.convertToProviderTool(sdkTool)
//	providerTool = advanced.EnrichProviderTool(providerTool, sdkTool)
//
// For deferred loading, wrap the full tool list:
//
//	eager, deferred := advanced.SplitTools(allProviderTools, allSDKTools)
//	// send 'eager' to provider, keep 'deferred' for ToolSearchTool
package advanced
