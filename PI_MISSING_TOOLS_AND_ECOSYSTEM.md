# Pi gaps for Swarm parity and ecosystem candidates

Status: research inventory. No third-party Pi package has been installed or
trusted by this repository.

This report compares the source-focused mirrors in `upstream/` with the public
Pi ecosystem observed on 2026-08-31. GitHub star counts below are discovery
signals, not quality or security ratings. They can change and should be
rechecked before adoption.

## Executive conclusion

Pi already has the core coding loop needed by Forge:

| Capability | Pi native surface | Swarm port decision |
|---|---|---|
| bounded file read | `core/tools/read.ts` | reuse Pi semantics; add Swarm policy/path wrapper only if parity requires it |
| file write | `core/tools/write.ts` | reuse execution and renderer; enforce Swarm mutation policy externally |
| edit/patch | `core/tools/edit.ts`, `edit-diff.ts` | reuse where the input/result contract matches |
| search | `core/tools/grep.ts`, `find.ts`, `ls.ts` | reuse; do not create duplicate search tools |
| shell | `core/tools/bash.ts` | reuse only behind a supervised sandbox/routing policy |
| prompt changes | `before_agent_start` | add Forge assembly and provenance |
| pre/post tool governance | `tool_call`, `tool_result` | add one centralized Swarm policy/hook coordinator |
| tool registration/rendering | `registerTool`, `execute`, progress, `renderCall`, `renderResult` | add thin Forge adapters |
| active-tool filtering | `setActiveTools`, SDK `tools`/`excludeTools` | use for closed profiles, with final authorization at execution |
| sessions/persistence | Pi `SessionManager`, custom session entries | add Swarm identity, policy, provenance, and audit entries |

The main missing tools are not replacements for Pi's built-ins. They are
Swarm-specific capabilities:

- capability-aware authorization and workspace/process/network enforcement;
- background subagents and deterministic workflows;
- native MCP discovery/routing;
- structured user questions and approval flows;
- task/todo and plan-mode semantics;
- web search/fetch and browser automation;
- A2A, ACP, serve, attach, and daemon bridges;
- durable cross-session memory and audit/provenance.

## Native Pi inventory

The local Pi mirror contains these built-in coding tools:

```text
upstream/pi-mono/packages/coding-agent/src/core/tools/
  bash.ts
  edit.ts
  edit-diff.ts
  find.ts
  grep.ts
  ls.ts
  read.ts
  write.ts
```

The local SDK documentation also defines `tools`, `excludeTools`,
`customTools`, and extension-registered tools:

- `upstream/pi-mono/packages/coding-agent/docs/sdk.md:474–475`
- `upstream/pi-mono/packages/coding-agent/docs/sdk.md:551–559`
- `upstream/pi-mono/packages/coding-agent/docs/packages.md:169–185`

The important security boundary is explicit in Pi's package documentation:
extensions and skills execute with broad host access and must be reviewed
before installation. Therefore an ecosystem package can provide functionality,
but it cannot substitute for the Swarm policy and sandbox layer.

## Swarm-to-Pi gap inventory

The Swarm snapshot contains substantially more than its Forge filesystem
catalog. Relevant tool families are visible under:

```text
upstream/swarm-sdk/internal/tools/
  advanced
  browser
  builtin
  codemode
  deepwiki
  forge
  history
  mcp
  projectmemory
  skilltools
  swarm
  swarmtools
  web_fetch
  websearch
  xai
```

### 1. Authorization and sandboxing — genuine gap

Swarm's tool contract carries required capabilities in
`upstream/swarm-sdk/internal/tools/tool.go:80–86`, with policy evaluation in
`upstream/swarm-sdk/internal/tools/permission_engine.go`. Pi's
`tool_call` event can block or mutate a call, but the extension API is not a
host security boundary.

Required addition:

```text
swarm-policy
  classify(tool, args, workspace, operation)
  authorize or block
  emit redacted audit outcome

supervised executor
  enforce filesystem/process/network boundary
  own credentials and dangerous-command policy
```

This should be implemented in-repository rather than delegated to a
third-party extension. A prompt instruction or confirmation widget is not
equivalent to Swarm authorization.

### 2. Subagents, background execution, and workflows — missing in Pi core

Pi's public site states that Pi does not ship subagents and recommends
extensions or process orchestration. Swarm has subagent, background-manager,
wait, steering, and deterministic child-binding behavior under
`upstream/swarm-sdk/internal/tools/builtin/` and related agent packages.

Required addition:

- deterministic parent/child/session IDs;
- bounded concurrency and cancellation;
- optional isolated worktrees or supervised processes;
- durable completion messages;
- explicit tool/policy profiles for children.

### 3. MCP — missing in Pi core, ecosystem choices exist

Swarm includes MCP under `internal/tools/mcp`. Pi's core documentation
describes extension/package loading, but MCP is intentionally an extension
concern rather than a built-in tool family.

Required addition:

- configured-server allowlist;
- lazy discovery and tool-name namespace;
- credential/network policy;
- server lifecycle and failure taxonomy;
- closed-harness exclusion of undeclared servers.

### 4. Web search, fetch, and browser — missing in Pi core

Swarm has `websearch`, `web_fetch`, `browser`, `xai`, and related tools.
Pi's built-in tool directory has no web or browser tool. These should be
adapters with explicit network policy and source/citation handling.

### 5. Tasks, todos, plan mode, questions, memory — missing or extension-owned

Pi's minimal core intentionally leaves plan mode and built-in todos to
extensions or file conventions. Swarm has task/todo, planning, user-question,
project-memory, and history surfaces. These should be separate semantic
extensions, not hidden inside the Forge tool adapter.

### 6. Remote control and peer protocols — genuine bridge gap

Swarm's A2A, ACP, serve, attach, control socket, and daemon behavior has no
direct TUI equivalent. Keep these in `swarm-bridge`, using stable session and
agent IDs. Do not represent a daemon as a local renderer or infer identity
from the latest session.

## Public ecosystem candidates

### Subagents and orchestration

`tintinweb/pi-subagents`

- observed GitHub signal: approximately 1,030 stars and 219 forks;
- provides background/foreground agents, parallel execution, workflows,
  steering, persistent memory, and optional Git worktree isolation;
- directly overlaps Swarm's `subagent`, background, workflow, memory, and
  child-session requirements;
- candidate role: reference implementation or optional adapter, not a
  replacement for Swarm policy.

Repository: <https://github.com/tintinweb/pi-subagents>

### MCP

`nicobailon/pi-mcp-adapter`

- observed GitHub signal: approximately 1,371 stars and 300 forks;
- exposes a token-efficient discovery/dispatch adapter, lazy server startup,
  tool metadata caching, and interactive server management;
- candidate role: preferred ecosystem reference for MCP routing semantics;
- parity warning: its lazy discovery must be constrained by the Swarm closed
  profile so undeclared servers cannot become reachable.

Repository: <https://github.com/nicobailon/pi-mcp-adapter>

Other candidates observed:

- `mitsuhiko/pi-codemode-mcp` — approximately 37 stars; experimental
  code-mediated MCP access.
- `dmmulroy/pi-mcp` — approximately 24 stars; OpenCode-style MCP client.
- `irahardianto/pi-mcp-extension` — approximately 5 stars; direct MCP bridge.

The lower-star alternatives are useful for protocol comparison, but the
star-count evidence alone does not establish maintenance or security.

### Web, research, and browser

`nicobailon/pi-web-access`

- provides search, content extraction, browser/video-oriented capabilities,
  multiple provider fallbacks, and citation-oriented results;
- candidate role: reference for Swarm `web_search`/`web_fetch` behavior and
  provider fallback design;
- parity warning: remote hosted fetchers and browser-cookie access require
  explicit network/credential policy.

Repository: <https://github.com/nicobailon/pi-web-access>

`narumiruna/pi-extensions`

- observed GitHub signal: approximately 478 stars and 82 forks;
- contains independently installable packages for plan mode, Chrome
  DevTools, Firecrawl, worktrees, goals, status, and workflow utilities;
- candidate role: source of small, separable references rather than adopting
  the entire monorepo.

Repository: <https://github.com/narumiruna/pi-extensions>

### Skills and general workflow surface

`badlogic/pi-skills`

- observed GitHub signal: approximately 2,462 stars and 213 forks;
- provides Brave search, browser tools, Google APIs, transcription, and
  YouTube transcript skills;
- candidate role: optional skills source for research/productivity tools;
- parity warning: skills are instructions plus executable helpers, not
  authorization. Review each skill and apply the same closed-profile policy.

Repository: <https://github.com/badlogic/pi-skills>

### Plan mode and worktrees

`janvitos/pi-plan-build`

- observed GitHub signal: approximately 7 stars;
- provides persistent plan/build modes, guarded plan-file edits, approval,
  and clean-session handoff;
- candidate role: inspect for plan-mode acceptance tests and UX ideas.

Repository: <https://github.com/janvitos/pi-plan-build>

`narumiruna/pi-extensions` also includes `pi-plan-mode` and `pi-worktree`,
which are closer to independently installable building blocks.

## Adoption decision matrix

| Swarm need | Pi core | Candidate to study | Build/borrow decision |
|---|---|---|---|
| Forge read/write/edit/search | yes | none required | build only policy/path adapters |
| closed tool profile | partial | none trusted as authority | build `swarm-policy` |
| dangerous command/workspace boundary | no | package candidates exist | build supervised enforcement |
| subagents/workflows | no | `pi-subagents` | study/adapt behind Swarm IDs and policy |
| MCP | no | `pi-mcp-adapter` | study/adapt with closed allowlist |
| web search/fetch | no | `pi-web-access` | use as optional provider adapter after review |
| browser automation | no | `pi-web-access`, `pi-chrome-devtools` | optional adapter with explicit network policy |
| plan mode | no | `pi-extensions`, `pi-plan-build` | study UX; implement Swarm semantics locally |
| todos/tasks | no | `pi-extensions`, Pi upstream extensions | implement durable Swarm task contract |
| structured questions | no | Pi upstream `ask_user` extension | adapt interaction contract and validation |
| persistent memory | no | `pi-subagents`, `pi-extensions` | implement namespaced, redacted state |
| A2A/ACP/attach/daemon | no | no direct equivalent found | build external `swarm-bridge` |

## Recommended sequence

1. Keep Pi's native `read`, `write`, `edit`, `grep`, `find`, `ls`, and `bash`
   as the execution foundation.
2. Implement and test `swarm-policy` before importing any broad ecosystem
   package.
3. Add `swarm-prompt` and `swarm-hooks` around Pi's native lifecycle.
4. Port `forge.read` as the first custom tool and prove closed-mode filtering,
   workspace checks, and durable audit results.
5. Build the subagent contract after reviewing `pi-subagents`, retaining
   Swarm's identity, cancellation, and policy semantics.
6. Build MCP and web adapters only behind explicit manifests and network
   capabilities.
7. Add plan/tasks/memory and then the remote bridge.

## Verification and adoption gates

Before installing or vendoring a candidate:

- pin a commit or immutable release;
- inspect its license, dependencies, install scripts, and file/process/network
  behavior;
- run it in a disposable project with no production credentials;
- test tool allowlists and blocked calls;
- verify stdout/RPC cleanliness and session persistence;
- record the exact revision and any known limitation.

Popularity is useful for finding implementation prior art, but source review,
test coverage, release activity, and the ability to enforce Swarm's closed
profile determine whether a candidate is safe to adapt.

