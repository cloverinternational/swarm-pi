# Forge — Multi-Language Symbol Index

## What Forge Is

Forge is a fast, in-process symbol index that provides `gopls`-level reference accuracy without running an LSP server. It indexes declarations, references, call hierarchies, and type relationships across Go, TypeScript/JavaScript, and Python in a single unified data model.

**Key properties:**
- Sub-millisecond queries after a one-time index build (~1s for ~2000 files)
- No external dependencies (no LSP, no gopls, no tree-sitter)
- Pure Go implementation using `go/ast` for Go and hand-written lexer-based extractors for other languages

## Architecture Overview

```
                     ┌──────────────────┐
                     │  GoSymbolIndex   │  (unified store)
                     │  .symbols[]      │  GoSymbol entries
                     │  .references[]   │  GoReference entries (with OwnerType)
                     │  .methodsByType  │  hierarchy data
                     │  .typeCache      │  bootstrap cache (Go)
                     └───────┬──────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
        ┌─────┴─────┐ ┌─────┴─────┐ ┌─────┴─────┐
        │ Go parser │ │ JS/TS     │ │ Python    │
        │ go/ast    │ │ lang_jsts │ │ lang_py   │
        └─────┬─────┘ └─────┬─────┘ └─────┬─────┘
              │             │             │
              │      ┌──────┴──────┐      │
              │      │ lang_lexer  │      │
              │      │ (tokeniser) │      │
              │      └──────┬──────┘      │
              │             │             │
        ┌─────┴─────┐ ┌─────┴────────┐ ┌──┴───────────┐
        │ go/types   │ │ ts_resolve   │ │ py_resolve   │
        │ bootstrap  │ │ _owners.js   │ │ _owners.py   │
        └───────────┘ └──────────────┘ └──────────────┘
```

### Data Model (shared across all languages)

Despite the `Go` prefix (historical), these types are language-agnostic:

- **`GoSymbol`** — A declaration: name, kind, file, line, column, receiver, signature, doc, package. Kinds: `function`, `method`, `struct`, `interface`, `field`, `variable`, `constant`, `type`.
- **`GoReference`** — A usage site: name, kind (call/selector/type/identifier/declaration), file, line, column, qualifier, enclosing function, **`OwnerType`**.
- **`parseFileResult`** — Per-file output from any language parser: symbols, references, methods, interfaces, embeds.

### The `OwnerType` Field (Critical)

Every `GoReference` has an `OwnerType` field that identifies which type the reference belongs to. For example, if `idx.Complete()` appears in code and `idx` is a `GoSymbolIndex`, then `OwnerType = "GoSymbolIndex"`.

This field is what eliminates false positives. Without it, searching for references to `Complete` would return every `.Complete()` call across all types. With it, `FindReferencesTyped("Complete", "GoSymbolIndex")` returns only references where the receiver is actually a `GoSymbolIndex`.

**For Go**: `OwnerType` is stamped by the type bootstrap pass (see below).
**For TypeScript/JavaScript**: `OwnerType` is stamped by the TS resolver script (see below).
**For Python**: `OwnerType` is stamped by the Python resolver script (see below).

## The Two-Phase Approach

### Phase 1: AST Index (fast, name-based)

Every supported language has a parser that produces a `parseFileResult`:
1. **Go**: `go/ast` parser in `go_symbol_index.go` via `parseGoFile()`
2. **JS/TS**: Hand-written lexer + pattern matcher in `lang_jsts.go` via `parseJSLikeFile()`
3. **Python**: Hand-written lexer + pattern matcher in `lang_python.go` via `parsePythonFile()`

All parsers are registered in `lang_dispatch.go:detectLanguageParser()`. The indexer calls this for every file, and the returned `parseFileResult` is merged into the unified `GoSymbolIndex`.

**What Phase 1 gives you**: Declarations, references (by name), call hierarchy (by enclosing function), type hierarchy (struct/interface relationships). References are name-based — searching for `Complete` returns ALL identifiers named `Complete` regardless of which type they belong to.

### Phase 2: Type Enrichment (one-time, expensive)

Phase 1's name-based references have a precision problem: common method names like `Complete`, `Close`, `String` appear on many types. Phase 2 solves this by running a type checker once and stamping each reference with its resolved `OwnerType`.

**For Go**: This is implemented in `type_bootstrap.go`. The process:

1. **Detect module path** from `go.mod`
2. **Discover all packages** by walking the workspace
3. **Type-check in dependency order**: non-test packages first (populating a shared `*types.Package` cache), then test packages (which can resolve workspace imports from the cache)
4. **Extract owner info** from `types.Info.Uses`: for each identifier, if the resolved `types.Object` is a `*types.Func` with a receiver, record the receiver type as the owner
5. **Stamp references**: Walk every `GoReference` in the index and set `ref.OwnerType` from the bootstrap cache, keyed by `file:line:col`

After enrichment, `FindReferencesTyped(name, ownerType)` is a simple direct filter — no fallback, no heuristics.

**Current results (Go)**:
```
Name-based  Recall: 100%  Precision: 62.5%  F1: 76.9%  (21 false positives)
Enriched    Recall: 100%  Precision: 100%   F1: 100%   (0 false positives)
```

**For TypeScript/JavaScript**: This is implemented in `type_bootstrap_ts.go` and `ts_resolve_owners.js`. The process:

1. **Locate tsconfig.json** in the workspace (or use `FORGE_TS_RESOLVER` env var)
2. **Run `ts_resolve_owners.js`** via node/bun with `NODE_PATH` pointing to `node_modules`
3. **Type-check the project** using TypeScript's compiler API (`ts.createProgram`)
4. **Walk all property access expressions** (`obj.method`, `obj.property`)
5. **Resolve the owner type** from the left-hand side expression's type
6. **Output JSON** with `file:line:col -> {name, owner, kind}` for each reference
7. **Stamp references**: Go reads the JSON and stamps `ref.OwnerType` on matching `GoReference` entries

The resolver handles:
- Class instances 	 owner is the class name
- Interfaces 	 owner is the interface name
- Union/intersection types 	 first named constituent
- `Promise<T>` 	 unwrapped to `T`
- Module paths (e.g., `"fs"`) 	 cleaned to package name

**Current results (TypeScript)**:
```
'call' name-based: 220 refs across 5 owner types + 27 un-enriched
'call' typed [SwarmIPCClient]: 182 refs (82.7% of total, 0 false positives)
'call' typed [JsonRpcClient]: 6 refs (2.7% of total, 0 false positives)

Enrichment coverage: ~14% of TS refs (only property/method accesses get owners)
```

**For Python**: This is implemented in `type_bootstrap_py.go` and `py_resolve_owners.py`. The process:

1. **Parse Python files** using the `ast` module (no external type checker required)
2. **Track variable types** from:
   - Type annotations (`var: ClassName`)
   - Function parameter annotations (`def foo(x: ClassName)`)
   - Constructor calls (`var = ClassName()`)
3. **Track class definitions** for `self.method()` resolution
4. **Extract attribute accesses** and resolve owner from the left-hand side's type
5. **Output JSON** with `file:line:col -> {name, owner, kind}`
6. **Stamp references**: Go reads the JSON and stamps `ref.OwnerType`

The Python resolver is lighter-weight than TypeScript's — it doesn't need a full type checker, just AST analysis with type annotation tracking.

**Current results (Python)**:
```
2189 refs stamped with owner types across 148 Python files (425ms)
Coverage: ~30% of Python refs (attribute accesses with resolvable types)
```

## Memory Consumption

Forge is designed for low memory overhead:

```
Test                      Files     Refs     Mem MB   MB/1K files   MB/10K refs
Go SDK (indexed only)      859   300897      67.4       78.5          2.24
Go SDK (enriched)          859   300897      70.3       81.9          2.34
swarm-app TS (enriched)    108    35951       7.9       73.3          2.20
mono all (enriched)       4589  1627022     356.6       77.7          2.19
```

Key observations:
- **~2.2 MB per 10,000 references** (consistent across languages)
- **~78 MB per 1,000 files** (varies by refs per file)
- **Full mono repo: 357 MB** for 4,589 files and 1.6M refs
- Enrichment adds only ~3 MB overhead for Go SDK

## File Map

| File | Purpose |
|---|---|
| `go_symbol_index.go` | Core index: `GoSymbolIndex` struct, `Index()`, `BootstrapTypes()`, `enrichReferencesFromBootstrap()`, all query methods |
| `go_references.go` | `GoReference` type, `collectReferences()` (Go AST walker), `FindReferences()`, `FindReferencesTyped()` |
| `go_hierarchy.go` | `parseFileResult` type, `extractHierarchy()`, method/interface/embed tracking |
| `go_call_hierarchy.go` | `IncomingCalls()`, `OutgoingCalls()` using `refsByEnclosingFunc` |
| `go_completion.go` | `Complete()` — prefix-based symbol completion |
| `go_position.go` | Position-based queries: hover, definition, signature help |
| `go_rename.go` | `Rename()` — multi-file identifier rename |
| `type_bootstrap.go` | Go type enrichment: `TypeBootstrapCache`, `Bootstrap()`, workspace-aware importer |
| `type_bootstrap_ts.go` | TypeScript enrichment: `BootstrapTypeScript()`, runs `ts_resolve_owners.js`, parses JSON |
| `type_bootstrap_py.go` | Python enrichment: `BootstrapPython()`, runs `py_resolve_owners.py`, parses JSON |
| `ts_resolve_owners.js` | Node.js script using TypeScript compiler API to resolve owner types |
| `py_resolve_owners.py` | Python script using ast module to resolve owner types from annotations |
| `lang_dispatch.go` | `detectLanguageParser()` — extension-to-parser routing |
| `lang_lexer.go` | Shared tokeniser for JS/TS/Python (not used by Go — Go uses `go/parser`) |
| `lang_jsts.go` | JS/TS declaration + reference extractor |
| `lang_python.go` | Python declaration + reference extractor |
| `semantic_grep.go` | Tool integration: the MCP tool definitions that agents call |
| `tools.go` | Additional tool wiring |
| `accuracy_test.go` | Go accuracy test comparing Forge against gopls with hallucination flagging |
| `ts_accuracy_test.go` | TypeScript accuracy test with owner distribution analysis |
| `memory_test.go` | Memory consumption benchmarks and profiling |
| `index_cache.go` | Disk cache for the index (gob serialization) |

## Adding a New Language

### Step 1: Add a lexer scanner (if needed)

If your language's syntax is close enough to JS/Python, you may be able to reuse `lang_lexer.go`'s tokeniser. Otherwise, add a `scanFoo()` method to the `lexer` type in `lang_lexer.go` (or a new file `lang_foo_lexer.go`).

The tokeniser must emit `lexToken` values with:
- `kind`: token type (identifier, keyword, punctuation, string, number, etc.)
- `text`: raw text of the token
- `line`, `col`: 1-based position

### Step 2: Write the parser (`lang_foo.go`)

Create a file like `lang_rust.go` with a function matching the `languageParser` signature:

```go
func parseRustFile(idx *GoSymbolIndex, filePath string) *parseFileResult {
    src, err := os.ReadFile(filePath)
    if err != nil {
        return nil
    }

    lx := newLexer(src)
    tokens := lx.scanRust() // or scanFoo()

    result := &parseFileResult{
        typeDecls: make(map[string]bool),
    }

    declIdx := make(map[tokenKey]bool)
    extractRustDeclarations(tokens, filePath, result, declIdx)
    extractRustReferences(tokens, filePath, declIdx, result)
    return result
}
```

**Declaration extractor** must populate `result.symbols` with `GoSymbol` entries. Set:
- `Name`, `Kind`, `File`, `Line`, `Column`, `EndLine`, `EndColumn`
- `Receiver` (for methods — the struct/impl type name)
- `ContainerName` (for nested items — the parent type/class)
- `Package` (module/namespace name if applicable)
- `Exported` (public/private)

**Reference extractor** must populate `result.references` with `GoReference` entries. Set:
- `Name`, `Kind` (RefKindCall/RefKindSelector/RefKindType/RefKindIdent), `File`, `Line`, `Column`
- `Qualifier` (for `obj.method()`, qualifier is `"obj"`)
- `EnclosingFunc` (name of the function this reference lives inside)
- `OwnerType` — leave empty for now; enrichment fills it later

**Important**: Use `declIdx` (a `map[tokenKey]bool`) to track which positions are declaration sites. The reference extractor must skip these to avoid double-counting.

### Step 3: Register in `lang_dispatch.go`

Add your extension(s) to `detectLanguageParser()`:

```go
case ".rs":
    return parseRustFile
```

That's it for basic indexing. After this, `Index()` will include your language's symbols and references in the unified index.

### Step 4: Add type enrichment (the precision win)

This is where you get from ~60% precision to ~100%. The approach varies by language:

**For compiled/typed languages (Rust, Java, C#)**:
- Run the language's type checker or compiler frontend once
- Extract owner types for method/field references
- Stamp `ref.OwnerType` on each reference

**For TypeScript**:
- Use `tsc` or the TypeScript compiler API to type-check
- For each identifier usage, resolve its containing type
- Map back to file:line:col and stamp `OwnerType`

**For Python**:
- Use `mypy` or `pyright` in analysis mode
- Extract type info for method calls
- Stamp `OwnerType` on references where the type is known

The enrichment follows the same pattern as Go's `type_bootstrap.go`:

1. Run the type checker once for the whole workspace
2. Build a map of `file:line:col -> ownerType`
3. Walk all `GoReference` entries in the index and stamp `ref.OwnerType`
4. Store file hashes for staleness detection

The key implementation point: add a `BootstrapFooTypes()` method to `GoSymbolIndex` (or extend `BootstrapTypes()` to dispatch by language), and call `enrichReferencesFromBootstrap()` with the results.

### Step 5: Add accuracy tests

Follow the pattern in `accuracy_test.go`. The test should:
1. Build an index over known source files
2. Compare against a ground-truth tool (the language's LSP, compiler, etc.)
3. Report recall, precision, F1, false positives (hallucinations), and false negatives (missing)
4. Flag any symbol where `forge_count > ground_truth_count` as `HALLUCINATING`

## The Workspace-Aware Importer (Go-specific)

The Go type bootstrap uses a custom `workspaceImporter` that chains two import strategies:

1. **Package cache**: When package A imports package B, and B was already type-checked in an earlier pass, the cached `*types.Package` is returned instantly
2. **Default importer**: Falls back to `importer.Default()` for stdlib and external modules

Packages are processed in two passes:
- **Pass 1**: Non-test packages (populates the cache)
- **Pass 2**: Test packages (can now resolve workspace imports from the cache)

This is what allows cross-package references to be fully resolved. Without it, test files that `import "mymodule/tools/forge"` would fail to type-check, leaving those references un-enriched.

**For other languages**: The equivalent would be ensuring your type checker can resolve workspace-local imports. For TypeScript this means a proper `tsconfig.json`; for Python it means setting `PYTHONPATH` or using `pyright`'s project config.

## Key Design Decisions

1. **Unified data model**: All languages share `GoSymbol` and `GoReference`. The "Go" prefix is historical. Don't rename — it would break too many call sites for no functional gain.

2. **No fallback**: `FindReferencesTyped()` filters strictly by `ref.OwnerType`. If a reference wasn't enriched (OwnerType is ""), it's excluded from typed queries. This prevents hallucinations at the cost of missing un-enrichable references.

3. **Enrichment is optional**: The index works without type enrichment — you just get name-based results. Enrichment is an additive precision layer.

4. **One-time bootstrap, cheap maintenance**: The type checker runs once. After that, the AST index maintains itself with fast incremental file re-parses. When a file changes, its references are re-parsed from AST (cheap) and re-stamped from the still-valid cache for unchanged files.

5. **Lexer-based parsers for non-Go**: We don't use tree-sitter or full parsers for JS/TS/Python. A hand-written lexer + pattern matcher is sufficient for declaration/reference extraction and keeps the dependency footprint at zero.

## Running Tests

```bash
# Full accuracy test (compares against gopls, ~45s)
cd swarm-sdk/tools/forge && go test -run TestWideDomainAccuracy -v -timeout 300s

# Just the method disambiguation test (the enrichment proof)
cd swarm-sdk/tools/forge && go test -run 'TestWideDomainAccuracy/references_sdk_scope/method_disambiguation' -v -timeout 300s

# TypeScript enrichment test (~1.5s)
cd swarm-sdk/tools/forge && go test -run TestTypeScriptEnrichment -v -timeout 120s

# Memory consumption test (~12s)
cd swarm-sdk/tools/forge && go test -run TestMemoryConsumption -v -timeout 180s

# Multi-language lexer tests
cd swarm-sdk/tools/forge && go test -run TestLang -v

# All forge tests
cd swarm-sdk/tools/forge && go test -v -timeout 300s
```
