package chat

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// BashExecutor handles bash command execution
type BashExecutor struct {
	workDir string
	timeout time.Duration
}

// NewBashExecutor creates a new bash executor
func NewBashExecutor(workDir string) *BashExecutor {
	return &BashExecutor{
		workDir: workDir,
		timeout: 30 * time.Second,
	}
}

// Execute runs a bash command and returns the result.
// Output is written to temp files to avoid buffer issues and enable recovery.
func (b *BashExecutor) Execute(ctx context.Context, command string) (*BashResult, error) {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	// Create temp files for stdout and stderr
	// Use os.TempDir() for cross-platform compatibility
	stdoutFile, err := os.CreateTemp(os.TempDir(), "bash-exec-stdout-*.txt")
	if err != nil {
		return nil, err
	}
	stdoutPath := stdoutFile.Name()
	defer os.Remove(stdoutPath)

	stderrFile, err := os.CreateTemp(os.TempDir(), "bash-exec-stderr-*.txt")
	if err != nil {
		stdoutFile.Close()
		os.Remove(stdoutPath)
		return nil, err
	}
	stderrPath := stderrFile.Name()
	defer os.Remove(stderrPath)

	// Create the command
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = b.workDir
	cmd.WaitDelay = 2 * time.Second

	// Set non-interactive environment markers to prevent interactive prompts
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"TERM=dumb",                      // Simple terminal type
		"DEBIAN_FRONTEND=noninteractive", // Disable apt prompts
		"CI=true",                        // Many tools respect this
	)

	// Redirect output to temp files
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	// Note: cmd.Stdin is nil by default, which Go connects to the null device

	// Run the command with duration tracking
	startTime := time.Now()
	cmdErr := cmd.Run()
	duration := time.Since(startTime)

	// Close files before reading
	stdoutFile.Close()
	stderrFile.Close()

	// Read output from temp files
	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stderrBytes, _ := os.ReadFile(stderrPath)

	// Prepare result
	result := &BashResult{
		Command:    command,
		Output:     string(stdoutBytes),
		Error:      string(stderrBytes),
		DurationMs: duration.Milliseconds(),
	}

	// Get exit code
	if exitError, ok := cmdErr.(*exec.ExitError); ok {
		result.ExitCode = exitError.ExitCode()
	} else if cmdErr != nil {
		return nil, cmdErr
	}

	// Populate metadata for renderer
	result.Metadata = map[string]any{
		"exit_code":   result.ExitCode,
		"duration_ms": result.DurationMs,
	}

	return result, nil
}
