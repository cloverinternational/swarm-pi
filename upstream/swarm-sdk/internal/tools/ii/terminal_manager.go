// Terminal manager provides session management for persistent shell sessions.
// Ported from ii-agent's terminal_manager.py
//
// This package provides a tmux-based session manager that allows:
// - Creating and managing persistent shell sessions
// - Running commands with timeout support
// - Capturing session output (clean and ANSI)
// - Sending input to running processes
// - Killing commands and sessions
package ii

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Constants for terminal management
const (
	DefaultTimeout      = 60  // Default command timeout in seconds
	MaxTimeout          = 180 // Maximum allowed timeout in seconds
	PollInterval        = 500 * time.Millisecond
	DefaultPromptPrefix = "root@sandbox"
	SessionNamePrefix   = "II-AGENT-"
	TmuxDefaultWindowX  = 200 // Default window width
	TmuxDefaultWindowY  = 50  // Default window height
)

// PromptFormat is the custom prompt format for easy session state detection
var PromptFormat = fmt.Sprintf(`\[\033[01;32m\]%s\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ `, DefaultPromptPrefix)

// SessionState represents the current state of a shell session
type SessionState string

const (
	SessionStateBusy SessionState = "busy"
	SessionStateIdle SessionState = "idle"
)

// ShellResult contains both clean and ANSI-formatted output from a shell command
type ShellResult struct {
	// CleanOutput is the text without ANSI escape codes
	CleanOutput string
	// ANSIOutput is the text with ANSI escape codes preserved
	ANSIOutput string
}

// Shell error types

// ShellError is the base error type for shell operations
type ShellError struct {
	Message string
}

func (e *ShellError) Error() string {
	return e.Message
}

// ShellBusyError indicates the shell is busy executing a command
type ShellBusyError struct {
	ShellError
}

// ShellInvalidSessionNameError indicates an invalid session name was provided
type ShellInvalidSessionNameError struct {
	ShellError
}

// ShellSessionNotFoundError indicates the requested session was not found
type ShellSessionNotFoundError struct {
	ShellError
}

// ShellSessionExistsError indicates a session with the given name already exists
type ShellSessionExistsError struct {
	ShellError
}

// ShellRunDirNotFoundError indicates the specified run directory was not found
type ShellRunDirNotFoundError struct {
	ShellError
}

// ShellCommandTimeoutError indicates a command timed out
type ShellCommandTimeoutError struct {
	ShellError
}

// ShellOperationError indicates a general shell operation failure
type ShellOperationError struct {
	ShellError
}

// ShellManager defines the interface for managing shell sessions
type ShellManager interface {
	// GetAllSessions returns a list of all active session names
	AllSessions() []string

	// CreateSession creates a new shell session with the given name and working directory
	CreateSession(ctx context.Context, sessionName, startDirectory string, timeout int) error

	// DeleteSession terminates and removes the specified session
	DeleteSession(sessionName string) error

	// RunCommand executes a command in the specified session
	// If waitForOutput is true, waits for completion up to timeout seconds
	RunCommand(ctx context.Context, sessionName, command string, runDir string, timeout int, waitForOutput bool) (*ShellResult, error)

	// KillCurrentCommand sends SIGINT to stop the currently running command
	KillCurrentCommand(sessionName string, timeout int) (*ShellResult, error)

	// GetSessionState returns whether the session is busy or idle
	SessionState(sessionName string) (SessionState, error)

	// GetSessionOutput returns the current visible output of the session
	SessionOutput(sessionName string) (*ShellResult, error)

	// WriteToProcess sends input to the running process in the session
	WriteToProcess(sessionName, input string, pressEnter bool) (*ShellResult, error)
}

// TmuxSessionManager implements ShellManager using tmux sessions
type TmuxSessionManager struct {
	mu sync.RWMutex
	// Track sessions we've created to avoid querying tmux for unrelated sessions
	createdSessions map[string]bool
}

// NewTmuxSessionManager creates a new tmux-based session manager
func NewTmuxSessionManager() (*TmuxSessionManager, error) {
	// Verify tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, &ShellOperationError{ShellError{Message: "tmux is not installed or not in PATH"}}
	}

	return &TmuxSessionManager{
		createdSessions: make(map[string]bool),
	}, nil
}

// validateDirectory checks if a directory exists and is valid
func validateDirectory(directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", &ShellRunDirNotFoundError{ShellError{Message: "directory path cannot be empty"}}
	}

	absPath, err := filepath.Abs(directory)
	if err != nil {
		return "", &ShellRunDirNotFoundError{ShellError{Message: fmt.Sprintf("invalid directory path: %v", err)}}
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &ShellRunDirNotFoundError{ShellError{Message: fmt.Sprintf("directory does not exist: %s", directory)}}
		}
		return "", &ShellRunDirNotFoundError{ShellError{Message: fmt.Sprintf("cannot access directory: %v", err)}}
	}

	if !info.IsDir() {
		return "", &ShellRunDirNotFoundError{ShellError{Message: fmt.Sprintf("path is not a directory: %s", directory)}}
	}

	return absPath, nil
}

// validateSessionName checks if a session name is valid
func validateSessionName(name string) error {
	if name == "" {
		return &ShellInvalidSessionNameError{ShellError{Message: "session name cannot be empty"}}
	}

	// Allow only alphanumeric characters, hyphens, and underscores
	validName := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !validName.MatchString(name) {
		return &ShellInvalidSessionNameError{ShellError{Message: "invalid session name: only alphanumeric characters, hyphens, and underscores are allowed"}}
	}

	return nil
}

// runTmux executes a tmux command and returns stdout
func runTmux(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Check for specific tmux errors
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "no session") || strings.Contains(stderrStr, "session not found") {
			return "", &ShellSessionNotFoundError{ShellError{Message: fmt.Sprintf("session not found: %s", stderrStr)}}
		}
		if strings.Contains(stderrStr, "duplicate session") {
			return "", &ShellSessionExistsError{ShellError{Message: fmt.Sprintf("session already exists: %s", stderrStr)}}
		}
		return "", &ShellOperationError{ShellError{Message: fmt.Sprintf("tmux command failed: %v - %s", err, stderrStr)}}
	}

	return stdout.String(), nil
}

// GetAllSessions returns all sessions created by this manager
func (m *TmuxSessionManager) AllSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]string, 0, len(m.createdSessions))
	for name := range m.createdSessions {
		// Verify session still exists in tmux
		_, err := runTmux("has-session", "-t", name)
		if err == nil {
			sessions = append(sessions, name)
		}
	}
	return sessions
}

// CreateSession creates a new tmux session
func (m *TmuxSessionManager) CreateSession(ctx context.Context, sessionName, startDirectory string, timeout int) error {
	if err := validateSessionName(sessionName); err != nil {
		return err
	}

	absDir, err := validateDirectory(startDirectory)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if session already exists
	if m.createdSessions[sessionName] {
		_, err := runTmux("has-session", "-t", sessionName)
		if err == nil {
			return &ShellSessionExistsError{ShellError{Message: fmt.Sprintf("session '%s' already exists", sessionName)}}
		}
		// Session was tracked but no longer exists, clean it up
		delete(m.createdSessions, sessionName)
	}

	// Create new tmux session with specified dimensions
	_, err = runTmux(
		"new-session",
		"-d",              // detached
		"-s", sessionName, // session name
		"-c", absDir, // start directory
		"-x", fmt.Sprintf("%d", TmuxDefaultWindowX), // width
		"-y", fmt.Sprintf("%d", TmuxDefaultWindowY), // height
	)
	if err != nil {
		return err
	}

	m.createdSessions[sessionName] = true

	// Configure the session with custom prompt
	promptCmd := fmt.Sprintf("export PS1='%s'; clear", PromptFormat)
	_, err = runTmux("send-keys", "-t", sessionName, promptCmd, "Enter")
	if err != nil {
		// Cleanup on failure
		m.DeleteSession(sessionName)
		return &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to configure session: %v", err)}}
	}

	// Set TERM for color support
	_, err = runTmux("send-keys", "-t", sessionName, "export TERM='xterm-256color'", "Enter")
	if err != nil {
		m.DeleteSession(sessionName)
		return &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to set TERM: %v", err)}}
	}

	// Wait for session to be idle
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return m.waitForIdle(ctx, sessionName, timeout)
}

// DeleteSession removes a tmux session
func (m *TmuxSessionManager) DeleteSession(sessionName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, err := runTmux("kill-session", "-t", sessionName)
	delete(m.createdSessions, sessionName)

	if err != nil {
		// Ignore "session not found" errors when deleting
		if _, ok := err.(*ShellSessionNotFoundError); ok {
			return nil
		}
		return err
	}
	return nil
}

// GetSessionOutput captures the current session output
func (m *TmuxSessionManager) SessionOutput(sessionName string) (*ShellResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Capture with ANSI codes
	ansiOutput, err := runTmux("capture-pane", "-t", sessionName, "-e", "-p", "-S", "-")
	if err != nil {
		return nil, err
	}

	// Capture without ANSI codes
	cleanOutput, err := runTmux("capture-pane", "-t", sessionName, "-p", "-S", "-")
	if err != nil {
		return nil, err
	}

	return &ShellResult{
		CleanOutput: strings.TrimRight(cleanOutput, "\n"),
		ANSIOutput:  strings.TrimRight(ansiOutput, "\n"),
	}, nil
}

// GetSessionState determines if the session is busy or idle
func (m *TmuxSessionManager) SessionState(sessionName string) (SessionState, error) {
	result, err := m.SessionOutput(sessionName)
	if err != nil {
		return SessionStateIdle, err
	}

	lines := strings.Split(result.CleanOutput, "\n")
	if len(lines) == 0 {
		return SessionStateIdle, nil
	}

	lastLine := lines[len(lines)-1]

	// Check if the prompt is visible (session is idle)
	if strings.Contains(lastLine, DefaultPromptPrefix) &&
		(strings.HasSuffix(lastLine, "$") || strings.HasSuffix(lastLine, "#")) {
		return SessionStateIdle, nil
	}

	return SessionStateBusy, nil
}

// RunCommand executes a command in the specified session
func (m *TmuxSessionManager) RunCommand(ctx context.Context, sessionName, command string, runDir string, timeout int, waitForOutput bool) (*ShellResult, error) {
	// Validate session exists
	m.mu.RLock()
	if !m.createdSessions[sessionName] {
		m.mu.RUnlock()
		return nil, &ShellSessionNotFoundError{ShellError{Message: fmt.Sprintf("session '%s' not found", sessionName)}}
	}
	m.mu.RUnlock()

	// Check if session is busy
	state, err := m.SessionState(sessionName)
	if err != nil {
		return nil, err
	}
	if state == SessionStateBusy {
		return nil, &ShellBusyError{ShellError{Message: "session is busy, the last command is not finished"}}
	}

	// Change directory if specified
	if runDir != "" {
		absDir, err := validateDirectory(runDir)
		if err != nil {
			return nil, err
		}
		// Quote the directory path for safety
		cdCmd := fmt.Sprintf("cd %q", absDir)
		_, err = runTmux("send-keys", "-t", sessionName, cdCmd, "Enter")
		if err != nil {
			return nil, &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to change directory: %v", err)}}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Clear the screen before running command
	_, err = runTmux("send-keys", "-t", sessionName, "clear", "Enter")
	if err != nil {
		return nil, &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to clear screen: %v", err)}}
	}
	time.Sleep(100 * time.Millisecond)

	// Send the command
	_, err = runTmux("send-keys", "-t", sessionName, command, "Enter")
	if err != nil {
		return nil, &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to send command: %v", err)}}
	}
	time.Sleep(100 * time.Millisecond)

	// Wait for output if requested
	if waitForOutput {
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		if err := m.waitForIdle(ctx, sessionName, timeout); err != nil {
			return nil, err
		}
	}

	return m.SessionOutput(sessionName)
}

// KillCurrentCommand sends SIGINT to stop the current command
func (m *TmuxSessionManager) KillCurrentCommand(sessionName string, timeout int) (*ShellResult, error) {
	m.mu.RLock()
	if !m.createdSessions[sessionName] {
		m.mu.RUnlock()
		return nil, &ShellSessionNotFoundError{ShellError{Message: fmt.Sprintf("session '%s' not found", sessionName)}}
	}
	m.mu.RUnlock()

	// Send Ctrl+C
	_, err := runTmux("send-keys", "-t", sessionName, "C-c", "")
	if err != nil {
		return nil, &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to send interrupt: %v", err)}}
	}

	// Wait for session to be idle
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx := context.Background()
	if err := m.waitForIdle(ctx, sessionName, timeout); err != nil {
		// Return output even if wait failed
		result, _ := m.SessionOutput(sessionName)
		return result, err
	}

	return m.SessionOutput(sessionName)
}

// WriteToProcess sends input to the running process
func (m *TmuxSessionManager) WriteToProcess(sessionName, input string, pressEnter bool) (*ShellResult, error) {
	m.mu.RLock()
	if !m.createdSessions[sessionName] {
		m.mu.RUnlock()
		return nil, &ShellSessionNotFoundError{ShellError{Message: fmt.Sprintf("session '%s' not found", sessionName)}}
	}
	m.mu.RUnlock()

	// Send the input
	if pressEnter {
		_, err := runTmux("send-keys", "-t", sessionName, input, "Enter")
		if err != nil {
			return nil, &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to send input: %v", err)}}
		}
	} else {
		_, err := runTmux("send-keys", "-t", sessionName, input, "")
		if err != nil {
			return nil, &ShellOperationError{ShellError{Message: fmt.Sprintf("failed to send input: %v", err)}}
		}
	}

	time.Sleep(100 * time.Millisecond)
	return m.SessionOutput(sessionName)
}

// waitForIdle waits for the session to become idle
func (m *TmuxSessionManager) waitForIdle(ctx context.Context, sessionName string, timeout int) error {
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return &ShellCommandTimeoutError{ShellError{Message: "context cancelled while waiting for session"}}
		default:
		}

		state, err := m.SessionState(sessionName)
		if err != nil {
			return err
		}

		if state == SessionStateIdle {
			return nil
		}

		time.Sleep(PollInterval)
	}

	return &ShellCommandTimeoutError{ShellError{Message: "command timed out waiting for session to become idle"}}
}

// Cleanup terminates all sessions managed by this manager
func (m *TmuxSessionManager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name := range m.createdSessions {
		runTmux("kill-session", "-t", name)
	}
	m.createdSessions = make(map[string]bool)
}
