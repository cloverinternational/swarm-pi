package builtin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tokens"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BashParams are the typed parameters for the bash tool.
type BashParams struct {
	Command        string            `json:"command"                   description:"Shell command to execute"                                                                    required:"true"`
	Cwd            string            `json:"cwd,omitempty"             description:"Working directory (use this instead of 'cd')"`
	Env            map[string]string `json:"env,omitempty"             description:"Extra environment variables to set"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" description:"Max seconds to wait (minimum 60; values below 60 are raised automatically)" default:"60"`
	Description    string            `json:"description,omitempty"     description:"Brief label for this command shown in the status header and TUI (5-10 words)"`
}

// BashTool implements command execution functionality with safety checks.
// It satisfies both [tools.TypedTool][BashParams] and [tools.TypedStreamingTool][BashParams].
type BashTool struct {
	tools.BaseTool

	shell        string   // Shell to use (bash, sh, etc.)
	allowedPaths []string // Optional working directory restrictions
	defaultCwd   string   // Default working directory when agent doesn't specify cwd
}

// BashToolConfig configures BashTool behavior.
type BashToolConfig struct {
	Shell        string
	AllowedPaths []string
	DefaultCwd   string // Default working directory for commands
}

// DefaultBashConfig returns default configuration.
func DefaultBashConfig() BashToolConfig {
	shell := "/bin/bash"
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
	}
	return BashToolConfig{
		Shell:        shell,
		AllowedPaths: defaultAllowedPaths(),
	}
}

// NewBashTool creates a BashTool with default configuration, wrapped as a typed Tool.
func NewBashTool() tools.Tool {
	return NewBashToolWithConfig(DefaultBashConfig())
}

// NewBashToolWithConfig creates a BashTool with custom configuration, wrapped as a typed Tool.
func NewBashToolWithConfig(config BashToolConfig) tools.Tool {
	if config.Shell == "" {
		config.Shell = DefaultBashConfig().Shell
	}
	t := &BashTool{
		shell:        config.Shell,
		allowedPaths: config.AllowedPaths,
		defaultCwd:   config.DefaultCwd,
	}
	return tools.Typed[BashParams](t)
}

// Name returns the tool name.
func (t *BashTool) Name() string { return "bash" }

// Description returns the tool description.
func (t *BashTool) Description() string {
	return `Execute a shell command and capture stdout, stderr, exit code, duration, and timeout status. Pipes and redirections are supported.

Set timeout_seconds for every command; values below the enforced 60-second minimum are raised automatically. Use cwd instead of cd.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BashTool) Parameters() any { return tools.SchemaFor[BashParams]() }

// IsIdempotent returns false — commands can modify state.
func (t *BashTool) IsIdempotent() bool { return false }

// RequiresPermission returns the required permissions for this tool.
func (t *BashTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionBashExecute}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BashTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BashTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ─── Public entry points ─────────────────────────────────────────────────────

// Run executes the command and returns the complete output (batch mode).
// Uses temp files for stdout/stderr to avoid buffer overflow on large outputs.
func (t *BashTool) Run(ctx context.Context, params BashParams) (*tools.ToolResult, error) {
	return t.runBatch(ctx, params)
}

// RunStreaming executes the command and emits output incrementally (streaming mode).
// Each line of stdout/stderr is forwarded to onOutput as it arrives.
// Uses OS pipes for zero-latency streaming.
func (t *BashTool) RunStreaming(ctx context.Context, params BashParams, onOutput func(chunk string, stream string)) (*tools.ToolResult, error) {
	return t.runStreaming(ctx, params, onOutput)
}

// ─── Shared setup helpers ────────────────────────────────────────────────────

// applyTimeout applies the requested timeout (minimum 60 s) to ctx.
// Returns the new context, its cancel func, the originally-requested seconds,
// and the effective seconds after clamping.
func applyTimeout(ctx context.Context, requestedSecs int) (context.Context, context.CancelFunc, int, int) {
	effectiveSecs := 60
	if requestedSecs > 0 {
		effectiveSecs = max(requestedSecs, 60)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(effectiveSecs)*time.Second)
	return ctx, cancel, requestedSecs, effectiveSecs
}

// resolveWorkdir validates and returns the absolute working directory.
func (t *BashTool) resolveWorkdir(cwd string) (string, error) {
	if cwd == "" {
		return t.defaultCwd, nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", sdkerr.Permanent("bash.invalid_cwd",
			fmt.Sprintf("Failed to resolve working directory: %v", err))
	}
	if err := t.checkPathAllowed(abs); err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", sdkerr.Permanent("bash.invalid_cwd",
				fmt.Sprintf("cwd does not exist: %s", abs))
		}
		return "", sdkerr.Permanent("bash.invalid_cwd",
			fmt.Sprintf("failed to access cwd %s: %v", abs, err))
	}
	if !info.IsDir() {
		return "", sdkerr.Permanent("bash.invalid_cwd",
			fmt.Sprintf("cwd is not a directory: %s", abs))
	}
	return abs, nil
}

// prepareCmd builds and configures the exec.Cmd (env, stdin, working dir).
func (t *BashTool) prepareCmd(ctx context.Context, params BashParams, resolvedCwd string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/C", params.Command)
	} else {
		cmd = buildBashCommand(ctx, t.shell, params.Command)
	}
	// WaitDelay ensures pipes are closed after process exit, preventing hangs
	// when child processes inherit stdout/stderr (Go issue #21922).
	cmd.WaitDelay = 2 * time.Second

	if resolvedCwd != "" {
		cmd.Dir = resolvedCwd
	}

	// Non-interactive environment.
	cmd.Env = append(os.Environ(),
		"TERM=dumb",
		"DEBIAN_FRONTEND=noninteractive",
		"CI=true",
		"PS1=",            // Suppress shell prompt (prevents custom PS1 from appearing in output with PTY)
		"PROMPT_COMMAND=", // Suppress prompt command (prevents extra output when using script command)
	)
	for k, v := range params.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	return cmd
}

// ─── Shared output helpers ───────────────────────────────────────────────────

// bashEnrichMetadata adds execution stats to the result's Metadata map.
// When outputPath is set (truncation occurred), full stdout/stderr are NOT
// stored in metadata — only the temp-file path is — to avoid bloating
// serialization with potentially multi-MB strings. The agent can read the
// full output from outputPath instead.
func bashEnrichMetadata(result *tools.ToolResult, exitCode int, duration time.Duration,
	stdout, stderr, output, desc string, timedOut bool,
	outputPath string,
) {
	result.Metadata["exit_code"] = exitCode
	result.Metadata["duration_ms"] = duration.Milliseconds()
	result.Metadata["output_lines"] = strings.Count(output, "\n")
	result.Metadata["output_bytes"] = len(output)
	if desc != "" {
		result.Metadata["description"] = desc
	}
	if timedOut {
		result.Metadata["timed_out"] = true
	}
	if outputPath != "" {
		// Output was truncated — store the file path instead of
		// the full strings so metadata stays small.
		result.Metadata["stdout"] = "(saved to file — see output_path)"
		result.Metadata["stderr"] = "(saved to file — see output_path)"
		result.Metadata["output_path"] = outputPath
		result.Metadata["truncated"] = true
	} else {
		result.Metadata["stdout"] = stdout
		result.Metadata["stderr"] = stderr
	}
}

// bashTruncateOutput truncates large output and optionally saves the full
// output to a temp file. Returns the (possibly truncated) output, the path
// to the full output file (empty if not truncated), and whether truncation occurred.
//
// Both caps are enforced independently: a single-line blob whose token
// estimate exceeds maxTokens trips the token cap even though it has only
// one newline, and a 3 000-line log trips maxLines even when each line is
// short. Prior to these, only the line cap was actually applied, so a
// `cat large_file` call could blow straight through and push hundreds of
// thousands of tokens into conversation history. The token cap is tuned
// to match the old 50KB byte cap (~12.5K tokens) — the goal is preventing
// context blowup, not disk I/O.
func bashTruncateOutput(output string) (out string, outputPath string, truncated bool, origBytes int, method string) {
	const (
		maxLines  = 2000
		maxTokens = 12_500
	)

	// P0.5 structured tool-output contract: capture the ORIGINAL byte count
	// before any truncation so downstream (Harbor event bridge / analysis) can
	// attribute how much was dropped and which cap fired.
	origBytes = len(output)
	method = "none"

	lineCount := strings.Count(output, "\n")
	outputTokens := tokens.Estimate(output)
	if lineCount <= maxLines && outputTokens <= maxTokens {
		return output, "", false, origBytes, "none"
	}

	truncated = true
	outputDir := filepath.Join(os.TempDir(), "swarm-tool-output")
	os.MkdirAll(outputDir, 0o755)
	if tmp, err := os.CreateTemp(outputDir, "bash-full-*.txt"); err == nil {
		tmp.WriteString(output)
		tmp.Close()
		outputPath = tmp.Name()
	}

	// Track which cap(s) actually fired so the contract can report the exact
	// truncation method: "lines", "tokens", or "lines+tokens".
	var lineCapFired, tokenCapFired bool

	// Line cap first: easy to explain what was dropped.
	if lineCount > maxLines {
		lines := strings.SplitN(output, "\n", maxLines+1)
		if len(lines) > maxLines {
			truncatedLines := lineCount - maxLines
			output = strings.Join(lines[:maxLines], "\n")
			output += fmt.Sprintf(
				"\n\n...%d lines truncated...\n\nFull output saved to: %s\nUse `sed -n 'START,ENDp' FILE` to view sections, or `rg PATTERN FILE` to search within it.",
				truncatedLines, outputPath,
			)
			lineCapFired = true
		}
	}

	// Token cap second: may fire alone (one-line blob) or after the line
	// cap if the surviving prefix is still oversized.
	if tokens.Estimate(output) > maxTokens {
		droppedTokens := tokens.Estimate(output) - maxTokens
		output = tokens.Truncate(output, maxTokens) + fmt.Sprintf(
			"\n\n...~%d tokens truncated...\n\nFull output saved to: %s\nUse `sed -n 'START,ENDp' FILE` to view sections, or `rg PATTERN FILE` to search within it.",
			droppedTokens, outputPath,
		)
		tokenCapFired = true
	}

	switch {
	case lineCapFired && tokenCapFired:
		method = "lines+tokens"
	case lineCapFired:
		method = "lines"
	case tokenCapFired:
		method = "tokens"
	}
	return output, outputPath, true, origBytes, method
}

// mergeOutput combines stdout and stderr into a single string.
// ansiEscapeRegex matches CSI escape sequences: ESC [ params final-byte. This
// covers both SGR (color/style, final byte 'm') and cursor/erase control
// (e.g. \x1b[2K, \x1b[1A) that progress-bar-heavy tools like npm/pip/apt emit.
// Subprocesses run under the PTY-forcing `script` buffering fallback (used when
// stdbuf is unavailable, see bash_cmd_builder.go) auto-detect a terminal and may
// emit these even though the caller never asked for color. Left unstripped, the
// raw escape bytes reach the model as literal control characters wrapping the
// highlighted text (e.g. ripgrep match highlighting), which is at best noise and
// at worst gets misread as if the wrapped substring were missing. Stripping here
// is the single choke point both non-streaming and streaming execution paths
// funnel through (see buildResult/bashCommandFailedError callers below).
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;:?]*[A-Za-z]`)

// StripANSI removes CSI escape sequences from captured command output before it
// is ever placed in a ToolResult or error the model can see. Exported so other
// packages that build their own Bash-like tool result (e.g.
// swarm-tui/internal/bgprocess, which wraps this same tool) can reuse the
// exact same sanitization instead of drifting out of sync with a duplicate.
func StripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s // fast path: no escape bytes at all
	}
	return ansiEscapeRegex.ReplaceAllString(s, "")
}

// stripANSI is the package-local alias used by this file's call sites.
func stripANSI(s string) string { return StripANSI(s) }

func mergeOutput(stdout, stderr string) string {
	if len(stderr) == 0 {
		return stdout
	}
	if len(stdout) == 0 {
		return stderr
	}
	return stdout + "\n" + stderr
}

// bashCommandFailedError retains bounded excerpts of both captured streams in
// the error for callers that render only errors and discard the ToolResult.
func bashCommandFailedError(exitCode int, cmdErr error, stdout, stderr string) error {
	stdout = stripANSI(stdout)
	stderr = stripANSI(stderr)
	stdoutExcerpt, _, _, _, _ := bashTruncateOutput(stdout)
	stderrExcerpt, _, _, _, _ := bashTruncateOutput(stderr)
	if stdoutExcerpt == "" {
		stdoutExcerpt = "(no output)"
	}
	if stderrExcerpt == "" {
		stderrExcerpt = "(no output)"
	}

	return sdkerr.Permanent("bash.command_failed",
		fmt.Sprintf("Command exited with code %d: %v\n\nstderr:\n%s\n\nstdout:\n%s",
			exitCode, cmdErr, stderrExcerpt, stdoutExcerpt))
}

// buildResult assembles the final ToolResult as structured XML with header metadata
// as attributes and stdout/stderr as CDATA child elements.
func buildResult(
	exitCode int, duration time.Duration,
	requestedSecs, effectiveSecs int,
	stdout, stderr, desc, command string,
	timedOut bool,
) *tools.ToolResult {
	// Strip any ANSI/SGR escape codes before anything downstream (merge,
	// truncation, XML fields, metadata enrichment) sees the text. See
	// stripANSI's doc comment for why these can appear even though the
	// caller never requested color.
	stdout = stripANSI(stdout)
	stderr = stripANSI(stderr)

	// Apply truncation to merged output (preserves existing temp-file behaviour).
	merged := mergeOutput(stdout, stderr)
	if merged == "" {
		merged = "(no output)"
	}
	truncatedMerged, outputPath, truncated, origBytes, truncMethod := bashTruncateOutput(merged)

	b := tools.NewXML("result").
		AttrInt("exit_code", int64(exitCode)).
		AttrInt("duration_ms", duration.Milliseconds()).
		AttrBool("timed_out", timedOut)
	if desc != "" {
		b.Attr("description", desc)
	}
	if requestedSecs > 0 && requestedSecs < 60 {
		b.SelfClose("timeout_clamped",
			fmt.Sprintf(`requested_seconds="%d"`, requestedSecs),
			fmt.Sprintf(`effective_seconds="%d"`, effectiveSecs))
	}
	if truncated {
		truncMsg := fmt.Sprintf(
			"(output truncated — full content saved to output_path)\n"+
				"Full output saved to: %s\n"+
				"Use `sed -n 'START,ENDp' FILE` to view sections, or `rg PATTERN FILE` to search within it.",
			outputPath)
		b.Field("stdout", truncMsg)
		b.Field("stderr", truncMsg)
		b.SelfClose("output_file", fmt.Sprintf(`path=%q`, outputPath))
	} else {
		b.Field("stdout", stdout)
		b.Field("stderr", stderr)
	}

	result := tools.NewXMLResult(b).WithDuration(duration.Milliseconds())
	result.Metadata["command"] = command
	bashEnrichMetadata(result, exitCode, duration, stdout, stderr, truncatedMerged, desc, timedOut, outputPath)

	// P0.5 structured tool-output contract (versioned). These additive keys let
	// downstream consumers (Harbor event bridge / analysis pipeline) attribute
	// truncation to the original size + method and reconstruct the full output
	// from full_output_path — so a later successful command can never obscure
	// an earlier decisive error, and no decisive output is silently lost.
	result.Metadata["original_output_bytes"] = origBytes
	result.Metadata["returned_output_bytes"] = len(truncatedMerged)
	result.Metadata["truncation_method"] = truncMethod
	result.Metadata["output_contract"] = map[string]any{
		"version":               1,
		"exit_code":             exitCode,
		"duration_ms":           duration.Milliseconds(),
		"timed_out":             timedOut,
		"truncated":             truncated,
		"truncation_method":     truncMethod,
		"original_output_bytes": origBytes,
		"returned_output_bytes": len(truncatedMerged),
		"full_output_path":      outputPath, // "" when not truncated
	}

	// Typed structured outcome (G4). exitCode here is the real
	// ProcessState.ExitCode() the caller just read — this is the one site in
	// the process that holds it as an integer. Everything downstream (the
	// AfterTool hook event, and anything computing objective terminal facts
	// from it) reads this typed value instead of regexing it back out of the
	// rendered XML above, which is why the map form alone was not enough:
	// Metadata is map[string]any and any consumer of it is one refactor away
	// from silently getting nothing.
	//
	// Both the batch and streaming execution paths funnel through
	// buildResult, so both are covered by this single assignment. Purely
	// observational: nothing below reads Outcome, so the command's behaviour,
	// output, and error are byte-identical to before.
	result.Outcome = toolout.Command(exitCode, duration.Milliseconds(), timedOut)
	return result
}

// ─── Batch execution (temp files) ───────────────────────────────────────────

func (t *BashTool) runBatch(ctx context.Context, params BashParams) (*tools.ToolResult, error) {
	// Early context check.
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(fmt.Errorf("command canceled: %w", ctx.Err()), "bash.context_cancelled")
	default:
	}

	ctx, cancel, requestedSecs, effectiveSecs := applyTimeout(ctx, params.TimeoutSeconds)
	defer cancel()

	resolvedCwd, err := t.resolveWorkdir(params.Cwd)
	if err != nil {
		return nil, err
	}

	// Temp files for stdout/stderr avoid buffer overflow on large outputs.
	stdoutFile, err := os.CreateTemp(os.TempDir(), "bash-stdout-*.txt")
	if err != nil {
		return nil, sdkerr.Permanent("bash.tmpfile_failed",
			fmt.Sprintf("Failed to create stdout temp file: %v", err))
	}
	stdoutPath := stdoutFile.Name()
	defer os.Remove(stdoutPath)

	stderrFile, err := os.CreateTemp(os.TempDir(), "bash-stderr-*.txt")
	if err != nil {
		stdoutFile.Close()
		os.Remove(stdoutPath)
		return nil, sdkerr.Permanent("bash.tmpfile_failed",
			fmt.Sprintf("Failed to create stderr temp file: %v", err))
	}
	stderrPath := stderrFile.Name()
	defer os.Remove(stderrPath)

	cmd := t.prepareCmd(ctx, params, resolvedCwd)
	if devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0); err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	startTime := time.Now()
	cmdErr := cmd.Run()
	duration := time.Since(startTime)

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	stdoutFile.Close()
	stderrFile.Close()
	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stderrBytes, _ := os.ReadFile(stderrPath)
	stdout := string(stdoutBytes)
	stderr := string(stderrBytes)

	if cmdErr != nil {
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		result := buildResult(exitCode, duration, requestedSecs, effectiveSecs, stdout, stderr, params.Description, params.Command, timedOut)
		if ctx.Err() != nil {
			if timedOut {
				return result, sdkerr.Wrap(fmt.Errorf("command timed out after %ds (exit_code=%d): %w", effectiveSecs, exitCode, ctx.Err()), "bash.timeout")
			}
			return result, sdkerr.Wrap(fmt.Errorf("command canceled: %w", ctx.Err()), "bash.context_cancelled")
		}
		return result, bashCommandFailedError(exitCode, cmdErr, stdout, stderr)
	}

	result := buildResult(exitCode, duration, requestedSecs, effectiveSecs, stdout, stderr, params.Description, params.Command, false)
	result.Metadata["stdout_file"] = stdoutPath
	result.Metadata["stderr_file"] = stderrPath
	return result, nil
}

// ─── Streaming execution (OS pipes) ─────────────────────────────────────────

// runStreaming runs the command with incremental output delivery.
// Each line of stdout/stderr is emitted via onOutput as it arrives.
func (t *BashTool) runStreaming(ctx context.Context, params BashParams, onOutput func(chunk string, stream string)) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(fmt.Errorf("command canceled: %w", ctx.Err()), "bash.context_cancelled")
	default:
	}

	ctx, cancel, requestedSecs, effectiveSecs := applyTimeout(ctx, params.TimeoutSeconds)
	defer cancel()

	resolvedCwd, err := t.resolveWorkdir(params.Cwd)
	if err != nil {
		return nil, err
	}

	cmd := t.prepareCmd(ctx, params, resolvedCwd)
	devNull, devNullErr := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if devNullErr == nil {
		cmd.Stdin = devNull
	}

	// OS pipes for zero-latency line-by-line streaming.
	// Must be attached before cmd.Start(); must be fully drained before cmd.Wait().
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, sdkerr.Permanent("bash.pipe_failed",
			fmt.Sprintf("Failed to create stdout pipe: %v", err))
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, sdkerr.Permanent("bash.pipe_failed",
			fmt.Sprintf("Failed to create stderr pipe: %v", err))
	}

	// Create temp files so large streaming output doesn't OOM.
	// The in-memory builders are capped at streamMemCap bytes;
	// everything beyond that goes only to the temp files.
	const streamMemCap = 100 << 10 // 100 KB — well above display needs, well below OOM risk

	outputDir := filepath.Join(os.TempDir(), "swarm-tool-output")
	os.MkdirAll(outputDir, 0o755)

	stdoutTmp, err := os.CreateTemp(outputDir, "bash-stream-stdout-*.txt")
	if err != nil {
		return nil, sdkerr.Permanent("bash.tmpfile_failed",
			fmt.Sprintf("Failed to create streaming stdout temp file: %v", err))
	}
	stdoutTmpPath := stdoutTmp.Name()

	stderrTmp, err := os.CreateTemp(outputDir, "bash-stream-stderr-*.txt")
	if err != nil {
		stdoutTmp.Close()
		os.Remove(stdoutTmpPath)
		return nil, sdkerr.Permanent("bash.tmpfile_failed",
			fmt.Sprintf("Failed to create streaming stderr temp file: %v", err))
	}
	stderrTmpPath := stderrTmp.Name()

	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		stdoutTmp.Close()
		stderrTmp.Close()
		os.Remove(stdoutTmpPath)
		os.Remove(stderrTmpPath)
		return nil, sdkerr.Permanent("bash.start_failed",
			fmt.Sprintf("Failed to start command: %v", err))
	}

	// Accumulate stdout and stderr for the final ToolResult.
	// In-memory builders are capped at streamMemCap; temp files get everything.
	var stdoutBuf, stderrBuf strings.Builder
	var stdoutCapped, stderrCapped bool
	var mu sync.Mutex

	// streamPipe reads from a pipe line-by-line, writes to the temp file,
	// emits each line via onOutput, and appends to the in-memory builder
	// (until the cap is hit).
	streamPipe := func(r io.Reader, stream string, tmpFile *os.File, buf *strings.Builder, capped *bool) {
		reader := bufio.NewReader(r)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				// Always write to temp file (no cap).
				tmpFile.WriteString(line)

				mu.Lock()
				if !*capped {
					if buf.Len()+len(line) > streamMemCap {
						*capped = true
						// Don't append this line — we've hit the cap.
					} else {
						buf.WriteString(line)
					}
				}
				mu.Unlock()

				if onOutput != nil {
					onOutput(line, stream)
				}
			}
			if err != nil {
				return
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); streamPipe(stdoutPipe, "stdout", stdoutTmp, &stdoutBuf, &stdoutCapped) }()
	go func() { defer wg.Done(); streamPipe(stderrPipe, "stderr", stderrTmp, &stderrBuf, &stderrCapped) }()

	wg.Wait() // Drain fully before Wait (required by Go docs).
	cmdErr := cmd.Wait()

	// Close temp files; read them back if in-memory buffers were capped.
	stdoutTmp.Close()
	stderrTmp.Close()

	if devNullErr == nil {
		devNull.Close()
	}

	duration := time.Since(startTime)
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	// If in-memory buffers were capped, the full output lives in the temp
	// files. Read them back so buildResult/bashTruncateOutput can save to
	// the canonical output file and truncate properly.
	var stdout, stderr string
	if stdoutCapped || stderrCapped {
		if sb, err := os.ReadFile(stdoutTmpPath); err == nil {
			stdout = string(sb)
		} else {
			stdout = stdoutBuf.String()
		}
		if sb, err := os.ReadFile(stderrTmpPath); err == nil {
			stderr = string(sb)
		} else {
			stderr = stderrBuf.String()
		}
		// Clean up stream temp files — the canonical file is created by
		// bashTruncateOutput inside buildResult.
		os.Remove(stdoutTmpPath)
		os.Remove(stderrTmpPath)
	} else {
		stdout = stdoutBuf.String()
		stderr = stderrBuf.String()
		// Small output — remove temp files, nothing to persist.
		os.Remove(stdoutTmpPath)
		os.Remove(stderrTmpPath)
	}

	if cmdErr != nil {
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		result := buildResult(exitCode, duration, requestedSecs, effectiveSecs, stdout, stderr, params.Description, params.Command, timedOut)
		if ctx.Err() != nil {
			if timedOut {
				return result, sdkerr.Wrap(fmt.Errorf("command timed out after %ds (exit_code=%d): %w", effectiveSecs, exitCode, ctx.Err()), "bash.timeout")
			}
			return result, sdkerr.Wrap(fmt.Errorf("command canceled: %w", ctx.Err()), "bash.context_cancelled")
		}
		return result, bashCommandFailedError(exitCode, cmdErr, stdout, stderr)
	}

	return buildResult(exitCode, duration, requestedSecs, effectiveSecs, stdout, stderr, params.Description, params.Command, false), nil
}

// checkPathAllowed verifies the path is within allowed paths (if configured).
func (t *BashTool) checkPathAllowed(absPath string) error {
	return checkAllowedPath(absPath, t.allowedPaths, "bash.path_not_allowed")
}
