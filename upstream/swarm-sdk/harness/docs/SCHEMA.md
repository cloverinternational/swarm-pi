# SCHEMA — the manifest shape `harness.Load`/`harness.Compile` accept

This is a field-by-field reference of the on-disk `Document` type
(`harness/types.go` and the section files it references), derived directly
from the Go struct tags and `resolve*` functions that decode and validate
each section. Every struct here is decoded with
`json.Decoder.DisallowUnknownFields` recursively (`harness/strict_decode.go`),
so an unknown key at ANY level is a compile-time error, not a silently
ignored typo. YAML input is converted to canonical JSON first
(`configformat.ToJSON`), so the same `json:` tags below drive both YAML and
JSON manifests identically.

## apiVersion / kind

```yaml
apiVersion: swarm.ai/v1alpha1   # required; group "swarm.ai", version "v1alpha1"
kind: Harness                   # required; exactly "Harness"
```

`swarm.ai/v1alpha1` is currently the **only** schema version that has ever
shipped. `harness/migrate.go`'s `migrationRegistry` — the mechanism that
would carry a document from an older `apiVersion` forward — is intentionally
**empty** (`TestMigrationRegistryIsEmpty` asserts this fact rather than
leaving it implicit). Do not write a manifest expecting migration support;
none exists yet. An `apiVersion` naming a newer major version fails with
`harness.version.futureMajor`; anything else under the `swarm.ai` group other
than `v1alpha1` fails with `harness.version.unsupported`.

## metadata

```yaml
metadata:
  name: my-harness          # required, non-empty
  description: "..."        # optional
  labels: {key: value}      # optional
```
(`harness/types.go`'s `Metadata`.)

## runtime

```yaml
runtime:
  workspace: ./workspace     # optional; manifest-relative, resolved against
  storage: ./storage         #   the MANIFEST DIRECTORY, never the process CWD
  watch: false                # optional
```
(`harness/types.go`'s `Runtime`.)

## provider

```yaml
provider:
  id: anthropic              # required
  model: claude-sonnet-4-5   # required
  baseURL: https://...        # optional
  credential:                 # optional; a Ref one-of (see "Ref" below)
    env: ANTHROPIC_API_KEY
```
(`harness/types.go`'s `Provider`.) Note: compiling a manifest never checks
that `provider.id` is one client construction can actually build — that check
(`harnessSealedProviders`) lives in `client`, not here. See
[SECURITY.md](SECURITY.md) and [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

### Ref — the one-of used by every credential/env reference

```go
type Ref struct {
    Env    string `json:"env,omitempty"`
    File   string `json:"file,omitempty"`
    Vault  string `json:"vault,omitempty"`
    Inline string `json:"inline,omitempty"`
}
```
(`harness/refs.go`.) Exactly one of `env`/`file`/`vault`/`inline` must be set;
zero or more than one is a diagnostic (`harness.ref.empty` /
`harness.ref.multiple`). A resolved credential's VALUE never appears in
`Plan.Explain()`/`ExplainJSON()`/`Digest()`/`Provenance()` — only its
provenance label (e.g. `"env:NAME"`) does.

## agent

```yaml
agent:
  systemPrompt:               # required; one-of inline|file
    inline: "..."             #   OR
    file: ./prompt.md
  tools: []                   # required UNLESS `compatibility:` is set (see below);
                               #   tri-state: omit key = invalid, [] = zero tools,
                               #   [id, ...] = exact capability IDs
  limits:                     # optional
    maxOutputTokens: 4096
    maxTurns: 20
    timeoutSeconds: 300
```
(`harness/types.go`'s `Agent`/`SystemPrompt`/`Limits`.) `tools:` values are
exact, stable, dotted capability IDs from the closed catalog
(`harness/catalog.go`'s `Catalog()`/`LookupCapability`) — never a runtime tool
name. Each selected ID is checked against its `PolicyClass`
(`checkCapabilityPolicy`, `harness/plan.go`):

- `DEFER-DISCOVER` — hard rejected, no override, ever
  (`harness.agent.tools.deferDiscover`).
- `NEVER-DEFAULT` — selecting it in `tools:` is necessary but not sufficient;
  its exact ID must ALSO appear in
  `permissions.acknowledgeNeverDefault` (`harness.agent.tools.neverDefault.unacknowledged`).
- Any class with `credentialBound: yes` in the catalog additionally requires
  the document to declare SOME credential reference
  (`provider.credential` or a `profiles[].credential`) —
  `harness.agent.tools.credentialUnbound`.

### compatibility (presets)

```yaml
compatibility: minimal   # or: tui-v1
```
(`harness/types.go`'s `Compatibility` field, resolved by
`harness/presets.go`.) When set, it supplies the primary agent's EFFECTIVE
`tools:` selection as a copy of the named preset's ID list, which then flows
through the exact same validation as a hand-written list — a preset EXPANDS,
it never bypasses (`harness/presets.go`'s D1). Setting `compatibility:`
together with an explicit `agent.tools` is a compile-time error
(`harness.compatibility.conflictsWithTools`) — they never silently merge.
`KnownPresetNames()` names the closed set (today: `minimal`, `tui-v1`); any
other value fails with `harness.compatibility.unknown`.

- `minimal` — exactly the five capability IDs every Swarm root registers
  unconditionally: `forge.read`, `forge.apply_patch`, `forge.unified_grep`,
  `forge.undo`, `forge.semantic_rename`.
- `tui-v1` — every TUI-registered-by-default capability that is both
  class-legal and constructible on the harness client path today. What it
  deliberately does NOT reproduce is enumerated by
  `Plan.CompatibilityShortfall()`, each entry carrying a `cause` of
  `defer-discover`, `host-binding-required`, or `no-manifest-section`.

## permissions

```yaml
permissions:
  approvalMode: interactive          # interactive | readonly | yolo; default "interactive"
  workspaceBoundary: true            # optional
  allowMutation: false               # optional
  acknowledgeNeverDefault: []        # exact NEVER-DEFAULT capability IDs (see above)
```
(`harness/types.go`'s `Permissions`.) `approvalMode: yolo` compiles fine — the
harness package never rejects it — but `client.WithHarnessPlan` refuses to
HONOR it unless the host also passes `client.WithHarnessAllowYolo()`; see
[TROUBLESHOOTING.md](TROUBLESHOOTING.md). Every ID in
`acknowledgeNeverDefault` must be a known, actually-`NEVER-DEFAULT`-classed
capability that is ALSO selected somewhere in `tools:` — a dangling
acknowledgement (acknowledged but never selected) is
`harness.permissions.acknowledgeNeverDefault.unused`.

## interfaces (presentation-only)

```yaml
interfaces:
  default: tui        # optional
  tui:
    theme: dark
  print:
    format: json
```
(`harness/types.go`'s `Interfaces`/`InterfaceTUI`/`InterfacePrint`.)
Interface selection must never change provider, prompt, tools, or
permissions — it is presentation-only by construction (there is no field here
that resolveTools or the provider/credential path reads).

## skills

```yaml
skills:
  - id: my-skill                # sequence form: one or more SkillEntry
    path: ./skills/my-skill     # optional; manifest-relative dir or SKILL.md
# OR the object form:
skills:
  entries: [{id: my-skill}]
  searchRoots: [./skills]
```
(`harness/skills.go`'s `SkillsSection`/`SkillEntry`.) Declaration + resolution
(content-hashed) only — no skill is loaded or invoked by compiling.

## hooks

```yaml
hooks:
  - id: my-hook
    event: tool.before          # free-form dot-notation event name
    scope: global                # global | interface | agent | tool
    matcher: ""                  # required when scope != global
    type: command                 # command | script | http (exactly one of
    command: "echo hi"            #   command/path/url must be set, matching type)
    priority: 0
    timeoutSeconds: 30           # default 30; must be >= 0
    enabled: true                # default true
    environment: [PATH]          # ALLOWLIST of env var NAMES, never values
```
(`harness/hooks.go`'s `HookEntry`/`HookSpec`.) **`type: command` and
`type: script` execute local code; `type: http` performs a network call.**
See [SECURITY.md](SECURITY.md) for what that means, and use
`Plan.ExecutableSupplyChain()`/`Plan.HasExecutableSupplyChain()`
(`harness/supplychain.go`) to inspect which hooks on a compiled plan actually
execute code. An inline `command`'s literal text is retained on the Plan for
a later trusted consumer (`Plan.RevealHookCommand`) but is **never** exposed
by `Explain`/`Digest`/`Provenance`/JSON — only its SHA-256 hash and byte
count (`HookSpec.CommandHash`/`CommandBytes`) are.

## mcp

```yaml
mcp:
  - id: my-server
    type: stdio                  # stdio | http | sse ("oauth" is recognized
    command: /usr/bin/my-server  #   only to be rejected with an explicit,
    args: ["--flag"]             #   actionable diagnostic)
    workDir: ./mcp                # stdio only; manifest-relative
    timeoutSeconds: 30            # default 30
    enabled: true                 # default true
    environment: [PATH]           # ALLOWLIST of env var NAMES, never values
    tools: []                     # optional allowlist (mutually exclusive
    excludeTools: []              #   with excludeTools)
    unsafeDevMode: false          # stdio only; explicit escape hatch for the
                                   #   PINNING RULE below
```
(`harness/mcp.go`'s `McpEntry`/`McpServerSpec`.) **`type: stdio` spawns
`command` as a local subprocess; `type: http`/`type: sse` are network
clients.** Same code-execution distinction as hooks above — see
[SECURITY.md](SECURITY.md).

**PINNING RULE** (`isPinnedStdioCommand`, `harness/mcp.go`): a `type: stdio`
entry's `command`/`args` must be either an absolute path, or carry an
explicit non-floating version pin of the form `name@1.2.3` (NOT `@latest`,
`@next`, `@canary`, or `@*`) — otherwise compilation fails with
`harness.mcp.stdio.unpinned` unless `unsafeDevMode: true` is set explicitly.

`command`/`args` are shown PLAINLY on the redacted `McpServerSpec` — they are
operational identifiers (like `agent.tools`), not attacker-controllable free
text the way an inline hook shell command is (`harness/mcp.go`'s
`McpServerSpec` doc comment). Only actual environment VALUES never enter the
harness surface; `environment`/`headers` carry NAMES/references only.

## profiles / fallback / agents (subagents)

```yaml
profiles:
  - id: fast
    provider: anthropic
    model: claude-haiku
    credential: {env: ANTHROPIC_API_KEY}   # profiles reject `inline` credentials
    limits: {}
fallback:
  enabled: false          # must be explicitly true to have any effect
  chain: [fast]            # ordered list of profiles[] ids
  retryOn: [timeout]        # closed taxonomy: timeout|rateLimit|providerError|
                             #   networkError|overloaded
  cooldownSeconds: 0
  maxAttempts: 0            # 0 -> defaults to len(chain)
  onExhaustion: fail         # fail | degrade
agents:
  - id: reviewer
    profile: fast            # optional; references profiles[].id
    systemPrompt: {inline: "..."}
    tools: []                # optional override; tri-state (omit = "no override")
    skills: []
    hooks: []
    limits: {}
    fallback: {}
    delegates: [other-agent-id]
```
(`harness/agents.go`.) Declaration + resolution only — no provider, client,
or subagent is constructed by compiling. `profiles[].credential` explicitly
FORBIDS `inline` (`harness.profiles.credential.inlineForbidden`) — unlike
`provider.credential`, which allows it. `agents[].delegates` is checked for
cycles at compile time (`harness.agents.delegates.cycle`).

## budgets

```yaml
budgets:
  run:         {maxRuns: 10, onExhaustion: block}
  turn:        {maxTurns: 20, onExhaustion: block}
  outputToken: {maxCumulativeOutputTokens: 100000, onExhaustion: block}
  totalToken:  {maxTotalTokens: 200000, onExhaustion: block}
  toolCall:    {maxToolCalls: 50, onExhaustion: block}
  cost:        {maxCostMicroUSD: 500000, onExhaustion: block}
  wallClock:   {maxSeconds: 3600, onExhaustion: block}
  acknowledgeObserved: []   # kinds whose enforcement class is "observed only"
```
(`harness/budgets.go`.) Each of the seven kinds is independently optional.
Compiling this section CLASSIFIES and validates every declared bound against
what the runtime can actually enforce — it enforces nothing itself (that is a
later, client-side concern).

## schedules

```yaml
schedules:
  - id: nightly
    cron: "0 2 * * *"
    timezone: America/New_York        # required explicit IANA zone
    overlap: skip                      # skip|allowConcurrent|queue|cancelPrevious
    misfire: drop                      # drop|runImmediately|backfillAll
    retry: {policy: none}              # none|fixedDelay|exponentialBackoff
    concurrency: {policy: unlimited}   # unlimited|maxConcurrent
    target: {kind: agent, id: reviewer}
    approvalPosture: deny              # unattended-run safety posture
    resultSink: {kind: discard}        # discard|named
    acknowledgeUnenforced: []
```
(`harness/schedules.go`.) Declaration + resolution only — no scheduler,
cron registration, clock read, or goroutine is started by compiling. Every
operational dimension is classified with an `EnforcementClass` on the
resolved `ScheduleSpec`, and `Plan.RequiredRuntimeBindings()` names the host
capabilities (e.g. a clock, an occurrence store) a schedule needs before it
can actually run.

## workflows

```yaml
workflows:
  - id: my-workflow
    file: ./workflows/my-workflow.yaml
    expectedVersion: "1"
```
(`harness/workflows.go`'s `WorkflowEntry`.) A TYPED REFERENCE to an external
workflow manifest — id + file + expected version — never a second embedded
graph language. The referenced file's content hash, adapted definition
(enum values, ids, counts, prompt HASHES only), and required runtime bindings
are folded onto the Plan; no `WorkflowEngine`, group, or agent is constructed
by compiling.

## What is intentionally NOT in this schema

`harness/presets.go`'s `tuiV1Shortfall` enumerates, with citations, every TUI
behavior that has genuinely no manifest section at all today (for example:
CLAUDE.md/AGENTS.md ambient context assembly, permission POLICY objects
beyond the four scalar fields above, autoskills trigger budgets, plugin
composition, compaction auto-config, observability wiring). That list is the
authoritative "not yet expressible" reference — do not assume a YAML key
exists just because the underlying TUI feature does.
