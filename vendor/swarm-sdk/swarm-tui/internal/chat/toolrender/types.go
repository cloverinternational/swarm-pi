package toolrender

import "time"

// CachedResult is stored on Message for pre-processed tool output.
type CachedResult interface {
	GetRenderedLines() []string
	NeedsRerender(width int) bool
}

// Renderer is the interface that all tool renderers must implement.
// Each renderer handles a specific tool type (Bash, Read, Edit, Patch, etc.)
// and knows how to detect whether it can render a given tool result, how to
// pre-process the result for caching, and how to produce the final styled lines.
type Renderer interface {
	// CanRender returns true if this renderer handles the given tool context.
	// Implementations match on ToolName, Params, and/or Output content heuristics.
	CanRender(ctx *RenderContext) bool

	// Render produces styled output lines ready for display.
	// If a cached CachedResult is provided and still valid, implementations
	// should use it for instant rendering. Otherwise they render from scratch.
	Render(ctx *RenderContext, cached CachedResult) []string

	// PreProcess computes a CachedResult that can be stored on the message
	// for efficient re-rendering. Returns nil if stateless (no caching needed).
	PreProcess(ctx *RenderContext) CachedResult
}

// Attachment represents an image/file attached to a tool result.
type Attachment struct {
	FilePath string
	FileName string
	MimeType string
	Content  []byte
	Size     int64
}

// LiveTerminalLine is one line of live terminal output (ANSI preserved).
type LiveTerminalLine struct {
	Stream  string // "stdout" or "stderr"
	Content string
}

// LiveTerminal carries a live snapshot of a backgrounded bash process so the
// bash renderer can draw an inline "terminal window" that updates while the
// command runs. It is populated by the app render layer (which can reach the
// background process manager); renderers stay pure and never call the SDK.
// When nil, the bash renderer falls back to the static backgrounded status.
type LiveTerminal struct {
	TaskID   string
	Command  string
	Status   string // process state, e.g. "running", "completed", "failed"
	Running  bool
	PID      int
	ExitCode *int
	Elapsed  time.Duration
	Lines    []LiveTerminalLine
}

// RenderContext is the unified input to all renderers.
type RenderContext struct {
	ToolName    string
	Output      string         // tool result output
	Error       string         // tool error (if any)
	Params      map[string]any // tool call parameters (nil for legacy paths)
	Metadata    map[string]any // tool result metadata (e.g., diff data from hashline edit)
	CallID      string
	Width       int
	BgColor     string // hex color code (e.g. "#0E1118") or empty
	IsActive    bool   // true = currently streaming
	ShowFull    bool   // true = user toggled verbose mode (Ctrl+O)
	Attachments []Attachment
	// LiveTerminal is set only for backgrounded bash results with a live process;
	// nil for every other tool/result (zero behavior change).
	LiveTerminal *LiveTerminal
}
