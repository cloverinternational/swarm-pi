package chat

// BashResult represents the result of a bash command execution
type BashResult struct {
	Command    string
	Output     string
	Error      string
	ExitCode   int
	DurationMs int64          // Execution duration in milliseconds
	Metadata   map[string]any // Rich metadata from SDK (exit_code, duration_ms, truncated, etc.)
}
