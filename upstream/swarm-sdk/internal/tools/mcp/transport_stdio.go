package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// StdioTransport implements stdio-based MCP communication
type StdioTransport struct {
	config  *TransportConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	scanner *bufio.Scanner
	logger  observability.Logger

	mu         sync.Mutex
	connected  bool
	lastStderr []string
}

// NewStdioTransport creates a new stdio transport
func NewStdioTransport(config *TransportConfig, logger observability.Logger) *StdioTransport {
	return &StdioTransport{
		config:     config,
		logger:     logger,
		lastStderr: make([]string, 0, 50),
	}
}

func (t *StdioTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		return fmt.Errorf("already connected")
	}

	// Create command
	t.cmd = exec.CommandContext(ctx, t.config.Command, t.config.Args...)

	// Set working directory if specified
	if t.config.WorkingDir != "" {
		t.cmd.Dir = t.config.WorkingDir
	}

	// Set environment - start with parent environment
	t.cmd.Env = os.Environ()
	t.cmd.Env = append(t.cmd.Env, "MCP_TRANSPORT=stdio")
	for k, v := range t.config.Env {
		t.cmd.Env = append(t.cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Setup pipes
	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start process
	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	// Setup scanner for newline-delimited JSON
	t.scanner = bufio.NewScanner(t.stdout)
	// Increase buffer size for large messages
	buf := make([]byte, 0, 64*1024)
	t.scanner.Buffer(buf, 1024*1024) // 1MB max

	// Start stderr logger
	go t.logStderr()

	t.connected = true

	t.logger.Info(ctx, "MCP stdio transport connected",
		observability.F("command", t.config.Command),
		observability.F("pid", t.cmd.Process.Pid))

	return nil
}

func (t *StdioTransport) Send(ctx context.Context, message any) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return fmt.Errorf("not connected")
	}

	// Marshal to JSON
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Write with newline delimiter (MCP spec requirement)
	if _, err := t.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	t.logger.Debug(ctx, "Sent MCP message",
		observability.F("type", fmt.Sprintf("%T", message)),
		observability.F("size", len(data)))

	return nil
}

func (t *StdioTransport) Receive(ctx context.Context) (any, error) {
	// Scan next line
	if !t.scanner.Scan() {
		if err := t.scanner.Err(); err != nil {
			t.logger.Error(ctx, "Scanner error during receive",
				observability.F("error", err.Error()))
			return nil, fmt.Errorf("scanner error: %w", err)
		}
		t.logger.Warn(ctx, "EOF while receiving - process may have exited")

		// Capture stderr context
		t.mu.Lock()
		stderrContext := ""
		if len(t.lastStderr) > 0 {
			stderrContext = "\nStderr output:\n" + strings.Join(t.lastStderr, "\n")
		}
		t.mu.Unlock()

		if stderrContext != "" {
			return nil, fmt.Errorf("initialization failed: EOF%s", stderrContext)
		}
		return nil, io.EOF
	}

	data := t.scanner.Bytes()

	t.logger.Debug(ctx, "Received raw data",
		observability.F("size", len(data)),
		observability.F("preview", string(data[:min(100, len(data))])))

	// Try to determine message type
	var peek struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Method  string `json:"method"`
		Result  any    `json:"result"`
		Error   any    `json:"error"`
	}

	if err := json.Unmarshal(data, &peek); err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	// Determine type and unmarshal accordingly
	if peek.Method != "" && peek.ID != nil {
		// Request
		var req JSONRPCRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		t.logger.Debug(ctx, "Received MCP request",
			observability.F("method", req.Method),
			observability.F("id", req.ID))
		return &req, nil
	} else if peek.Method != "" {
		// Notification
		var notif JSONRPCNotification
		if err := json.Unmarshal(data, &notif); err != nil {
			return nil, err
		}
		t.logger.Debug(ctx, "Received MCP notification",
			observability.F("method", notif.Method))
		return &notif, nil
	} else {
		// Response
		var resp JSONRPCResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		t.logger.Debug(ctx, "Received MCP response",
			observability.F("id", resp.ID))
		return &resp, nil
	}
}

func (t *StdioTransport) logStderr() {
	ctx := context.Background()
	scanner := bufio.NewScanner(t.stderr)
	// Increase buffer size to handle large stderr output and avoid "token too long" errors
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max
	for scanner.Scan() {
		line := scanner.Text()

		t.mu.Lock()
		if len(t.lastStderr) >= 50 {
			// Shift and append (circular buffer-ish)
			t.lastStderr = t.lastStderr[1:]
		}
		t.lastStderr = append(t.lastStderr, line)
		t.mu.Unlock()

		t.logger.Info(ctx, "MCP server stderr",
			observability.F("message", line))
	}
	if err := scanner.Err(); err != nil {
		t.logger.Error(ctx, "stderr scanner error",
			observability.F("error", err.Error()))
	}
	t.logger.Debug(ctx, "stderr logger finished")
}

func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return nil
	}

	t.connected = false

	// Close stdin to signal shutdown
	if t.stdin != nil {
		t.stdin.Close()
	}

	// Kill process if still running
	if t.cmd != nil && t.cmd.Process != nil {
		if err := t.cmd.Process.Kill(); err != nil {
			ctx := context.Background()
			t.logger.Warn(ctx, "failed to kill MCP server",
				observability.F("error", err.Error()))
		}
		t.cmd.Wait() // Clean up zombie
	}

	ctx := context.Background()
	t.logger.Info(ctx, "MCP stdio transport closed")

	return nil
}

func (t *StdioTransport) IsConnected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connected
}

func (t *StdioTransport) TransportType() TransportType {
	return TransportStdio
}
