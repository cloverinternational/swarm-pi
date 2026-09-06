# TROUBLESHOOTING — real diagnostic codes and error strings

Every code/message below is quoted (or copied verbatim, with `%q`/`%s`
placeholders left in) from the actual `newDiag(...)` calls in `harness/*.go`
and `fmt.Errorf("harness: ...")` calls in `client/harness_*.go` — not
paraphrased from memory. Search the cited file for the code/string if you
want to see the exact check that produced it.

`harness.Compile`/`harness.CompileBytes` return an error that is (or wraps)
`harness.Diagnostics` — a sorted list of `harness.Diagnostic`, each with a
stable `Code`, a human `Message`, and `SourcePath`/`FieldPath`. Printing the
error (`err.Error()`) already includes all of this; you do not need to type-
assert to `Diagnostics` just to see what went wrong.

## Compile-time (harness package) failures

### Version / kind

- `harness.version.missing` — "apiVersion is required; expected
  \"swarm.ai/v1alpha1\""
- `harness.version.malformed` — apiVersion or its version segment does not
  parse as `group/vN...`
- `harness.version.unknownGroup` — apiVersion's group is not `swarm.ai`
- `harness.version.futureMajor` — apiVersion names a newer major version than
  this build supports; message names the exact supported value
- `harness.version.unsupported` — apiVersion is in the `swarm.ai` group but is
  not exactly `swarm.ai/v1alpha1` (there is no other supported version — see
  [SCHEMA.md](SCHEMA.md))
- `harness.kind.missing` / `harness.kind.unknown` — `kind` must be exactly
  `"Harness"`

(`harness/version.go`'s `validateVersion`.)

### Required fields

- `harness.metadata.name.missing` — "metadata.name is required"
- `harness.provider.id.missing` — "provider.id is required"
- `harness.provider.model.missing` — "provider.model is required"
- `harness.agent.systemPrompt.missing` — "agent.systemPrompt is required; set
  exactly one of inline or file"
- `harness.agent.systemPrompt.exclusive` — both `inline` and `file` set
- `harness.agent.systemPrompt.empty` — neither set
- **`harness.agent.tools.required`** (the most common first-manifest
  failure) — "agent.tools is required for the primary agent; use \[\] to
  select zero tools (this is different from omitting the field)". This fires
  when `agent.tools` is omitted entirely AND no `compatibility:` preset is
  set. Fix: either add `tools: []` (or a real ID list), or set
  `compatibility: minimal`/`tui-v1` instead.

(`harness/plan.go`.)

### The DEFER-DISCOVER catalog rejection

- `harness.agent.tools.deferDiscover` — "capability %q has policy class
  DEFER-DISCOVER and cannot be selected: its exact capability/lifecycle
  contract is unresolved (...). There is no acknowledgement or override that
  enables it; it must first be reclassified in the capability catalog."

This is a HARD, unconditional reject (`checkCapabilityPolicy`,
`harness/plan.go`) — unlike `NEVER-DEFAULT`, there is no acknowledgement flag
that makes a `DEFER-DISCOVER` capability selectable. If you hit this, the
capability is not available via `agent.tools` at all yet; check
`harness/presets.go`'s `tuiV1Shortfall` for whether it is tracked as a known
gap.

### NEVER-DEFAULT without acknowledgement

- `harness.agent.tools.neverDefault.unacknowledged` — "capability %q has
  policy class NEVER-DEFAULT (...) and requires an explicit named posture:
  add %q to permissions.acknowledgeNeverDefault. Selecting it in tools alone
  is not sufficient". Fix: add the exact same ID to
  `permissions.acknowledgeNeverDefault`.
- `harness.permissions.acknowledgeNeverDefault.unused` — the inverse: an ID
  is acknowledged but not actually selected in any agent's `tools:` (a
  dangling acknowledgement). Fix: remove the acknowledgement or select the
  capability.
- `harness.agent.tools.credentialUnbound` — a selected capability is
  credential-bound but the document declares no `provider.credential` or
  `profiles[].credential` at all. Fix: declare one.

### The preflight binding-refusal error

Not a `harness.*` diagnostic — this is `client`'s runtime preflight, which
runs against an ALREADY-COMPILED plan, so it can surface after
`harness.Compile` has already succeeded:

```
harness: preflight refused: N required runtime binding(s) not supplied by the selected interface
  - <binding-kind> [no client binding interface is defined for this kind]   (only when unsupported)
      requiredBy: <ids that need it>
      reason: <why>
  supply the missing implementation(s) via client.WithHarnessBindings, or change the manifest so they are not required
```

(`client.HarnessPreflightError.Error()`, `client/harness_bindings.go`, wraps
the sentinel `client.ErrHarnessBindingUnsatisfied`.) This fires when
`plan.RequiredRuntimeBindings()` (e.g. a schedule needing a clock/occurrence
store) names a binding the host never supplied via
`client.WithHarnessBindings`. Fix: either supply the implementation, or
change the manifest so it is not required. `client.PreflightHarnessPlan` is
the exported entry point a host can call directly to get this refusal BEFORE
even attempting construction.

### The restart-required vs forbidden distinction

`client.ApplyHarnessPlan` (`client/harness_apply.go`) hot-applies a NEW plan
onto an already-constructed harness client and classifies every changed
field into exactly one of three disjoint sets:

- **Applied** — changed and safely hot-swappable; the running client now
  reflects it.
- **RestartRequired** — changed but only safe to apply via a fresh
  construction; nothing is mutated, and the call returns
  `client.ErrHarnessRestartRequired`. This is not a failure of your manifest
  — it just means this particular field (e.g. provider identity) cannot be
  swapped on a live client.
- **Forbidden** — the change is refused outright; the call fails with
  `harness: apply rejected: forbidden change(s): <field list>`
  (`client/harness_apply.go`), and — like RestartRequired — nothing is
  mutated (fail-closed: any forbidden change blocks the WHOLE apply, not just
  that field).

Other `ApplyHarnessPlan` errors you may see verbatim:
`"harness: ApplyHarnessPlan: nil newPlan"`,
`"harness: hot apply failed, prior plan restored: %w"` (an apply failed
partway through and the client was rolled back to the prior plan — the
message names the underlying cause),
`"harness: apply failed (%v) AND prior-plan restore failed (%v)"` (the rare
case where even rollback failed; treat the client as unusable and
reconstruct it).

### The yolo-without-`WithHarnessAllowYolo` failure

- `"harness: approvalMode %q requires an explicit named posture
  (client.WithHarnessAllowYolo); refusing to auto-approve"`
  (`client/harness_plan.go`'s `validateHarnessApprovalMode`). A manifest may
  declare `permissions.approvalMode: yolo` — `harness.Compile` never rejects
  it — but `client.WithHarnessPlan` refuses to HONOR it unless the host also
  passes `client.WithHarnessAllowYolo()` at construction. Fix: either change
  the manifest's approval mode, or add that option (a deliberate, code-level
  opt-in — never implied by the manifest alone).
- `"harness: unknown approvalMode %q; must be one of interactive, readonly,
  yolo"` — a typo/unsupported value in `permissions.approvalMode`.

## Hooks-specific compile failures (`harness/hooks.go`)

- `harness.hooks.id.missing` / `.id.duplicate`
- `harness.hooks.event.missing`
- `harness.hooks.scope.missing` / `.scope.invalid` (must be one of
  `global`/`interface`/`agent`/`tool`)
- `harness.hooks.matcher.forbidden` (matcher set with `scope: global`) /
  `.matcher.missing` (matcher required for any other scope)
- `harness.hooks.type.missing` / `.type.invalid` (must be one of
  `command`/`script`/`http`)
- `harness.hooks.source.oneOf` — "hook entry must set exactly one of command,
  path, url"
- `harness.hooks.source.typeMismatch` — declared `type` does not match which
  of `command`/`path`/`url` is actually set
- `harness.hooks.timeoutSeconds.invalid` (negative)
- `harness.hooks.environment.empty` / `.environment.duplicate`
- `harness.hooks.url.invalid` (type=http; must be a well-formed absolute URL)
- `harness.hooks.unreadable` / `.pathIsDir` (type=script; the referenced file
  could not be read, or is a directory instead of a file)

At the `client` construction layer (only reachable after a plan already
compiled cleanly): `"harness: hook %q: type=http is not yet supported on the
closed client path"` (`client/harness_hooks.go`) — a `type: http` hook
compiles, but the closed client-construction path does not yet execute it.

## MCP-specific compile failures (`harness/mcp.go`)

- `harness.mcp.id.missing` / `.id.duplicate`
- `harness.mcp.type.oauthUnsupported` — `type: oauth` is recognized ONLY to
  be rejected with this specific, actionable message (an OAuth-authenticated
  server cannot be resolved without a working auth flow yet); use
  `stdio`/`http`/`sse` instead
- `harness.mcp.type.missing` / `.type.invalid`
- `harness.mcp.stdio.command.missing`
- **`harness.mcp.stdio.unpinned`** — "mcp stdio entry must reference a
  pinned command (an absolute path, or a package argument with an explicit
  version pin like pkg@1.2.3) or explicitly set unsafeDevMode: true to bypass
  this check" — see [SECURITY.md](SECURITY.md)'s PINNING RULE section
- `harness.mcp.stdio.url.notAllowed` / `.headers.notAllowedForStdio`
- `harness.mcp.remote.command.notAllowed` / `.workDir.notAllowedForNonStdio`
  (command/args/workDir are stdio-only)
- `harness.mcp.url.missing` / `.url.invalid` (type=http/sse)
- `harness.mcp.timeoutSeconds.invalid`
- `harness.mcp.environment.empty` / `.environment.duplicate`
- `harness.mcp.headers.keyEmpty` / `.headers.unknownEnvRef` (a header value
  must reference a name already declared in that entry's own `environment`
  allowlist — there is no way to inline a literal header value)
- `harness.mcp.tools.bothSet` — `tools` and `excludeTools` both set (choose
  one)
- `harness.mcp.workDir.notDir` (workDir resolved but is not a directory)

## Diagnostic-shaped decode failures (`harness/strict_decode.go`)

- `harness.decode.multipleDocuments` — more than one YAML document in one
  file
- `harness.decode.syntax` — not valid YAML/JSON at all
- `harness.decode.unknownField` — an unrecognized key anywhere in the
  document (DisallowUnknownFields is recursive); message names the field
- `harness.decode.trailingData` — extra data after the one JSON value
- `harness.load.notFound` — no explicit path given and `./harness.yaml` does
  not exist (parents are NEVER searched) — or an explicit path that does not
  exist
- `harness.load.unreadable` — file exists but could not be read

## Where to look for anything not listed here

This document intentionally does not enumerate every diagnostic in
`harness/agents.go`, `harness/budgets.go`, or `harness/schedules.go` — those
sections have dozens of dimension-specific checks (retry taxonomies, cron
field bounds, delegate cycles, ...). Every one of them follows the exact same
shape as the entries above: `grep -n 'newDiag(' harness/*.go` for the
compile-time list, `grep -n 'fmt.Errorf("harness:' client/harness_*.go` for
the construction-time list. Every message is written to be actionable on its
own; if one is not, that is a bug in the diagnostic, not a gap in this
document.
