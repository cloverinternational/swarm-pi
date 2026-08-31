// Package main provides the computer-use MCP server CLI.
// This is a stdio-based MCP server that provides computer control tools.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse/x11"
)

func main() {
	// Create context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Create X11 backend (auto-detects display)
	backend, err := x11.NewBackend(x11.DefaultOptions())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create X11 backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	// Create Linux executor
	executor, err := computeruse.NewLinuxExecutor(backend, computeruse.DefaultOptions())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create executor: %v\n", err)
		os.Exit(1)
	}

	// Create MCP server
	server, err := computeruse.NewServer(computeruse.ServerConfig{
		ServerName: "computer-use",
		Executor:   executor,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create server: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()

	// Run stdio MCP server
	if err := runStdioServer(ctx, server); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func runStdioServer(ctx context.Context, server *computeruse.Server) error {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for large messages
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("scanner error: %w", err)
				}
				return nil // EOF
			}

			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Parse JSON-RPC request
			var req struct {
				JSONRPC string         `json:"jsonrpc"`
				ID      any            `json:"id,omitempty"`
				Method  string         `json:"method"`
				Params  map[string]any `json:"params,omitempty"`
			}

			if err := json.Unmarshal(line, &req); err != nil {
				sendError(nil, -32700, fmt.Sprintf("Parse error: %v", err))
				continue
			}

			// Handle request
			response := handleRequest(ctx, server, req)

			// Send response (except for notifications)
			if req.ID != nil {
				if err := sendMessage(response); err != nil {
					return fmt.Errorf("failed to send response: %w", err)
				}
			}
		}
	}
}

func handleRequest(ctx context.Context, server *computeruse.Server, req struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}) map[string]any {
	switch req.Method {
	case "initialize":
		return handleInitialize(req.ID, server)
	case "tools/list":
		return handleToolsList(req.ID, server)
	case "tools/call":
		return handleToolsCall(ctx, req.ID, req.Params, server)
	case "ping":
		return makeResponse(req.ID, map[string]any{})
	default:
		return makeError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func handleInitialize(id any, server *computeruse.Server) map[string]any {
	return makeResponse(id, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false,
			},
		},
		"serverInfo": map[string]any{
			"name":    server.Name(),
			"version": "1.0.0",
		},
	})
}

func handleToolsList(id any, server *computeruse.Server) map[string]any {
	tools, err := server.ListTools()
	if err != nil {
		return makeError(id, -32603, fmt.Sprintf("Failed to list tools: %v", err))
	}
	return makeResponse(id, map[string]any{
		"tools": tools,
	})
}

func handleToolsCall(ctx context.Context, id any, params map[string]any, server *computeruse.Server) map[string]any {
	toolName, _ := params["name"].(string)
	toolParams, _ := params["arguments"].(map[string]any)

	if toolName == "" {
		return makeError(id, -32602, "Missing tool name")
	}

	result, err := server.HandleToolCall(ctx, toolName, toolParams)
	if err != nil {
		return makeResponse(id, map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": fmt.Sprintf("Error: %v", err),
				},
			},
			"isError": true,
		})
	}

	// Handle different result types
	content := formatResult(result)
	return makeResponse(id, map[string]any{
		"content": content,
	})
}

func formatResult(result any) []map[string]any {
	switch r := result.(type) {
	case map[string]any:
		// Check if it's a screenshot result with base64
		if base64, ok := r["base64"].(string); ok {
			return []map[string]any{
				{
					"type":     "image",
					"data":     base64,
					"mimeType": "image/jpeg",
				},
			}
		}
		// Default: convert to JSON text
		jsonBytes, _ := json.Marshal(r)
		return []map[string]any{
			{
				"type": "text",
				"text": string(jsonBytes),
			},
		}
	default:
		return []map[string]any{
			{
				"type": "text",
				"text": fmt.Sprintf("%v", r),
			},
		}
	}
}

func makeResponse(id any, result any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
}

func makeError(id any, code int, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
}

func sendError(id any, code int, message string) {
	resp := makeError(id, code, message)
	_ = sendMessage(resp)
}

func sendMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "%s\n", data)
	return err
}

// Ensure io.Writer is used
var _ io.Writer = os.Stdout
