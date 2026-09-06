# Plan: source-focused Swarm snapshot and modular Pi architecture

Status: archived — executed; superseded by
`docs/architecture/modular-pi-architecture.md` and
`docs/reference/upstream-snapshot.md`.

## Resolved scope

Create a source-focused mirror of the canonical Swarm SDK/TUI inside
`/home/swarm/Work/Pi-Swarm`, preserving production source, tests, build/module
metadata, canonical prompts/harness schemas, and directly relevant docs.
Exclude binaries, caches, benchmark/evidence trees, generated outputs, nested
repositories, and secret-bearing files.

Canonical inputs:

- `/home/swarm/Work/mono/swarm-sdk`
- `/home/swarm/Work/buzz/pi-mono`

Destination:

- `/home/swarm/Work/Pi-Swarm`

## Low-level decomposition

### A. Source boundary and provenance

Need: clean canonical source status, commit IDs, licenses, module files,
production directories, test directories, generated/artifact classification.

Expect: a reproducible source manifest with source commit IDs and exclusions.

Logic: inspect tracked paths and file types; classify before copying; copy only
validated classes; record the exact source revisions.

Breakpoints: nested repositories, symlinks, generated files mixed with source,
large evidence directories, binary executables, and secret-like names.

### B. Online Pi architecture research

Need: official Pi extension lifecycle, session manager, tool registration,
custom rendering, RPC mode, and sandbox/container guidance.

Expect: research notes tied to official URLs and translated into concrete
module boundaries.

Logic: compare Pi’s documented event order and extension APIs with Swarm’s
agent/tool/hook lifecycle; distinguish Pi-core behavior from extension-owned
behavior and external bridges.

Breakpoints: documentation drift, APIs described in examples but absent from
the pinned local checkout, and treating Pi’s default ambient resources as
closed-harness behavior.

### C. Snapshot copy

Need: selected source files and directories only.

Expect: source tree available locally for bounded inspection and future
commits, with no source mutation during copying.

Logic: use a deterministic allowlist/exclusion procedure; preserve relative
paths and file bytes; do not copy built binaries or runtime state.

Breakpoints: copy size, permission errors, symlink traversal, accidentally
including `.env`, credentials, databases, `.harbor`, output, or nested `.git`.

### D. Modular Pi design

Need: the existing Swarm map, copied source, Pi APIs, and source ownership.

Expect: a module graph and staged implementation sequence:
prompt/config → runtime adapter → tools → hooks/policy → persistence →
subagents/workflows → rendering → remote control.

Logic: keep execution semantics independent from Pi UI callbacks; introduce
interfaces at provider, tool, hook, policy, storage, and transport seams; use
stable IDs for events and conversations.

Breakpoints: circular imports, duplicate policy enforcement, unbounded ambient
configuration, event ordering differences, and unsupported Swarm transports.

### E. Commits and verification

Need: small, surgical commits and focused checks.

Expect:

1. provenance/exclusion manifest;
2. source-focused Swarm snapshot;
3. modular Pi architecture/research document;
4. optional initial scaffold only after explicit implementation approval.

Logic: validate each commit’s contents and status before beginning the next
commit; run checks appropriate to the copied language trees, while documenting
that the destination itself is not yet a runnable Pi package until a scaffold
is added.

Breakpoints: source snapshot too large, package checks requiring unavailable
credentials/network, broken relative links, and tests that depend on excluded
artifacts.

## Acceptance criteria

- No credential values, private keys, `.env` files, databases, binaries, or
  generated benchmark/evidence outputs are copied.
- Source revisions and excluded path classes are documented.
- The copied tree is inspectable with `rg`, bounded reads, and local Git.
- The architecture document cites official Pi documentation and local Swarm
  source paths.
- Every commit is independently reviewable and has an explicit verification
  result.
