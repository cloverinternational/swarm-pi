<p align="center">
  <img src="https://swarmcode.ai/logo.svg" alt="Swarm logo" width="96" />
</p>

<p align="center">
  <a href="https://github.com/swarmcode-ai/swarm-sdk/actions/workflows/ci.yml"><img alt="Build" src="https://img.shields.io/github/actions/workflow/status/swarmcode-ai/swarm-sdk/ci.yml?style=flat-square&branch=main" /></a>
  <a href="https://pkg.go.dev/github.com/swarmcode-ai/swarm-sdk"><img alt="Go Reference" src="https://img.shields.io/badge/go-reference-007d9c?style=flat-square&logo=go&logoColor=white" /></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-proprietary-lightgrey?style=flat-square" /></a>
</p>

Go SDK for building multi-agent AI systems. One import — `client` — gives you a full agent runtime: unified LLM providers, tool calling, streaming, conversation persistence, hooks, skills, and multi-agent orchestration.

## Table of Contents

- [Quick Start](#quick-start)
- [Quick Orientation](#quick-orientation)
- [Package Map](#package-map)
- [Examples](#examples)
- [The client Facade](#the-client-facade)
- [Serving the SDK](#serving-the-sdk)
- [Configuration Discovery](#configuration-discovery)
- [Fuzzing & Migration Notes](#fuzzing--migration-notes)
- [Development](#development)
- [License](#license)

---

## Quick Start

```bash
go get github.com/Swarm-Code/mono/swarm-sdk
```

Set `ANTHROPIC_API_KEY` (or `OPENAI_API_KEY`, `GEMINI_API_KEY`, …) and run:

```go
package main

import (
    "fmt"

    "github.com/Swarm-Code/mono/swarm-sdk/client"
)

func main() {
    // client.New() auto-loads ~/.swarmos/config.json and resolves credentials
    // from env vars, credentials.json, and OAuth tokens — zero config needed.
    c, err := client.New()
    if err != nil {
        panic(err)
    }
    defer c.Close()

    reply, err := c.Chat("Summarize the Go 1.26 release notes in one paragraph.")
    if err != nil {
        panic(err)
    }
    fmt.Println(reply)
}
```

To pin the provider and model explicitly (and skip the user's on-disk config):

```go
c, err := client.New(
    client.WithProvider(client.ProviderAnthropic, "claude-sonnet-4-5"),
    client.WithSystemPrompt("You are a Go expert."),
    client.WithWorkspace("/path/to/project"),
    client.WithoutAutoConfig(), // ignore ~/.swarmos; fully self-contained
)
```

That `*client.Client` is the whole SDK: chat, streaming, tools, conversations, hooks, modes, and MCP are all methods and options on it.

---

## Quick Orientation

Where to start depends on what you're building:

- **New to the SDK?** Read [`examples/cli_agent`](examples/cli_agent/) — a complete agent in ~50 lines — then work through the [examples](#examples) in order.
- **Building a service?** Use [`client`](client/) for the agent and [`serve`](serve/) to expose it over JSON-RPC / SSE / WebSocket. [`examples/http_server`](examples/http_server/) and [`examples/websocket_chat`](examples/websocket_chat/) are the canonical patterns.
- **Everything else in the tree** is internal machinery. The implementation lives under [`internal/`](internal/); the remaining top-level directories are thin compatibility shims kept so existing consumers keep compiling. New code should not import them.

---

## Package Map

The module has exactly one public API surface plus a compatibility layer:

### Public API

| Package | Description |
|---------|-------------|
| [`client`](client/) | The SDK facade. `client.New()` + options; re-exports every type you need (`client.Tool`, `client.Hook`, `client.IntermediateUpdate`, `client.Conversation`, …) so consumers import only this package. |
| [`serve`](serve/) | JSON-RPC 2.0 method registry + HTTP/SSE/WebSocket transports over a `*client.Client`. The supported entry point for server embedding. |

### Compatibility shims (deprecated)

Every other top-level Go package (`agent/`, `tools/`, `provider/`, `conversation/`, `hooks/`, `mode/`, `mcp/`, … ~70 in total) is a generated one-file shim: type aliases and forwarders pointing at the real implementation under `internal/`. They exist so pre-migration consumers compile unchanged.

- Each shim's package comment is marked `Deprecated:` and names its replacement.
- Type identity is preserved (Go type aliases), so values interoperate freely with `client` re-exports.
- **They will be removed in a future major version.** New code should use `client` (and `serve`).

### Internal

| Path | Description |
|------|-------------|
| [`internal/`](internal/) | All implementation packages — providers, agent runtime, tool system, orchestration, storage. Not importable outside this module; structure may change without notice. |

Supporting directories: `cmd/` (CLI tools and probes), `examples/`, `tests/`, `docs/`, `specs/` (feature specs, including this layout's rationale).

---

## Examples

Five self-contained programs under [`examples/`](examples/), each importing only `client` (plus `serve` for the two server examples). Read them in this order:

| # | Example | What it shows | LOC |
|---|---------|---------------|-----|
| 1 | [`cli_agent/`](examples/cli_agent/) | The minimal agent: `client.New()` → `Chat()` → print. | 51 |
| 2 | [`cli_streaming/`](examples/cli_streaming/) | Rendering `client.IntermediateUpdate` variants (content, thinking, tool calls) as they arrive via `Stream()`. | 89 |
| 3 | [`http_server/`](examples/http_server/) | JSON-RPC 2.0 over HTTP with `serve.NewMux`, plus an SSE event stream. | 82 |
| 4 | [`websocket_chat/`](examples/websocket_chat/) | Bidirectional WebSocket chat with a minimal HTML UI; events auto-pushed to the browser. | 90 |
| 5 | [`daemon/`](examples/daemon/) | Long-running background agent: telemetry via `Subscribe`, ticker dispatch, graceful shutdown. | 106 |

```bash
go build ./examples/...
export ANTHROPIC_API_KEY=sk-ant-...
go run ./examples/cli_agent -prompt "Hello, world."
```

See [`examples/README.md`](examples/README.md) for conventions and a "when to use what" guide.

---

## The client Facade

Everything below uses only `client.*` names — no second SDK import required.

### Chat with context and conversations

```go
ctx := context.Background()

// One-shot (creates/uses the default conversation).
reply, err := c.Chat("Hello!")

// Context-aware, addressed to a specific conversation ("" = default).
reply, err = c.ChatCtx(ctx, "", "And in French?")
```

### Streaming updates

`Stream` yields the same update flow the Swarm TUI renders — content tokens, thinking blocks, tool calls, tool results:

```go
updates, err := c.Stream(ctx)
if err != nil {
    panic(err)
}
go func() {
    for u := range updates {
        switch v := u.(type) {
        case client.ContentUpdate:
            fmt.Print(v.Content)
        case client.ThinkingUpdate:
            fmt.Printf("\033[2m%s\033[0m", v.Content)
        case client.ToolCallUpdate:
            fmt.Printf("\n→ %s(%v)\n", v.Name, v.Parameters)
        case client.ToolResultUpdate:
            if v.Error != nil {
                fmt.Printf("← err: %v\n", v.Error)
            }
        }
    }
}()
_, err = c.ChatCtx(ctx, "", "Write a haiku about Go.")
```

The full variant vocabulary (`client.TokenCountUpdate`, `client.SubAgentUpdate`, `client.CompactionDoneUpdate`, …) is documented in [`client/aliases.go`](client/aliases.go). Callback-style delivery is also available via `c.SubscribeUpdates(...)`.

### Events for telemetry

```go
unsub := c.Subscribe(func(ev client.Event) error {
    switch ev.Kind {
    case client.EventAgent:
        if tc, ok := ev.Agent.(client.TokenCountUpdate); ok {
            log.Printf("tokens in=%d out=%d", tc.InputTokens, tc.OutputTokens)
        }
    case client.EventError:
        log.Printf("error: %v", ev.Payload)
    }
    return nil
})
defer unsub()
```

### Options

`client.New` accepts functional options for every subsystem; see `go doc client` for the full set. Highlights:

- `WithProvider` / `WithProviderString` / `WithAPIKey` — provider, model, credentials
- `WithTool` / `WithToolRegistry` — custom tools (`client.Tool` interface)
- `WithHook` — lifecycle hooks (`client.Hook`)
- `WithOperatingMode` — plan/act/auto mode (`client.OperatingMode`)
- `WithSystemPrompt`, `WithWorkspace`, `WithLogger`, `WithTracer`
- `WithCompactionConfig`, `WithDream`, `WithCodeModeConfig`, `WithAutogenSkills`

---

## Serving the SDK

The `serve` package turns a `*client.Client` into a network service. `serve.NewMux` registers ~50 JSON-RPC methods (sendMessage, snapshot, setMode, listConversations, …) with HTTP, SSE, and WebSocket transports:

```go
mux := serve.NewMux(c)
httpMux := http.NewServeMux()
httpMux.Handle("/rpc", mux.HTTPHandler())      // JSON-RPC 2.0 over POST
httpMux.Handle("/sse", mux.SSEHandler())       // event stream
httpMux.Handle("/ws", mux.WebSocketHandler())  // bidirectional
```

See [`examples/http_server`](examples/http_server/) and [`examples/websocket_chat`](examples/websocket_chat/) for complete, runnable versions.

---

## Configuration Discovery

`client.New()` resolves configuration in precedence order: **caller options → env vars → `~/.swarmos/` files → defaults**. Auto-loaded paths include `~/.swarmos/config.json` (provider/model), `credentials.json`, `providers.json` (custom providers), and `oauth.json`; opt-in paths cover hooks, MCP servers, and multi-account rotation.

The authoritative, always-current reference is the package documentation:

```bash
go doc github.com/Swarm-Code/mono/swarm-sdk/client
```

Supported providers out of the box: Anthropic Claude, OpenAI (plus Groq / Azure / OpenRouter and any OpenAI-compatible endpoint), Google Gemini, Ollama (local), MiniMax, Codex, xAI. Select with `client.WithProvider(client.ProviderAnthropic, model)` or by name with `client.WithProviderString("openrouter", model)`.

---

## Fuzzing & Migration Notes

- **Fuzzing campaign** — the SDK ships native Go fuzz targets and long-haul campaign scripts. Start at [FUZZING_README.md](FUZZING_README.md), then [FUZZING_DURATION_GUIDE.md](FUZZING_DURATION_GUIDE.md) and [AGGRESSIVE_FUZZING_SETUP.md](AGGRESSIVE_FUZZING_SETUP.md).
- **Why the tree looks like this** — the public-boundary inversion (all implementation under `internal/`, alias shims at the old paths, `client` as the sole facade) is specified in [specs/001-sdk-dx-overhaul](specs/001-sdk-dx-overhaul/): see `spec.md` for the rationale, `plan.md` for the alias-shim compatibility contract, and `MIGRATION_GUIDE.md` for the mechanical rules.

---

## Development

```bash
git clone https://github.com/swarmcode-ai/swarm-sdk
cd swarm-sdk

go build ./...
go test ./...
```

Tests that require API keys are skipped automatically when keys are absent. To run live provider tests:

```bash
ANTHROPIC_API_KEY=sk-ant-... go test ./tests/provider/anthropic/live/...
```

See [VERSIONING.md](VERSIONING.md) for the release process and [AGENTS.md](AGENTS.md) for contribution guidelines.

---

## License

Proprietary — © 2026 SwarmCode. All rights reserved. See [LICENSE](LICENSE) for terms.
For commercial licensing: [licensing@swarmcode.ai](mailto:licensing@swarmcode.ai)
