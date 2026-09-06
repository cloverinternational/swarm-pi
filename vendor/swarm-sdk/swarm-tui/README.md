# SwarmOS TUI

**A terminal user interface for SwarmOS — an AI-powered, agent-driven development environment.**

SwarmOS TUI is a fast, keyboard-first terminal client for working with AI coding
agents. It supports multiple model providers, multi-agent orchestration,
streaming responses, hooks, MCP tools, and a fully scriptable headless mode for
automation and CI.

---

## Features

- **Multi-provider chat** — Claude Code, Anthropic, OpenAI, Gemini, Cerebras,
  OpenRouter, Fireworks, Groq, Together, DeepSeek, and Perplexity.
- **Fallback & retry chains** — automatic provider/model failover with
  configurable rate limits and rotation on rate-limit errors.
- **Multi-agent orchestration** — spawn sub-agents and delegates, run work in the
  background, and coordinate via A2A (agent-to-agent) messaging.
- **Headless mode** — drive the agent non-interactively with `-p` for scripting,
  pipelines, and CI; output as `text`, `json`, or `stream-json`.
- **Context compaction** — micro-compaction and auto-compaction keep long
  conversations within the model's context window.
- **MCP tools & hooks** — extensible tool surface plus lifecycle hooks for
  governance and automation.
- **Background daemon** — keep a global engine running for cron/scheduled work
  even after the UI is closed.

## Prerequisites

- **Go 1.25.6+** — required to build the TUI.
- **Swarm SDK** — the Go agent SDK must be available at `../swarm-sdk`
  (repository: `github.com/Swarm-Code/mono/swarm-sdk`).
- **Git** — used to inject version/build information at compile time.

## Building

### Quick build

```bash
make build
```

Produces a `swarm` binary (and a `swarm-headless` binary) in the current
directory.

### Build with an explicit version

The base version is read from `internal/version/version.go`. Override the full
version string for a build with:

```bash
VERSION=v1.13.1 make build
```

### Run from source

```bash
go run -tags fts5 ./cmd/swarmos
```

> Note: the `fts5` build tag is required for full-text search support.

## Installation

Stable native executables and checksums are published at
[`Swarm-Code/swarm-releases`](https://github.com/Swarm-Code/swarm-releases/releases/latest).
Select the asset matching your operating system and architecture, download its
adjacent `.sha256` file, and verify the checksum before installation. The
built-in updater uses the same public, checksummed release channel.

Install system-wide (to `/usr/local/bin`, plus `~/bin` when present):

```bash
make install
```

Override the install location:

```bash
make install INSTALL_DIR=$HOME/.local/bin
```

## Usage

Launch the interactive TUI:

```bash
swarm
```

### Headless mode

Run a single prompt without the UI — ideal for scripts and CI:

```bash
# Basic headless prompt
swarm -p "Summarize the changes in this repo"

# Choose provider and model, emit structured output
swarm -p "Review main.go" -P anthropic -m claude-opus-4-20250514 --output-format json
```

### Common flags

| Flag | Description |
|------|-------------|
| `-p <prompt>` | Run headless with the given prompt |
| `-P <provider>` | Override provider (e.g. `anthropic`, `openai`, `gemini`) |
| `-m <model>` | Override model ID |
| `--api-key <key>` | Provider API key (overrides env/stored) |
| `--base-url <url>` | Custom base URL for OpenAI-compatible providers |
| `--workspace <dir>` | Working directory for tools (default: cwd) |
| `--max-tokens <n>` | Max output tokens (default: 32768) |
| `--max-turns <n>` | Max agent turns (default: 10) |
| `--tools <list>` | Comma-separated tools to enable |
| `--disable-tools <list>` | Comma-separated tools to disable |
| `--output-format <fmt>` | Headless output: `text`, `json`, or `stream-json` |
| `--operating-mode <mode>` | `PLAN`, `ACT`, or `AUTO` (default: `ACT`) |
| `--daemon` | Render the global background daemon instead of an in-process engine |
| `--no-update` | Disable auto-update checks |
| `--version` | Show version and build information |
| `--help` | Show the full flag reference |

Run `swarm --help` for the complete list. See
[docs/CLI_REFERENCE.md](docs/CLI_REFERENCE.md) for details.

## Configuration

Configuration is JSON-based. See [`config.example.json`](config.example.json) for
a complete example covering providers, models, fallback chains, retry settings,
rate limits, and compaction. A minimal example:

```json
{
  "current_provider": "ClaudeCode",
  "current_model": "claude-opus-4-20250514",
  "chat_fallback_chain": {
    "primary": { "provider": "ClaudeCode", "model": "claude-opus-4-20250514" },
    "fallbacks": [ { "provider": "OpenAI", "model": "gpt-5.1" } ]
  },
  "retry_settings": { "enabled": true, "max_retries_per_provider": 3 },
  "enableMicroCompaction": true,
  "enableAutoCompaction": false,
  "autoCompactionThresholdPercent": 85
}
```

Full reference: [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## Project Structure

```
swarm-tui/
├── cmd/
│   ├── swarmos/            # Main TUI application entry point
│   ├── anthropic-web-search/
│   ├── tui-automation/     # Headless automation harness
│   ├── tui-preview/        # Render preview tool
│   └── pngdiff/            # Visual-diff tool for VHS verification
├── internal/               # Internal packages (chat, settings, engine, version, …)
├── headless/               # Headless server + ACP protocol implementation
├── docs/                   # Documentation
├── scripts/                # Build/install helper scripts
└── Makefile                # Build, install, and VHS test targets
```

The Swarm SDK lives alongside the TUI in the monorepo at `../swarm-sdk`.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Configuration](docs/CONFIGURATION.md)
- [Keybindings](docs/KEYBINDINGS.md)
- [CLI Reference](docs/CLI_REFERENCE.md)
- [VHS Visual Testing](docs/VHS_VISUAL_TESTING.md)
- [Agent Development Guide](AGENTS.md)

## Development

```bash
# Run directly from source (fts5 tag required)
go run -tags fts5 ./cmd/swarmos

# Build automation/preview tools
make tools

# Show the version info that will be injected at build time
make version-info
```

### Visual regression testing (VHS)

```bash
make vhs-baseline   # refresh approved baseline images after intentional UI changes
make vhs-verify     # fail if current snapshots drift from baseline
```

Requires [VHS](https://github.com/charmbracelet/vhs) (`brew install vhs`).

## License

**Proprietary Software — All Rights Reserved**

Copyright (c) 2026 SwarmCode. This software is proprietary and confidential.

- Personal, non-commercial evaluation use permitted
- Commercial use requires a license from SwarmCode
- Redistribution, modification, or derivative works prohibited

For commercial licensing: licensing@swarmcode.ai · https://swarmcode.ai/licensing

See the [LICENSE](LICENSE) file for complete terms.

## Contributing

This is proprietary software. Contributions are accepted only under a Contributor
License Agreement (CLA). See [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## Clickable links

URLs, markdown links and source locations in the chat become clickable
terminal hyperlinks (OSC 8). The visible text never changes and no column
is added — the escape is zero-width — so enabling this cannot shift layout.

What is linked:

| In a message | Becomes |
| --- | --- |
| `https://example.com` | a link to itself |
| `[docs](https://example.com)` | the label `docs`, pointing at the target |
| `internal/chat/app.go:42` | `file://<host>/…/app.go#42` |

Only `http`, `https`, `mailto` and `file` targets are ever linked; anything
else stays plain text. File paths are linked only when the file actually
exists, and are resolved against the workspace root. A label may differ from
its target only for assistant text — in raw tool output the destination is
always shown literally, so a search result cannot present a friendly label
pointing somewhere else.

### tmux

tmux passes hyperlinks through from 3.4 onward, but the capability is off by
default:

```tmux
set -ga terminal-features "*:hyperlinks"
```

### Opening files at a line (kitty)

Kitty already opens `file://` links in `$EDITOR`. To honour the line number,
add to `~/.config/kitty/open-actions.conf`:

```conf
protocol file
fragment_matches [0-9]+
action launch --type=os-window -- $EDITOR +$FRAGMENT -- $FILE_PATH
```

Terminals without OSC 8 support simply render the text normally.
