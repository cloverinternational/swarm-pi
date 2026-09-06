// Package main provides a command-line tool to run the websearch MCP server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/websearch"
)

func main() {
	socketPath := flag.String("socket", "/tmp/anthropic-web-search.sock", "Path to Unix socket for MCP server")
	flag.Parse()

	// Create web search tool
	tool := websearch.New()

	// Check if authentication is configured
	if !websearch.IsAuthConfigured() {
		fmt.Fprintln(os.Stderr, "ERROR: OAuth authentication is not configured")
		fmt.Fprintln(os.Stderr, "Please run 'claude login' to authenticate")
		os.Exit(1)
	}

	// Create MCP server (pass nil for logger, which will use no-op)
	server := websearch.NewMCPServer(tool, nil)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server
	if err := server.Start(ctx, *socketPath); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	fmt.Printf("Web Search MCP Server listening on %s\n", *socketPath)
	fmt.Println("Ready to accept connections from Claude Code and MCP clients")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan

	fmt.Println("\nShutting down server...")
	if err := server.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	}

	fmt.Println("Server stopped")
}
