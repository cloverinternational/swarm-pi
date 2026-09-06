# Swarm SDK Contract Linter

`swarmlint` is a static analysis tool that enforces Swarm SDK contracts at compile time.

## Overview

Swarm SDK contracts define invariants that MUST be maintained for correct behavior. This linter detects violations before code runs, catching issues that would otherwise only surface at runtime.

### Contracts Enforced

| Contract | Violation | Fix |
|----------|-----------|-----|
| OrderedBlocks Access | Direct `.OrderedBlocks` field access | Use `GetOrderedBlocks()` method |
| Message Construction | Direct `Message{}` struct literal | Use `NewMessage()` or `MessageBuilder` |
| JSON-RPC Client | Custom WebSocket implementations | Use official `swarm-sdk/client` |
| Documentation | Missing contract comments | Add `CONTRACT:` documentation |

## Installation

### From Source

```bash
cd swarm-sdk/lint/cmd/swarmlint
go install .
```

### As a Go Tool

Add to your `go.mod`:

```go
//go:build tools

package tools

import _ "github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint"
```

Then install:

```bash
go install github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint@latest
```

## Usage

### Basic Usage

```bash
# Check current package
swarmlint .

# Check all packages recursively
swarmlint ./...

# Check specific packages
swarmlint github.com/Swarm-Code/mono/swarm-sdk/client/...
```

### As a Go Vet Analyzer

```bash
# Install first
go install github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint@latest

# Run via go vet
go vet -vettool=$(which swarmlint) ./...
```

### Command Line Flags

```bash
swarmlint -h

Usage of swarmlint:
  -ordered-blocks
        check for direct OrderedBlocks access (default true)
  -message-construction
        check for direct Message{} construction (default true)
  -custom-client
        check for custom JSON-RPC client usage (default true)
  -documentation
        check for contract documentation (default false)
```

## Violations and Fixes

### 1. Direct OrderedBlocks Access

**Problem:**
```go
msg := &conversation.Message{}
blocks := msg.OrderedBlocks  // WRONG: May be out of order!
```

**Fix:**
```go
msg := &conversation.Message{}
blocks := msg.GetOrderedBlocks()  // CORRECT: Guaranteed sorted
```

**Why:** The `OrderedBlocks` field may contain blocks in the order they were received, not in sequence order. `GetOrderedBlocks()` always returns them sorted by sequence number.

### 2. Direct Message Construction

**Problem:**
```go
msg := &conversation.Message{
    OrderedBlocks: []conversation.MessageBlock{
        {Sequence: 5, Content: "first"},
        {Sequence: 1, Content: "second"},
    },
}
// WRONG: Blocks are in wrong order!
```

**Fix:**
```go
msg := conversation.NewMessageBuilder().
    AddBlock(conversation.MessageBlock{Sequence: 5, Content: "first"}).
    AddBlock(conversation.MessageBlock{Sequence: 1, Content: "second"}).
    Build()
// CORRECT: Builder sorts blocks automatically
```

**Why:** Direct construction bypasses validation and ordering guarantees. Factory methods ensure invariants are maintained.

### 3. Custom JSON-RPC Client

**Problem:**
```go
import "github.com/gorilla/websocket"

func connect() {
    conn, _, _ := websocket.DefaultDialer.Dial("ws://localhost:8080/ws", nil)
    // WRONG: Custom client may not handle auth, reconnection, errors correctly
}
```

**Fix:**
```go
import "github.com/Swarm-Code/mono/swarm-sdk/client"

func connect() {
    c, err := client.New()  // CORRECT: Official client handles everything
}
```

**Why:** The official client handles authentication, reconnection, error handling, and protocol compliance automatically.

### 4. Missing Contract Documentation

**Problem:**
```go
func ProcessMessage(msg *conversation.Message) error {
    // No documentation about contracts
}
```

**Fix:**
```go
// ProcessMessage processes a message.
// CONTRACT: Message must be valid (non-nil, with valid blocks).
// CONTRACT: Caller must not modify msg.OrderedBlocks directly.
func ProcessMessage(msg *conversation.Message) error {
}
```

**Why:** Public APIs should document their invariants so callers understand the contract.

## CI/CD Integration

### GitHub Actions

Add to `.github/workflows/lint.yml`:

```yaml
name: Lint

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  swarmlint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      
      - name: Install swarmlint
        run: go install github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint@latest
      
      - name: Run swarmlint
        run: swarmlint ./...
      
      - name: Run go vet with swarmlint
        run: go vet -vettool=$(which swarmlint) ./...
```

### GitLab CI

Add to `.gitlab-ci.yml`:

```yaml
swarmlint:
  stage: test
  image: golang:1.22
  script:
    - go install github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint@latest
    - swarmlint ./...
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
```

### Pre-commit Hook

Add to `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: local
    hooks:
      - id: swarmlint
        name: swarmlint
        entry: swarmlint
        language: system
        types: [go]
        pass_filenames: false
```

### Makefile Integration

Add to `Makefile`:

```makefile
.PHONY: lint
lint: swarmlint
	@echo "Running swarmlint..."
	@swarmlint ./...

.PHONY: lint-install
lint-install:
	@go install github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint@latest

swarmlint:
	@which swarmlint > /dev/null || go install github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint@latest
```

### golangci-lint Integration

Create `.golangci.yml`:

```yaml
linters-settings:
  custom:
    swarmlint:
      path: ./lint/cmd/swarmlint
      description: Swarm SDK contract linter
      original-url: github.com/Swarm-Code/mono/swarm-sdk/lint

linters:
  enable:
    - swarmlint
```

## IDE Integration

### VS Code

Add to `.vscode/settings.json`:

```json
{
  "go.lintTool": "golangci-lint",
  "go.lintFlags": [
    "--enable=swarmlint"
  ],
  "go.vetFlags": [
    "-vettool=${workspaceFolder}/bin/swarmlint"
  ]
}
```

### GoLand/IntelliJ

1. Open Settings → Tools → File Watchers
2. Add a new Go tools watcher:
   - Program: `swarmlint`
   - Arguments: `$FileDir$`
   - Working Directory: `$ProjectFileDir$`

### Vim/Neovim (with gopls)

Configure `gopls` to use the linter:

```lua
-- For Neovim with nvim-lspconfig
require'lspconfig'.gopls.setup{
  settings = {
    gopls = {
      buildFlags = {"-vettool=swarmlint"},
    }
  }
}
```

## Suppressing Violations

### For a Single Line

```go
msg := &conversation.Message{}
//nolint:swarmlint // Reason: We need raw access for serialization
blocks := msg.OrderedBlocks
```

### For a File

```go
//nolint:swarmlint
package serialization

// This file handles raw serialization and needs direct field access
```

### For a Function

```go
//nolint:swarmlint
func marshalMessage(msg *Message) []byte {
    // Serialization requires direct field access
}
```

## Architecture

```
swarm-sdk/lint/
├── swarmlint.go          # Main analyzer implementation
├── swarmlint_test.go     # Unit tests
├── README.md             # This file
└── cmd/
    └── swarmlint/
        └── main.go       # Command entry point
```

### Analyzer Structure

The linter is built using `golang.org/x/tools/go/analysis`:

```go
var Analyzer = &analysis.Analyzer{
    Name:     "swarmlint",
    Doc:      "enforce Swarm SDK contracts at compile time",
    Run:      run,
}
```

Each detector is a separate function:

- `detectOrderedBlocksAccess()` - Finds direct `.OrderedBlocks` access
- `detectDirectMessageConstruction()` - Finds `Message{}` literals
- `detectCustomClientImplementation()` - Finds WebSocket imports outside client
- `detectMissingContractDocumentation()` - Finds undocumented public APIs

## Extending the Linter

### Adding a New Check

1. Add a detector function in `swarmlint.go`:

```go
func detectMyNewViolation(pass *analysis.Pass, n ast.Node) {
    // Your detection logic here
    if isViolation(n) {
        pass.Reportf(n.Pos(), "%s", ErrMyViolation)
    }
}
```

2. Add the error message:

```go
var ErrMyViolation = ContractViolationPrefix +
    "Description of the violation. " +
    "Recommendation for fixing it."
```

3. Call it from `run()`:

```go
func run(pass *analysis.Pass) (interface{}, error) {
    for _, file := range pass.Files {
        ast.Inspect(file, func(n ast.Node) bool {
            detectMyNewViolation(pass, n)
            return true
        })
    }
    return nil, nil
}
```

4. Add tests in `swarmlint_test.go`

### Adding Suggested Fixes

```go
func detectMyNewViolation(pass *analysis.Pass, n ast.Node) {
    // ... detection logic ...
    
    pass.Report(analysis.Diagnostic{
        Pos:      n.Pos(),
        End:      n.End(),
        Message:  ErrMyViolation,
        SuggestedFixes: []analysis.SuggestedFix{{
            Message: "Apply fix",
            TextEdits: []analysis.TextEdit{{
                Pos:     n.Pos(),
                End:     n.End(),
                NewText: []byte("fixed code"),
            }},
        }},
    })
}
```

## Performance

The linter is designed for fast execution:

- **O(n)** complexity where n is the number of AST nodes
- **No type checking required** for most checks (uses heuristics)
- **Parallel-safe** - can analyze multiple packages concurrently
- **Incremental** - skips generated files and test packages

Typical performance:

```
$ time swarmlint ./...
swarmlint ./...  0.12s user 0.03s system 99% cpu 0.151 total
```

## Troubleshooting

### "cannot find package"

Make sure you've installed the linter:

```bash
go install github.com/Swarm-Code/mono/swarm-sdk/lint/cmd/swarmlint@latest
```

### False Positives

If you get false positives in legitimate code:

1. Check if you're in a test file (tests are automatically skipped)
2. Add `//nolint:swarmlint` with a reason comment
3. File an issue with a reproduction case

### Missing Violations

If violations aren't being detected:

1. Check that the check is enabled (flags)
2. Verify the package path matches
3. Check for `//nolint` directives

## Contributing

1. Fork the repository
2. Create a feature branch
3. Add your detector with tests
4. Run `go test ./lint/...`
5. Submit a pull request

## License

See the main Swarm SDK LICENSE file.

## References

- [Go Analysis Framework](https://pkg.go.dev/golang.org/x/tools/go/analysis)
- [Writing Go Linters](https://disaev.github.io/p/writing-useful-go-linter/)
- [Swarm SDK Contracts](https://swarmcode.ai/docs/sdk/contracts)
