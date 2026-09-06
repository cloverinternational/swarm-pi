# Swarm SDK Usage Contract

This document defines the **CONTRACTUAL REQUIREMENTS** for using the Swarm SDK.
Violations will cause runtime errors with clear remediation steps.

## Purpose

The Swarm SDK is designed for **agent consumers** - autonomous code that uses the SDK to build conversational AI applications. To ensure consistency and correctness across all downstream consumers (swarm-tui, swarm-desktop, external agents), the SDK enforces these contracts at runtime.

**Agents cannot bypass these contracts** - they are intentionally enforced to catch mistakes early and guide correct usage.

---

## CONTRACT 1: Use Official Client

**Requirement:** You MUST use the official client for your language.

### Go Applications
```go
import "github.com/Swarm-Code/mono/swarm-sdk/client"

client, err := client.New(
    client.WithProvider("anthropic", "claude-sonnet-4-5"),
)
```

### TypeScript Applications
```typescript
import { Client } from '@swarm/swarm-sdk-client'

const client = new Client({ url: 'ws://localhost:8080/ws' })
await client.start()
```

**Violation:** Implementing custom JSON-RPC clients  
**Consequence:** Not supported, may break on updates, lacks message ordering guarantees  
**Error:** `ContractViolation: Custom implementation detected`

---

## CONTRACT 2: Valid Provider/Model

**Requirement:** Provider must be a known provider name.

**First-class providers** (with dedicated implementations):
- `anthropic` - Claude models
- `openai` - GPT models
- `gemini` - Gemini models

**OpenAI-compatible providers** (routed via the OpenAI adapter):
- `wafer`, `wafer.ai` - Wafer.ai gateway
- `cerebras` - Cerebras fast inference
- `fireworks` - Fireworks AI
- `groq` - Groq ultra-fast inference
- `openrouter` - Multi-provider aggregator
- `together`, `together-ai`, `togetherai` - Together AI
- `deepseek` - DeepSeek
- `perplexity` - Perplexity AI
- `glm`, `z.ai` - Z.AI GLM models
- `mistral` - Mistral AI
- And others defined in `provider/openai/profiles/profiles.toml`

**Common aliases:**
- `google` → `gemini`
- `claudecode` → `anthropic`
- `codex` → `openai`

**Go Example:**
```go
// ✓ VALID
client.New(client.WithProvider("anthropic", "claude-sonnet-4-5"))

// ✓ VALID - OpenAI-compatible provider
client.New(client.WithProvider("wafer", "GLM-5.1"))

// ✗ INVALID - will throw ContractViolation
client.New(client.WithProvider("totally-unknown-provider", "model"))
```

**TypeScript:** Provider is configured server-side, but URL must be valid scheme.

**Violation:** Passing unknown provider name  
**Consequence:** Client construction fails immediately  
**Error:** `ContractViolation: Unknown provider: "unknown-provider"`

---

## CONTRACT 3: Valid URL Scheme

**Requirement (TypeScript):** URL must start with known transport scheme.

**Valid Schemes:**
- `http://` or `https://` - HTTP transport (request/response only)
- `ws://` or `wss://` - WebSocket transport (bidirectional, streaming)
- `sse+http://` or `sse+https://` - SSE transport (read-only events)

**Example:**
```typescript
// ✓ VALID
new Client({ url: 'ws://localhost:8080/ws' })

// ✗ INVALID - will throw ContractViolation
new Client({ url: 'invalid-url' })
```

**Violation:** Invalid URL scheme  
**Consequence:** Client construction fails immediately  
**Error:** `ContractViolation: Invalid URL scheme`

---

## CONTRACT 4: Message Ordering

**Requirement:** Messages MUST be accessed via `GetOrderedBlocks()` to ensure correct display order.

**Rationale:** Streaming events arrive with sequence numbers that must be sorted for correct display. Direct access to blocks would show messages out of order.

**Go Example:**
```go
// ✓ VALID - uses accessor method
blocks := msg.GetOrderedBlocks()

// ✗ INVALID - direct field access
blocks := msg.OrderedBlocks  // Compile error: use GetOrderedBlocks()
```

**TypeScript Example:**
```typescript
// ✓ VALID - uses accessor method
const blocks = msg.getOrderedBlocks()

// ✗ INVALID - direct property access  
const blocks = msg.orderedBlocks  // Runtime error
```

**Enforcement:**
- Go: Private field (lowercase), public getter method
- TypeScript: Getter method only, private backing field
- Linter: `swarmlint` detects violations automatically

**Violation:** Accessing blocks directly  
**Consequence:** Blocks returned out of order  
**Error:** `CONTRACT VIOLATION: Use GetOrderedBlocks() instead of direct .OrderedBlocks access`

---

## CONTRACT 5: Hook Display (Coming Soon)

**Requirement:** Hooks where `MustDisplayHook()` returns `true` MUST be displayed prominently.

**Important Hooks:**
- `task-enforcement-hook` - Blocked operations
- `post-acting-hook` - Acting violations
- `verification-protocol-hook` - Verification failures
- `session-start-hook` - Session context
- `steering-pretool` - Steering decisions

**Rationale:** These hooks contain critical information that users must see. Blocked hooks indicate prevented operations.

**Status:** ⚠️ Implementation pending (Phase 4)

**Planned Enforcement:**
```go
// MustDisplayHook returns contractual requirement
if client.MustDisplayHook(hook) {
    // CONTRACT: You MUST display this hook
    displayHook(hook)
}
```

**Violation:** Hiding blocked hooks or important hooks  
**Consequence:** Users miss critical information  
**Remediation:** Check `MustDisplayHook()` before filtering hooks

---

## CONTRACT 6: Type-Safe Provider (Go)

**Requirement:** Provider must be specified using type-safe `Provider` constants.

**Rationale:** String-based provider selection is error-prone. Compile-time type safety catches typos and enables IDE autocomplete.

**Valid Providers:**
- `client.ProviderAnthropic` - Claude models
- `client.ProviderOpenAI` - GPT models  
- `client.ProviderGemini` - Gemini models

**Example:**
```go
// ✓ VALID - type-safe constant
c, err := client.New(
    client.WithProvider(client.ProviderAnthropic, "claude-sonnet-4-5"),
)

// ✗ INVALID - string literal (will cause ContractViolation)
c, err := client.New(
    client.WithProvider("anthropic", "claude-sonnet-4-5"), // Error!
)
```

**Dynamic Selection:**
```go
// For dynamic provider selection, use ParseProvider
p, err := client.ParseProvider(config.Provider)
if err != nil {
    return err
}
c, err := client.New(client.WithProvider(p, config.Model))
```

**Violation:** Using string literals for providers  
**Consequence:** Compile-time error (type mismatch)  
**Error:** `cannot use *provider (variable of type string) as client.Provider value`

---

## CONTRACT 7: Type-Safe URL (TypeScript)

**Requirement:** URL must be a validated `ValidUrl` type.

**Rationale:** Invalid URLs cause connection failures. Validating at construction time ensures URLs have supported schemes.

**Valid Schemes:**
- `ws://` or `wss://` - WebSocket transport
- `http://` or `https://` - HTTP transport
- `sse+http://` or `sse+https://` - SSE transport

**Example:**
```typescript
// ✓ VALID - type-safe URL construction
import { createUrl } from '@swarm/swarm-sdk-client'

const url = createUrl('ws://localhost:8080/ws')
const client = new Client({ url })

// ✗ INVALID - plain string
const client = new Client({ url: 'invalid-url' })  // Type error!
```

**Helper Functions:**
```typescript
// createUrl - throws ContractViolation on invalid URL
const url = createUrl('ws://localhost:8080/ws')

// tryCreateUrl - returns null on invalid URL
const url = tryCreateUrl(userInput)
if (url) { /* valid */ }

// isValidUrl - type guard for strings
if (isValidUrl(maybeUrl)) {
    // maybeUrl is now ValidUrl type
}
```

**Violation:** Using unvalidated string URLs  
**Consequence:** Client construction fails  
**Error:** `CONTRACT VIOLATION: Invalid URL scheme`

---

## Error Format

All contract violations use the `ContractViolation` error type with this format:

```
CONTRACT VIOLATION: [what was violated]
Required: [what is required]
Hint: [how to fix]
```

### Handling Contract Violations

**Go:**
```go
import "github.com/Swarm-Code/mono/swarm-sdk/client"

client, err := client.New(opts)
if err != nil {
    var cv *client.ContractViolation
    if errors.As(err, &cv) {
        // Handle contract violation
        log.Printf("Contract violated: %s", cv.Violation)
        log.Printf("Required: %s", cv.Required)
        log.Printf("Fix: %s", cv.Hint)
        // Fix your code
    }
    return err
}
```

**TypeScript:**
```typescript
import { Client, isContractViolation } from '@swarm/swarm-sdk-client'

try {
  const client = new Client({ url: 'invalid' })
} catch (err) {
  if (isContractViolation(err)) {
    console.error('Contract violated:', err.violation)
    console.error('Required:', err.required)
    console.error('Fix:', err.hint)
    // Fix your code
  }
}
```

---

## Summary

| Contract | Language | Enforcement | Status |
|----------|----------|-------------|--------|
| Use Official Client | Both | Error on custom implementations | ✅ Active |
| Valid Provider | Go | Type-safe Provider constants | ✅ Active |
| Valid URL Scheme | TypeScript | ValidUrl branded type | ✅ Active |
| Message Ordering | Both | Private blocks + GetOrderedBlocks() | ✅ Active |
| Hook Display | Both | MustDisplayHook() contract | ⚠️ Pending |
| Custom Linter | Go | swarmlint analyzer | ✅ Active |

---

## For Downstream Consumers

If you're building an application that uses the Swarm SDK:

1. **Read this contract** before integrating
2. **Use the official client** for your language
3. **Handle ContractViolation** errors explicitly
4. **Follow the examples** in the SDK documentation
5. **Check for updates** when new contracts are added

## For SDK Maintainers

When adding new features to the SDK:

1. **Define contracts first** - What MUST consumers do?
2. **Enforce at runtime** - Catch violations early with `ContractViolation`
3. **Enforce at compile-time** - Use strong types (`Provider`, `ValidUrl`) to prevent invalid usage
4. **Use ContractViolation** - Consistent error format with Violation/Required/Hint
5. **Add contract documentation** - Every public API must have CONTRACT: section in godoc
6. **Update this document** - Document all contracts
7. **Test violations** - Ensure errors are helpful
8. **Add linter rules** - Update `swarmlint` to detect new violation patterns

### Validation Requirements

All new public APIs MUST include appropriate validation:

- **Input validation** - Validate all inputs and return `ContractViolation` errors for misuse
- **Type constructors** - Enforce valid values via strong types (e.g., `Provider` type)
- **Factory methods** - Must be the only way to construct complex types
- **Functional options** - Use for extensible configuration with validation

### Type Safety Requirements

- Prefer strong types over strings (e.g., `Provider` instead of `string`)
- Use functional options pattern for configuration
- Hide implementation details in `internal/` packages

---

## Questions?

If you encounter a contract violation that seems incorrect:
1. Check this document for the contract definition
2. Review your code against the requirements
3. If still unclear, file an issue with the error message and your code
