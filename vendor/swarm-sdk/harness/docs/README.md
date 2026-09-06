# harness — operator documentation

`harness` is a public, side-effect-free compiler for Swarm "harness" documents.
It implements ONLY parse → validate → resolve → compile
(`harness/doc.go`'s package comment). Compiling a manifest never constructs a
provider, a client, a tool instance, an MCP process, a goroutine, or performs
any network or filesystem write beyond reading the manifest and the asset
files it explicitly references. The result is an immutable `*harness.Plan` —
turning that into a live, running client is a separate, later step performed
by a different package (`client.WithHarnessPlan`).

This directory documents the manifest format and the guarantees the compiler
makes about it. It does not document `client` itself.

## What a harness manifest is

A harness manifest is a single YAML (or JSON) document with exactly two
required top-level identity fields (`harness/version.go`):

```yaml
apiVersion: swarm.ai/v1alpha1
kind: Harness
```

`swarm.ai/v1alpha1` is currently the **only** supported `apiVersion` value —
see [SCHEMA.md](SCHEMA.md) for why, and do not assume any other value (or any
migration path from one) exists yet. `kind` must be exactly `Harness`. Both
are validated before anything else in the document is even looked at
(`validateVersion` runs first in `compileDocument`, `harness/plan.go`).

Beyond that, a manifest declares (at minimum) `metadata.name`, a
`provider.id`/`provider.model`, an `agent.systemPrompt`, and an
`agent.tools` selection (or a `compatibility:` preset that supplies one — see
[SCHEMA.md](SCHEMA.md)). Everything else — hooks, MCP servers, skills,
profiles/fallback/subagents, budgets, schedules, workflows — is optional and
additive: omitting a section compiles to the exact same plan (and, per the
package's own digest discipline, the same content digest) as the version of
this compiler that predates that section.

## The closed-construction guarantee

A compiled `*harness.Plan` is inert: `harness.Compile`/`harness.CompileBytes`
produce it with **no runtime side effects beyond reading the manifest and any
referenced asset files** (`harness/plan.go`'s `Compile` doc comment). Turning
it into a running client is `client.WithHarnessPlan`, whose own doc comment
(`client/harness_option.go`) states the guarantee this documentation set
relies on throughout, quoted verbatim:

> WithHarnessPlan makes client.New construct a minimal, deterministic harness
> directly from an immutable \*harness.Plan, without any TUI-private
> preassembly.
>
> When a plan is supplied, construction adopts a CLOSED posture: no
> auto-config, no INDEX.md, no ambient credentials, no default tools, no
> hooks, no skills/autoskills, no MCP discovery, no plugins, and no implicit
> provider fallback. Only the plan's declared provider/credential, system
> prompt, workspace, storage, limits, permissions, and EXACT selected tools
> apply.
>
> Passing a nil plan is a no-op (the normal client.New path runs unchanged).

In practice this means: if it is not written in the manifest, it is not in
the constructed client. There is no ambient environment variable, no
auto-discovered config file, and no default tool set that quietly widens what
a harness-built client can do beyond what the manifest says.

That closed posture is *itself* the reason [SECURITY.md](SECURITY.md) exists:
the one thing a closed, deterministic construction path cannot make safe by
itself is a manifest that explicitly, intentionally asks to run local code
(a `type: command`/`type: script` hook, or a `type: stdio` MCP server). Read
SECURITY.md before running an untrusted manifest.

## Minimal end-to-end example

```yaml
apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: minimal-example
provider:
  id: anthropic
  model: claude-sonnet-4-5
agent:
  systemPrompt:
    inline: "You are a minimal harness-driven agent."
compatibility: minimal
permissions:
  approvalMode: interactive
```

```go
plan, err := harness.Compile("harness.yaml")
if err != nil {
    // err is (or wraps) harness.Diagnostics — a structured, stable-coded,
    // secret-free list of problems. See TROUBLESHOOTING.md.
    log.Fatal(err)
}

// Before doing anything else, a host can ask whether this plan will execute
// local code (see SECURITY.md):
if plan.HasExecutableSupplyChain() {
    // ... require operator confirmation before proceeding ...
}

c, err := client.New(client.WithHarnessPlan(plan))
if err != nil {
    log.Fatal(err)
}
```

This exact manifest is also checked into
[`examples/minimal.yaml`](examples/minimal.yaml) and is compiled by a real Go
test (`harness/supplychain_test.go`'s `TestDocsExamplesCompile`), so it is
guaranteed to still work, not just to have worked when this file was written.

## Where to go next

- [SCHEMA.md](SCHEMA.md) — field-by-field reference of everything a manifest
  may declare, derived from the actual Go types in `harness/*.go`.
- [SECURITY.md](SECURITY.md) — the supply-chain / code-execution policy: what
  a manifest can make a host do, what is and is not gated today, and how a
  host can inspect a plan before trusting it.
- [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — the actual diagnostic codes and
  error strings this compiler (and the `client` construction layer) produce,
  and what each one means.
- [`examples/`](examples) — three complete, independently-compiling manifests
  (`minimal.yaml`, `with-hooks-and-mcp.yaml`, `tui-compat.yaml`).
