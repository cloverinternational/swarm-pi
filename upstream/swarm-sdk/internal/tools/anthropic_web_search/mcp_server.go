// Package anthropic_web_search provides MCP server integration for the web search tool
// This allows the web search tool to be exposed as an MCP (Model Context Protocol) server
// that can be called by Claude Code or other MCP clients.
package anthropic_web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// MCPServer wraps the web search tool as an MCP-compatible server
// This allows the tool to be called through the Model Context Protocol
type MCPServer struct {
	tool     *Tool
	listener net.Listener
	logger   observability.Logger
	mu       sync.RWMutex
	running  bool
}

// NewMCPServer creates a new MCP server wrapper for the web search tool
func NewMCPServer(tool *Tool, logger observability.Logger) *MCPServer {
	if logger == nil {
		logger = &noopLogger{}
	}
	return &MCPServer{
		tool:   tool,
		logger: logger,
	}
}

// noopLogger is a no-op implementation of observability.Logger
type noopLogger struct{}

func (l *noopLogger) Log(ctx context.Context, level observability.Level, event string, fields ...observability.Field) {
}
func (l *noopLogger) Trace(ctx context.Context, event string, fields ...observability.Field) {}
func (l *noopLogger) Debug(ctx context.Context, event string, fields ...observability.Field) {}
func (l *noopLogger) Info(ctx context.Context, event string, fields ...observability.Field)  {}
func (l *noopLogger) Warn(ctx context.Context, event string, fields ...observability.Field)  {}
func (l *noopLogger) Error(ctx context.Context, event string, fields ...observability.Field) {}
func (l *noopLogger) Fatal(ctx context.Context, event string, fields ...observability.Field) {}
func (l *noopLogger) WithFields(fields ...observability.Field) observability.Logger          { return l }
func (l *noopLogger) SetLevel(level observability.Level)                                     {}

// Start starts the MCP server listening on a Unix socket
func (s *MCPServer) Start(ctx context.Context, socketPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server is already running")
	}

	// Remove existing socket file
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	// Listen on Unix socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	s.listener = listener
	s.running = true

	// Accept connections in background
	go s.acceptConnections(ctx)

	return nil
}

// Stop stops the MCP server
func (s *MCPServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.running = false
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// acceptConnections accepts and handles incoming MCP connections
func (s *MCPServer) acceptConnections(ctx context.Context) {
	for {
		s.mu.RLock()
		if !s.running {
			s.mu.RUnlock()
			return
		}
		s.mu.RUnlock()

		conn, err := s.listener.Accept()
		if err != nil {
			// Check if server was stopped
			select {
			case <-ctx.Done():
				return
			default:
				s.logger.Error(ctx, "failed to accept connection", observability.F("error", err.Error()))
				continue
			}
		}

		// Handle connection in goroutine
		go s.handleConnection(ctx, conn)
	}
}

// handleConnection handles a single MCP client connection
func (s *MCPServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var msg json.RawMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				return
			}
			s.logger.Error(ctx, "failed to decode message", observability.F("error", err.Error()))
			return
		}

		response := s.handleMessage(ctx, msg)
		if err := encoder.Encode(response); err != nil {
			s.logger.Error(ctx, "failed to encode response", observability.F("error", err.Error()))
			return
		}
	}
}

// MCPMessage represents an MCP protocol message
type MCPMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

// handleMessage handles an incoming MCP message
func (s *MCPServer) handleMessage(ctx context.Context, rawMsg json.RawMessage) any {
	var msg MCPMessage
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		return map[string]any{
			"jsonrpc": "2.0",
			"error": map[string]any{
				"code":    -32700,
				"message": "Parse error",
			},
		}
	}

	switch msg.Method {
	case "initialize":
		return s.handleInitialize(msg.ID)
	case "tools/list":
		return s.handleListTools(msg.ID)
	case "tools/call":
		return s.handleCallTool(ctx, msg.ID, msg.Params)
	default:
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "Method not found",
			},
		}
	}
}

// handleInitialize handles the MCP initialization request
func (s *MCPServer) handleInitialize(id any) any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "anthropic-web-search",
				"version": "1.0.0",
			},
		},
	}
}

// handleListTools handles the MCP tools/list request
func (s *MCPServer) handleListTools(id any) any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"tools": []map[string]any{
				{
					"name":        "web_search",
					"description": s.tool.Description(),
					"inputSchema": s.tool.Parameters(),
				},
			},
		},
	}
}

// handleCallTool handles the MCP tools/call request
func (s *MCPServer) handleCallTool(ctx context.Context, id any, params json.RawMessage) any {
	var callParams struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}

	if err := json.Unmarshal(params, &callParams); err != nil {
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]any{
				"code":    -32602,
				"message": "Invalid params",
				"data":    err.Error(),
			},
		}
	}

	if callParams.Name != "web_search" {
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]any{
				"code":    -32603,
				"message": "Unknown tool",
			},
		}
	}

	// Add timeout to context
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Execute the tool
	result, err := s.tool.Execute(ctx, callParams.Arguments)
	if err != nil {
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]any{
				"code":    -32603,
				"message": "Tool execution failed",
				"data":    err.Error(),
			},
		}
	}

	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": result.Output,
				},
			},
		},
	}
}
