package advanced

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// ToolBuilder — fluent API for constructing tools with advanced features
// ---------------------------------------------------------------------------

// ToolBuilder provides a fluent API for creating tools with advanced metadata.
// It produces a BuiltTool that implements tools.Tool plus the optional
// Deferrable, tools.ToolWithExamples, tools.ParallelCapable, and
// tools.MetadataProvider interfaces.
//
// Example:
//
//	tool := advanced.NewToolBuilder("my_reader").
//		WithDescription("Reads data from the source").
//		WithParameters(schema).
//		WithExecutor(myExecuteFunc).
//		WithDefer(true).
//		WithConcurrency(true).
//		WithExamples([]tools.ToolExample{{Description: "basic read", Parameters: map[string]any{"path": "/etc/hosts"}}}).
//		WithCategory("filesystem").
//		WithTags("read", "fast").
//		Build()
type ToolBuilder struct {
	name            string
	description     string
	parameters      any
	executor        func(ctx context.Context, params map[string]any) (*tools.ToolResult, error)
	validator       func(params map[string]any) error
	idempotent      bool
	permissions     []tools.Permission
	contentTypes    []tools.ContentType
	hints           *tools.OptimizationHints
	shouldDefer     bool
	concurrencySafe bool
	examples        []tools.ToolExample
	category        string
	tags            []string
	source          tools.ToolSource
	serverName      string
	version         string
	author          string
}

// NewToolBuilder starts building a tool with the given name.
func NewToolBuilder(name string) *ToolBuilder {
	return &ToolBuilder{
		name:         name,
		contentTypes: []tools.ContentType{tools.ContentTypeText},
	}
}

// WithDescription sets the tool's description.
func (b *ToolBuilder) WithDescription(desc string) *ToolBuilder {
	b.description = desc
	return b
}

// WithParameters sets the JSON Schema for the tool's parameters.
func (b *ToolBuilder) WithParameters(params any) *ToolBuilder {
	b.parameters = params
	return b
}

// WithExecutor sets the function that executes the tool.
func (b *ToolBuilder) WithExecutor(fn func(ctx context.Context, params map[string]any) (*tools.ToolResult, error)) *ToolBuilder {
	b.executor = fn
	return b
}

// WithValidator sets a custom parameter validator.
func (b *ToolBuilder) WithValidator(fn func(params map[string]any) error) *ToolBuilder {
	b.validator = fn
	return b
}

// WithIdempotent marks the tool as idempotent (safe to cache).
func (b *ToolBuilder) WithIdempotent(idempotent bool) *ToolBuilder {
	b.idempotent = idempotent
	return b
}

// WithPermissions sets the required permissions.
func (b *ToolBuilder) WithPermissions(perms ...tools.Permission) *ToolBuilder {
	b.permissions = perms
	return b
}

// WithContentTypes sets the supported content types.
func (b *ToolBuilder) WithContentTypes(types ...tools.ContentType) *ToolBuilder {
	b.contentTypes = types
	return b
}

// WithHints sets optimisation hints.
func (b *ToolBuilder) WithHints(hints *tools.OptimizationHints) *ToolBuilder {
	b.hints = hints
	return b
}

// WithDefer marks the tool for deferred loading.
func (b *ToolBuilder) WithDefer(deferred bool) *ToolBuilder {
	b.shouldDefer = deferred
	return b
}

// WithConcurrency marks the tool as concurrency-safe.
func (b *ToolBuilder) WithConcurrency(safe bool) *ToolBuilder {
	b.concurrencySafe = safe
	return b
}

// WithExamples adds input examples.
func (b *ToolBuilder) WithExamples(examples []tools.ToolExample) *ToolBuilder {
	b.examples = examples
	return b
}

// WithCategory sets the tool's category for search and grouping.
func (b *ToolBuilder) WithCategory(category string) *ToolBuilder {
	b.category = category
	return b
}

// WithTags adds free-form tags for search.
func (b *ToolBuilder) WithTags(tags ...string) *ToolBuilder {
	b.tags = tags
	return b
}

// WithSource sets the tool source (builtin, mcp, config, custom).
func (b *ToolBuilder) WithSource(source tools.ToolSource) *ToolBuilder {
	b.source = source
	return b
}

// WithServerName sets the MCP server name (for MCP-sourced tools).
func (b *ToolBuilder) WithServerName(name string) *ToolBuilder {
	b.serverName = name
	return b
}

// WithVersion sets the tool version string.
func (b *ToolBuilder) WithVersion(version string) *ToolBuilder {
	b.version = version
	return b
}

// WithAuthor sets the tool author.
func (b *ToolBuilder) WithAuthor(author string) *ToolBuilder {
	b.author = author
	return b
}

// Build creates the tool. Returns an error if name or executor is not set.
func (b *ToolBuilder) Build() (*BuiltTool, error) {
	if b.name == "" {
		return nil, fmt.Errorf("advanced.ToolBuilder: name is required")
	}
	if b.executor == nil {
		return nil, fmt.Errorf("advanced.ToolBuilder: executor is required (use WithExecutor)")
	}

	return &BuiltTool{
		name:            b.name,
		description:     b.description,
		parameters:      b.parameters,
		executor:        b.executor,
		validator:       b.validator,
		idempotent:      b.idempotent,
		permissions:     b.permissions,
		contentTypes:    b.contentTypes,
		hints:           b.hints,
		shouldDefer:     b.shouldDefer,
		concurrencySafe: b.concurrencySafe,
		examples:        b.examples,
		category:        b.category,
		tags:            b.tags,
		source:          b.source,
		serverName:      b.serverName,
		version:         b.version,
		author:          b.author,
	}, nil
}

// ---------------------------------------------------------------------------
// BuiltTool — implements tools.Tool + all advanced interfaces
// ---------------------------------------------------------------------------

// BuiltTool is the product of ToolBuilder. It implements:
//   - tools.Tool
//   - tools.ToolWithExamples (if examples are set)
//   - tools.ParallelCapable
//   - tools.MetadataProvider
//   - advanced.Deferrable
type BuiltTool struct {
	name            string
	description     string
	parameters      any
	executor        func(ctx context.Context, params map[string]any) (*tools.ToolResult, error)
	validator       func(params map[string]any) error
	idempotent      bool
	permissions     []tools.Permission
	contentTypes    []tools.ContentType
	hints           *tools.OptimizationHints
	shouldDefer     bool
	concurrencySafe bool
	examples        []tools.ToolExample
	category        string
	tags            []string
	source          tools.ToolSource
	serverName      string
	version         string
	author          string
}

// --- tools.Tool ---

func (t *BuiltTool) Name() string        { return t.name }
func (t *BuiltTool) Description() string { return t.description }
func (t *BuiltTool) Parameters() any     { return t.parameters }
func (t *BuiltTool) IsIdempotent() bool  { return t.idempotent }

func (t *BuiltTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return t.executor(ctx, params)
}

func (t *BuiltTool) Validate(params map[string]any) error {
	if t.validator != nil {
		return t.validator(params)
	}
	// If no custom validator and no parameter schema, allow nil params
	if t.parameters == nil && params == nil {
		return nil
	}
	// If there is a parameter schema, params must not be nil
	if params == nil {
		return fmt.Errorf("parameters cannot be nil")
	}
	return nil
}

func (t *BuiltTool) RequiresPermission() []tools.Permission {
	if t.permissions == nil {
		return []tools.Permission{}
	}
	return t.permissions
}

func (t *BuiltTool) SupportedContentTypes() []tools.ContentType {
	if t.contentTypes == nil {
		return []tools.ContentType{tools.ContentTypeText}
	}
	return t.contentTypes
}

func (t *BuiltTool) OptimizationHints() *tools.OptimizationHints {
	return t.hints
}

// --- tools.ToolWithExamples ---

func (t *BuiltTool) InputExamples() []tools.ToolExample {
	return t.examples
}

// --- tools.ParallelCapable ---

func (t *BuiltTool) SupportsParallel() bool {
	return t.concurrencySafe
}

// --- advanced.Deferrable ---

func (t *BuiltTool) ShouldDefer() bool {
	return t.shouldDefer
}

// --- tools.MetadataProvider ---

func (t *BuiltTool) ToolMetadata() *tools.ToolMetadata {
	return &tools.ToolMetadata{
		Version:          t.version,
		Author:           t.author,
		Category:         t.category,
		Tags:             t.tags,
		Source:           t.source,
		ServerName:       t.serverName,
		SupportsParallel: t.concurrencySafe,
	}
}
