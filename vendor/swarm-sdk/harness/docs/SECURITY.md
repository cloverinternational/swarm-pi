# SECURITY — the supply-chain policy for executable hooks and MCP servers

## Read this before running a harness manifest you did not write yourself

**A harness manifest with a `type: command` or `type: script` hook, or a
`type: stdio` MCP server, grants ARBITRARY CODE EXECUTION with the full
privileges of the host process.** Treat such a manifest with the exact same
trust level you would give a shell script you downloaded and are about to
run — because, mechanically, that is what it is:

- A `type: command` hook runs its resolved command via `sh -c <command>`
  (`client/harness_hooks.go`'s `harnessCommandHook.OnEvent`, currently at
  line 362 of that file) whenever the declared `event`/`scope`/`matcher`
  fires during a run.
- A `type: script` hook reads and executes a manifest-relative script file
  the same way.
- A `type: stdio` MCP server spawns `McpServerSpec.Command` (with
  `McpServerSpec.Args`) as a local subprocess (`client/harness_mcp.go`) when
  the client starts that server.

Neither surface is sandboxed, resource-limited beyond a wall-clock timeout,
or restricted in what it can read, write, or reach on the network. Either one
can read any file the host process can read, write any file it can write,
and make any network call it can make — indistinguishable, from the
process's point of view, from code the operator wrote themselves.

`harness.Compile` intentionally remains policy-neutral and will compile either
surface. The client package now provides an **opt-in, host-owned exact-digest
gate**. A host enables it with
`client.WithHarnessRequireExecutableConsent(plan.Digest())`. When enabled, an
executable plan is refused before resource creation unless the supplied digest
strictly matches the compiled plan. The manifest cannot acknowledge itself.

## What DOES exist: `Plan.HasExecutableSupplyChain()`

`harness/supplychain.go` (this same phase) adds a read-only introspection
API so a host can inspect and approve the exact executable plan:

```go
plan, err := harness.Compile(path)
if err != nil { /* ... */ }

if plan.HasExecutableSupplyChain() {
    report := plan.ExecutableSupplyChain()
    // report.Hooks: every type=command/type=script hook (id, event, scope,
    //               kind, and a content hash — never the command text)
    // report.MCP:   every type=stdio MCP server (id, command, args — shown
    //               plainly; they are operational identifiers, not secrets)
    //
    // ... surface this to the operator and require explicit confirmation ...
}

c, err := client.New(
    client.WithHarnessPlan(plan),
    client.WithHarnessRequireExecutableConsent(plan.Digest()),
)
```

This is metadata only. `HasExecutableSupplyChain()`/`ExecutableSupplyChain()`
have no side effect, block nothing, and are safe to call on any compiled
plan. The harness package itself remains policy-neutral. Enforcement lives in
the client because consent is host state, not manifest state.

The gate is opt-in for compatibility. Once a client opts in, the posture is
persistent: hot apply must pass the new plan's exact digest in
`ApplyHarnessOptions.ExecutableConsentDigest`, and `WatchHarness` only
auto-applies executable plans whose digest was present in
`ReloadPolicy.ExecutableConsentDigests` when the watch began. Missing,
malformed, mismatched, or stale consent returns
`client.ErrHarnessExecutableConsentRequired` without mutating the live plan.

## What the closed-construction path DOES protect against

The consent gate acknowledges declared executable capability; it does not
sandbox that capability. The guarantees below remain separate and enforced.

### Env allowlisting for command hooks — intersected at invocation time, never persisted

A `type: command` hook's `environment:` list is an ALLOWLIST of environment
variable **names**, never values (`HookSpec.Environment`,
`harness/hooks.go`). At invocation time — not at plan compile/load time, and
never persisted anywhere — `harnessCommandHook.OnEvent`
(`client/harness_hooks.go`) builds the child process's environment as the
INTERSECTION of that allowlist against the live `os.Environ()` of the host
process at the moment the hook fires. A hook can therefore never see an
environment variable it did not explicitly name, and naming one grants it
only whatever value happens to be ambient in the host process at that exact
moment — nothing is baked into the plan or the manifest.

### Exit-code-2 block semantics

A hook's exit code changes execution flow, not just logging
(`harnessCommandHook.OnEvent`): exit code `2` BLOCKS whatever the hook is
gating, with the hook's stderr (falling back to stdout) surfaced as the block
reason. Any other non-zero exit continues the run, surfacing the hook's
stdout as a non-blocking message. This mirrors the standard client's
`ShellHook` `block_exit2` default for consistency across both construction
paths.

### Hook execution timeout

Every hook has a resolved `TimeoutSeconds` (default 30, `harness/hooks.go`'s
`defaultHookTimeoutSeconds`), enforced via `context.WithTimeout` around the
child process (`client/harness_hooks.go`). A hung hook is killed and reported
as a timeout message rather than hanging the run indefinitely — this bounds
*how long* a hook can run, but bounds nothing about *what* it does while
running.

### The closed-construction guarantee — no ambient credentials leak into a hook's env

Because `client.WithHarnessPlan` refuses all ambient config
(`client/harness_option.go`'s `WithHarnessPlan` doc comment: "no auto-config,
no INDEX.md, no ambient credentials, no default tools, no hooks, no
skills/autoskills, no MCP discovery, no plugins, and no implicit provider
fallback"), a hook's or an MCP server's child environment can never contain a
credential the manifest did not explicitly declare a path to. This does not
protect against a `type: command` hook running arbitrary shell logic — it
only means the AMBIENT process environment cannot smuggle in something the
manifest never asked for.

### The sealed-provider mechanism — the credential-side analogue

`client/harness_plan.go`'s `harnessSealedProviders` is the closest existing
analogue to a supply-chain gate, but for CREDENTIALS rather than executable
hooks/MCP servers: it is an explicit, auditable allowlist of provider
factories (`anthropic`, `openai` today) that are PROVEN to consult no ambient
environment beyond the explicit resolved key. Any other provider — even one
the manifest names validly — is rejected at construction with an explicit
error rather than silently falling back to whatever ambient credential
happens to be lying around. `sealHarnessProvider`
(`client/harness_plan.go`) additionally fails closed on an empty resolved
key: a declared-but-unresolvable credential reference never silently
degrades to "read the ambient key instead". The parallel to draw: sealed
providers close ONE specific ambient-leak channel (credentials); the opt-in
supply-chain consent gate addresses a DIFFERENT risk (operator acknowledgement
of arbitrary local execution). They are not the same mechanism and one does
not substitute for the other.

## Summary table

| Surface | Executes local code? | Compile-time gate? | Construction-time gate? | Inspectable before construction? |
|---|---|---|---|---|
| `hooks[].type: command` | Yes (`sh -c`) | No | Opt-in exact plan digest | Yes — `Plan.HasExecutableSupplyChain()` |
| `hooks[].type: script` | Yes (script file) | No | Opt-in exact plan digest | Yes — `Plan.HasExecutableSupplyChain()` |
| `hooks[].type: http` | No (network call) | No | No | N/A — excluded by design, see `harness/supplychain.go` |
| `mcp[].type: stdio` | Yes (subprocess) | Partial — PINNING RULE (`harness/mcp.go`) rejects an unpinned command unless `unsafeDevMode: true` | Opt-in exact plan digest | Yes — `Plan.HasExecutableSupplyChain()` |
| `mcp[].type: http`/`sse` | No (network client) | No | No | N/A — excluded by design |
| `provider.credential` | N/A | N/A | Yes — `harnessSealedProviders` allowlist + fail-closed empty-key check | Yes — `Plan.Credential()`/`Explain()`'s `CredentialState` |

The PINNING RULE is worth calling out precisely because it is easy to
over-read: it only rejects an OBVIOUSLY unpinned stdio command (a bare
floating package name, or `pkg@latest`) — it is a heuristic, not a
package-manager-aware artifact verifier, and it cannot detect (for example) a
git-branch ref smuggled in as a fake "version". It also has an unconditional
escape hatch (`unsafeDevMode: true`). It reduces the chance of an
accidentally-unpinned MCP dependency; it is not a supply-chain integrity
guarantee on its own.
