# Upstream source snapshot

This directory is a source-focused analysis mirror. It is not a vendored
runtime dependency yet and is not intended to be built from the repository root.

## Inputs

| Snapshot | Source revision | Destination | Files | Size |
|---|---|---|---:|---:|
| Swarm SDK/TUI | `ff0309c75d6d6aee271926f025deee9c2bab9c7d` | `upstream/swarm-sdk` | 3,227 after artifact cleanup | ~42 MB |
| Pi mono reference | `d8aef0feff2bf0ce6aa7f5769da2ed210c14ab74` | `upstream/pi-mono` | 582 | ~7.4 MB |

The Swarm source was selected from the clean `main` checkout at
`/home/swarm/Work/mono`. The Pi reference was selected from the clean checkout
at `/home/swarm/Work/buzz/pi-mono`.

## Included

### Swarm

- SDK production packages: agent, provider, client, conversation, tools,
  hooks, harness, MCP, compaction, serve, observability, workspace-related
  internals, and supporting packages.
- `swarm-tui` production source, tests, harness/automation/ACP packages,
  relevant prompts, settings, workflow manifests, and architecture docs.
- `cmd` production entrypoints and tests.
- Root module metadata, `README.md`, `AGENTS.md`, `LICENSE`, and `CONTRACT.md`.

### Pi

- `packages/agent`, `packages/ai`, `packages/coding-agent`, and `packages/tui`
  source/tests/docs relevant to agent sessions, extensions, tools, rendering,
  providers, RPC, and persistence.
- Root/package manifests and repository README/AGENTS guidance.

## Explicitly excluded

- `.git`, nested repositories, `.harbor`, benchmark/evidence trees, generated
  output, `dist`, `build`, `coverage`, `node_modules`, and profiling output.
- Swarm `integrations/harbor/curator_benchmark`, `docs/evidence`, and the
  token-counting experiment output tree.
- Runtime binaries (`swarm`, `swarm-headless`, `swarmos`, fixture binaries),
  databases, SQLite files, lock/runtime state, and image/video/archive files.
- Swarm TUI's old `octogit` Python UI subtree and profiling artifacts.
- Secret-bearing files such as `.env`, private keys, certificates, and token
  stores. No matching secret filenames were found in the copied snapshot.

HTML, JavaScript, Python, shell, and CSV files that are source or templates are
retained when they belong to an included production package. MIME type alone
was not used as an exclusion rule.

## Research sources

- Pi extension lifecycle and `ExtensionAPI`:  
  https://pi.dev/docs/latest/extensions
- Pi usage, sessions, resource trust, tool allowlists, and design boundaries:  
  https://pi.dev/docs/latest/usage
- Pi RPC embedding and JSON event transport:  
  https://pi.dev/docs/latest/rpc
- Pi sandbox/container patterns:  
  https://pi.dev/docs/latest/containerization

## Verification

- Source revisions recorded with `git rev-parse HEAD`.
- Copy selected from Git-tracked paths only.
- Copied file counts and sizes measured after copying.
- Explicit scan found no `.env`, `*.pem`, `*.key`, database, profiling, bytecode,
  or lock/runtime files after cleanup.
- The destination remains a source snapshot; package-specific builds/tests
  should be run from the copied module roots after their dependency strategy is
  deliberately established.
