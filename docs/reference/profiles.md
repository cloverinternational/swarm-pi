# Immutable runtime profiles

`swarm-contract` provides a small, offline-safe policy seam for Pi extensions.
`createRuntimeProfile` copies, de-duplicates, sorts, and freezes allowlists for
tools, skills, hooks, MCP servers, and capabilities. It also records context
file names, provider/model identity, prompt hash, and package provenance, then
computes a stable SHA-256 digest. It never stores prompt contents or credentials.

Use `assertAllowed(profile, kind, id)` at the final tool/MCP/hook boundary; tool
registration alone is not authorization. `hashPrompt` is intended for prompt
provenance (`sha256:<hex>`), and `provenance` records package identity without
secret values.

The model follows the upstream SDK's closed catalog and redacted provenance
rules (`upstream/swarm-sdk/harness/catalog.go`, `supplychain.go`, and
`plan.go`). This slice is deliberately host-agnostic: workspace/process/network
sandboxing remains a host policy and must be enforced before execution.

Verification: `taskmanage` build/tests pass (44 tests). The new package has
offline tests; dependency installation was unavailable in this environment, so
its TypeScript build was checked with the system compiler after adding a minimal
crypto declaration.
