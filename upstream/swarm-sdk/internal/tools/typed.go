package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// TypedTool is the interface for tools with strongly-typed parameters.
//
// P is the params struct type — it must be JSON-decodable (i.e. it should have
// json struct tags on its fields). The SDK receives raw map[string]any from the
// LLM and automatically decodes it into P before calling Run.
//
// # Why use TypedTool instead of Tool?
//
// The plain [Tool] interface requires Execute(ctx, map[string]any), forcing every
// tool author to write 5-8 lines of type-assertion boilerplate per parameter:
//
//	value, ok := params["path"]          // check existence
//	if !ok { return nil, error }         // handle missing
//	path, ok := value.(string)           // type-assert
//	if !ok { return nil, error }         // handle wrong type
//
// With TypedTool, the SDK decodes params into P automatically. Your Run method
// receives a typed struct with no boilerplate:
//
//	func (t *ReadTool) Run(ctx context.Context, p ReadParams) (*ToolResult, error) {
//	    data, err := os.ReadFile(p.Path)  // p.Path is already a string
//	    ...
//	}
//
// # How to implement
//
// 1. Define a params struct with json tags:
//
//	type ReadParams struct {
//	    Path string `json:"path" description:"Absolute file path" required:"true"`
//	}
//
// 2. Implement TypedTool[ReadParams]:
//
//	type ReadTool struct{ tools.BaseTool }
//
//	func (t *ReadTool) Name() string        { return "read_file" }
//	func (t *ReadTool) Description() string { return "Read a file from disk" }
//	func (t *ReadTool) Parameters() any     { return tools.SchemaFor[ReadParams]() }
//	func (t *ReadTool) Run(ctx context.Context, p ReadParams) (*ToolResult, error) {
//	    data, err := os.ReadFile(p.Path)
//	    if err != nil {
//	        return nil, err
//	    }
//	    return tools.NewToolResult(string(data)), nil
//	}
//
// 3. Register using [Typed]:
//
//	registry.Register(tools.Typed[ReadParams](&ReadTool{}))
type TypedTool[P any] interface {
	// Name returns the unique tool identifier.
	Name() string

	// Description returns a human-readable description shown to the LLM.
	Description() string

	// Parameters returns the JSON Schema for the tool's parameters.
	// Use [SchemaFor][P]() to generate it automatically from struct tags.
	Parameters() any

	// Run executes the tool with decoded, typed parameters.
	// P is populated from the LLM's JSON parameter map before Run is called.
	Run(ctx context.Context, params P) (*ToolResult, error)
}

// Typed wraps a [TypedTool][P] as a plain [Tool] so it can be registered in any
// [Registry]. The adapter decodes the raw map[string]any the LLM provides into
// your typed P struct via a JSON round-trip before calling Run.
//
// Usage:
//
//	registry.Register(tools.Typed[MyParams](&MyTool{}))
func Typed[P any](t TypedTool[P]) Tool {
	return &typedAdapter[P]{inner: t}
}

// Func creates a [Tool] from a plain function, eliminating the need to define
// a full struct for simple one-off tools.
//
// P is the typed params struct — define it with json and description tags
// exactly as you would for [TypedTool]. [SchemaFor][P] is called automatically.
//
// Example:
//
//	type WeatherParams struct {
//	    City string `json:"city" description:"City name" required:"true"`
//	}
//
//	reg.Register(tools.Func[WeatherParams]("get_weather", "Get the current weather",
//	    func(ctx context.Context, p WeatherParams) (*tools.ToolResult, error) {
//	        return tools.NewToolResult("Sunny, 22°C in " + p.City), nil
//	    },
//	))
func Func[P any](name, description string, fn func(context.Context, P) (*ToolResult, error)) Tool {
	return Typed[P](&funcTool[P]{name: name, description: description, fn: fn})
}

// funcTool[P] backs [Func]. It is private; callers interact through the [Tool] interface.
type funcTool[P any] struct {
	BaseTool
	name        string
	description string
	fn          func(context.Context, P) (*ToolResult, error)
}

func (t *funcTool[P]) Name() string        { return t.name }
func (t *funcTool[P]) Description() string { return t.description }
func (t *funcTool[P]) Parameters() any     { return SchemaFor[P]() }
func (t *funcTool[P]) Run(ctx context.Context, p P) (*ToolResult, error) {
	return t.fn(ctx, p)
}

// Implement this when your tool can stream stdout/stderr line-by-line.
//
// The adapter automatically detects this interface and routes the raw
// [StreamingTool.ExecuteStreaming] call through the JSON bridge so
// RunStreaming always receives a fully-typed P — no manual map decode needed.
//
// BashTool is the canonical example — it delegates both Run and RunStreaming
// to a shared internal helper:
//
//	func (t *BashTool) RunStreaming(ctx context.Context, p BashParams, onOutput func(string, string)) (*ToolResult, error) {
//		return t.run(ctx, p, onOutput)
//	}
type TypedStreamingTool[P any] interface {
	TypedTool[P]
	// RunStreaming executes the tool and emits output incrementally.
	// onOutput is called for each chunk; stream is "stdout" or "stderr".
	RunStreaming(ctx context.Context, params P, onOutput func(chunk string, stream string)) (*ToolResult, error)
}

// TypedValidator is an optional companion interface for [TypedTool][P].
// When the inner tool implements TypedValidator[P], the [typedAdapter] decodes
// the raw map[string]any into P before calling ValidateTyped — so validation
// receives the same fully-typed struct as [TypedTool.Run].
//
// If the tool only needs simple untyped validation it can instead implement
// Validate(map[string]any) error directly (legacy path, still supported).
//
// Example:
//
//	func (t *SearchTool) ValidateTyped(ctx context.Context, p SearchParams) error {
//		if p.Query == "" {
//			return errors.New("query is required")
//		}
//		return nil
//	}
type TypedValidator[P any] interface {
	ValidateTyped(ctx context.Context, params P) error
}

// typedAdapter bridges TypedTool[P] → Tool.
// It satisfies both Tool and all optional capability interfaces when the
// underlying TypedTool also satisfies them.
type typedAdapter[P any] struct {
	inner TypedTool[P]
}

func (a *typedAdapter[P]) Name() string        { return a.inner.Name() }
func (a *typedAdapter[P]) Description() string { return a.inner.Description() }
func (a *typedAdapter[P]) Parameters() any     { return a.inner.Parameters() }

// Execute implements [Tool]. It decodes rawParams into P via JSON and calls Run.
//
// Decoding strategy: json.Marshal(rawParams) → json.Unmarshal(bytes, &P{}).
// This correctly handles the float64-for-all-numbers quirk that arises when
// JSON is first decoded into map[string]any (json.Unmarshal into a typed int
// field will convert float64(42) to int(42) automatically).
func (a *typedAdapter[P]) Execute(ctx context.Context, rawParams map[string]any) (*ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, &ParamDecodeError{Tool: a.inner.Name(), Cause: fmt.Errorf("marshal: %w", err)}
	}
	var params P
	if err := json.Unmarshal(b, &params); err != nil {
		return nil, &ParamDecodeError{Tool: a.inner.Name(), Cause: fmt.Errorf("unmarshal: %w", err)}
	}
	return a.inner.Run(ctx, params)
}

// Forward optional capability interfaces from the underlying TypedTool[P].
// These are checked via type assertion at runtime, so they only activate when
// the underlying tool actually implements the relevant interface.

// Validate implements [ValidatableTool].
//
// If the inner tool implements [TypedValidator][P], params are decoded to P
// via JSON before calling ValidateTyped — the same type-safe path used by Run.
//
// Falls through to the legacy Validate(map[string]any) interface if present.
// Returns nil when neither interface is implemented.
func (a *typedAdapter[P]) Validate(params map[string]any) error {
	// Preferred path: typed validation — decode params to P first.
	if tv, ok := a.inner.(TypedValidator[P]); ok {
		b, err := json.Marshal(params)
		if err != nil {
			return &ParamDecodeError{Tool: a.inner.Name(), Cause: fmt.Errorf("marshal: %w", err)}
		}
		var p P
		if err := json.Unmarshal(b, &p); err != nil {
			return &ParamDecodeError{Tool: a.inner.Name(), Cause: fmt.Errorf("unmarshal: %w", err)}
		}
		return tv.ValidateTyped(context.Background(), p)
	}
	// Legacy path: untyped validation (raw map[string]any).
	type validatable interface {
		Validate(map[string]any) error
	}
	if v, ok := a.inner.(validatable); ok {
		return v.Validate(params)
	}
	return nil
}

// IsIdempotent implements [IdempotentTool] if the inner tool does.
func (a *typedAdapter[P]) IsIdempotent() bool {
	type idempotent interface{ IsIdempotent() bool }
	if v, ok := a.inner.(idempotent); ok {
		return v.IsIdempotent()
	}
	return false
}

// RequiresPermission implements [PermissionedTool] if the inner tool does.
func (a *typedAdapter[P]) RequiresPermission() []Permission {
	type permissioned interface{ RequiresPermission() []Permission }
	if v, ok := a.inner.(permissioned); ok {
		return v.RequiresPermission()
	}
	return nil
}

// SupportedContentTypes implements [ContentTypedTool] if the inner tool does.
func (a *typedAdapter[P]) SupportedContentTypes() []ContentType {
	type contentTyped interface{ SupportedContentTypes() []ContentType }
	if v, ok := a.inner.(contentTyped); ok {
		return v.SupportedContentTypes()
	}
	return nil
}

// OptimizationHints implements [HintedTool] if the inner tool does.
func (a *typedAdapter[P]) OptimizationHints() *OptimizationHints {
	type hinted interface{ OptimizationHints() *OptimizationHints }
	if v, ok := a.inner.(hinted); ok {
		return v.OptimizationHints()
	}
	return nil
}

// SupportsParallel implements [ParallelCapable] if the inner tool does.
func (a *typedAdapter[P]) SupportsParallel() bool {
	type parallel interface{ SupportsParallel() bool }
	if v, ok := a.inner.(parallel); ok {
		return v.SupportsParallel()
	}
	return false
}

// ExecuteStreaming implements [StreamingTool] routing.
//
// If the inner tool implements [TypedStreamingTool][P], the raw params are decoded
// into P via the JSON bridge and RunStreaming is called — this is the preferred path
// because the tool receives a fully-typed struct, not a raw map.
//
// If the inner tool only implements the legacy untyped ExecuteStreaming interface,
// we fall through to that. If neither, we fall back to non-streaming Execute.
func (a *typedAdapter[P]) ExecuteStreaming(
	ctx context.Context,
	rawParams map[string]any,
	onOutput func(chunk string, stream string),
) (*ToolResult, error) {
	// Preferred path: typed streaming — decode once, call RunStreaming with typed P.
	if st, ok := a.inner.(TypedStreamingTool[P]); ok {
		b, err := json.Marshal(rawParams)
		if err != nil {
			return nil, &ParamDecodeError{Tool: a.inner.Name(), Cause: fmt.Errorf("marshal: %w", err)}
		}
		var params P
		if err := json.Unmarshal(b, &params); err != nil {
			return nil, &ParamDecodeError{Tool: a.inner.Name(), Cause: fmt.Errorf("unmarshal: %w", err)}
		}
		return st.RunStreaming(ctx, params, onOutput)
	}
	// Legacy path: untyped ExecuteStreaming (passes raw map — tool decodes internally).
	type legacyStreaming interface {
		ExecuteStreaming(context.Context, map[string]any, func(string, string)) (*ToolResult, error)
	}
	if st, ok := a.inner.(legacyStreaming); ok {
		return st.ExecuteStreaming(ctx, rawParams, onOutput)
	}
	// Fallback: non-streaming Execute (no incremental output, but tool still runs).
	return a.Execute(ctx, rawParams)
}

// ParamDecodeError is returned when the adapter fails to decode raw LLM params
// into the typed P struct.
type ParamDecodeError struct {
	Tool  string
	Cause error
}

func (e *ParamDecodeError) Error() string {
	return "tool " + e.Tool + ": failed to decode parameters: " + e.Cause.Error()
}

func (e *ParamDecodeError) Unwrap() error { return e.Cause }

// compile-time check: typedAdapter[any] must satisfy Tool.
var _ Tool = (*typedAdapter[any])(nil)
