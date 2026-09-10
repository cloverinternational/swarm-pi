# Pi-Swarm

Pi-Swarm is a Pi extension pack that brings the Swarm development model to
Pi: structured prompt and context assembly, policy and hooks, durable state,
tasks, subagents, skills, research tools, parity checks, and a Swarm-oriented
terminal experience.

> **Status:** Active development. The repository is evolving quickly and some
> interfaces are still experimental.

## Why this repository exists

We spent a long time building and using these capabilities in the Swarm SDK.
That work gave us a mature set of ideas—agents, tools, skills, hooks, policy,
tasks, memory, and verification—but it also meant maintaining a separate agent
runtime and user experience.

We decided to move the integration to **Pi**. Pi already provides the provider
and agent loop, session management, native tools, and terminal UI. Pi-Swarm now
adds the Swarm capabilities as extensions instead of maintaining a second agent
runtime. This keeps the boundary clear:

```text
Pi owns the agent loop, provider, sessions, native tools, and TUI
        ↓
Pi-Swarm extensions add Swarm context, policy, tools, state, and UI
```

The migration is not a rewrite of the Swarm model. It is an adaptation of that
model to Pi's lifecycle and extension APIs, with parity fixtures and probes to
make behavioral differences visible.

## Capabilities

### Runtime and context

- Swarm system-prompt and context assembly with provenance and bounded inputs
- Hierarchical `AGENTS.md` discovery
- Plan mode, thinking controls, prompt-context configuration, and inspection
- Session startup and rehydration through Pi lifecycle events
- Runtime hooks, transport parity, cache telemetry, and update checks

### Tools and orchestration

- Bash, filesystem, patch, image-reading, search, and research tools
- Subagents through `Agent` and `AgentControl`
- Task creation, planning, blocking, notes, status, and run control
- Schedules, goals, loops, and wakeups
- MCP and vault adapters
- CodeMode for confined, composable tool workflows
- User questions and structured annoyance/defect reporting

### State and memory

- Durable session entries and conversation metadata
- Repository, worktree, and global memory scopes
- Bounded history search and retrieval with redaction
- Shared state that does not depend on what the TUI happens to render

### Policy and presentation

- Capability, workspace, mutation, network, and approval policy
- Disk hooks and policy nudges
- Pi-native tool renderers plus Swarm hook rows and widgets
- Conversation metrics, footer segments, themes, control-panel status, and
  the `/btw` side-question overlay

### Verification

- Pi ↔ Swarm behavioral parity probes and fixtures
- Unit tests, integration tests, build checks, and dogfood commands
- Architecture notes documenting the migration and the important seams

## How it works

Pi loads extensions from `.pi/extensions/` one level deep. The numeric layer
prefixes define load order:

| Layer | Responsibility |
| --- | --- |
| `00-runtime` | Integration boundary, hooks, runtime, transport, telemetry |
| `10-context` | Prompt assembly, context, plan mode, skills, thinking |
| `20-policy` | Disk hooks and policy nudges |
| `30-tools` | Agents, tasks, bash, filesystem, search, MCP, research, vault, and more |
| `40-state` | Memory history and conversation metadata |
| `50-ui` | Metrics, themes, status, control panel, and UI extensions |

Each layer has a `package.json` whose `pi.extensions` list is authoritative.
An extension registers with Pi using `registerTool`, `registerCommand`,
`registerShortcut`, or lifecycle handlers such as `session_start`,
`before_agent_start`, `tool_call`, and `tool_result`.

The implementation is split between thin Pi adapters and reusable packages:

```text
.pi/extensions/       Pi entrypoints and registration
.pi/lib/              Shared runtime, context, tool, state, and UI helpers
packages/             Reusable TypeScript packages grouped by capability
docs/architecture/    Living design and migration notes
docs/parity/          Parity plan, acceptance criteria, and media
tests/                Repository-level tests
tools/                Probes, integration runners, install checks, and maintenance
vendor/               Read-only upstream references and submodules
```

Semantic state, authorization, persistence, and cancellation belong to the
runtime or state layers—not to rendered transcript text. UI code presents
state; it must not become the source of truth for that state.

## Repository map

| Path | Purpose |
| --- | --- |
| `.pi/extensions/` | Pi extension entrypoints, grouped by load layer |
| `.pi/lib/` | Shared implementation used by extensions |
| `.pi/test/` | Tests for Pi-specific code |
| `.pi/config/` | Project prompt and Swarm settings; credentials stay ignored |
| `.pi/themes/` | Local Pi themes |
| `packages/runtime/` | Core identity, contracts, runtime control, and bootstrap |
| `packages/context/` | Prompt assets, skills, and generated-skill lifecycle |
| `packages/policy/` | Capability and execution policy |
| `packages/tools/` | Agents, CodeMode, MCP, scheduling, and task management |
| `docs/` | Architecture, plans, references, and parity documentation |
| `artifacts/` | Ignored probe output, recordings, and baselines |
| `vendor/` | Read-only upstream source and reference submodules |

The repository operating contract is [AGENTS.md](AGENTS.md). Read it before
making changes; it describes extension inventory, capability ownership, safety
rules, and validation expectations.

## Getting started

### Requirements

- Node.js with npm workspaces support
- Git, including submodule support when working with vendored references
- Pi installed and available on your `PATH` for interactive use

Install dependencies:

```bash
npm install
```

Build the runtime packages:

```bash
npm run build:runtime
```

Build all packages:

```bash
npm run build
```

Pi discovers the project extensions from the repository's `package.json`:

```json
{
  "pi": {
    "extensions": [
      ".pi/extensions/00-runtime",
      ".pi/extensions/10-context",
      ".pi/extensions/20-policy",
      ".pi/extensions/30-tools",
      ".pi/extensions/40-state",
      ".pi/extensions/50-ui"
    ],
    "themes": [".pi/themes"]
  }
}
```

Start Pi from the repository root so the project extensions and hierarchical
context files are discovered:

```bash
pi
```

## Development and validation

Run the focused test suite:

```bash
npm test
```

Run parity checks:

```bash
npm run test:parity
```

Run the repository dogfood path (build, tests, and parity):

```bash
npm run dogfood
```

Check installation health with:

```bash
npm run doctor
```

For parity probes and their acceptance criteria, see
[docs/parity/acceptance.md](docs/parity/acceptance.md). For the migration
design, start with [docs/architecture/modular-pi-architecture.md](docs/architecture/modular-pi-architecture.md),
[docs/architecture/swarm-tui-to-pi-map.md](docs/architecture/swarm-tui-to-pi-map.md),
and [docs/architecture/hooks-prompts-tools-pi-equivalence.md](docs/architecture/hooks-prompts-tools-pi-equivalence.md).

## Contributing

Changes should preserve the Pi/Swarm boundary and keep extensions thin:

1. Put domain logic in the appropriate package or `.pi/lib/` module.
2. Put registration and Pi lifecycle wiring in `.pi/extensions/`.
3. Put authorization and execution constraints in the policy layer.
4. Put rendering in the UI or render bridge, never in semantic state.
5. Update `AGENTS.md` and architecture notes when repository boundaries change.
6. Run the narrowest useful tests, then the broader validation relevant to the
   change.

Please include a clear description of the behavior changed, the commands run,
and any known validation gaps in pull requests.

## License and upstream references

This repository includes vendored upstream projects for reference and
integration. See the relevant license files and submodule metadata before
redistributing any upstream material. The repository's release metadata is
maintained in `package.json`, `CHANGELOG.md`, and `update-manifest.json`.
