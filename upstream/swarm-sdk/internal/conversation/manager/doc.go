// Package manager provides conversation management and coordination.
//
// This package implements Ring 1 of the architecture, coordinating:
// - Storage backend persistence
// - Context window management and trimming strategies
// - Multi-format export/import (JSON, Markdown, HTML, JSONL)
// - Structured logging and distributed tracing
//
// The Manager is the primary entry point for conversation lifecycle management:
//
//	config := manager.Config{
//		Storage: storage.NewMemoryStorage(),
//		Logger: myLogger,
//		Tracer: myTracer,
//		MaxContextTokens: 100000,
//	}
//
//	mgr, err := manager.NewManager(config)
//	if err != nil {
//		// handle error
//	}
//
//	// Create a new conversation
//	conv, err := mgr.Create(ctx, manager.CreateOptions{
//		Mode: "planning",
//		Metadata: &conversation.ConversationMetadata{
//			UserID: "user123",
//		},
//	})
//
//	// Add messages
//	msg := &conversation.Message{
//		ID: "msg1",
//		Timestamp: time.Now(),
//		Role: conversation.RoleUser,
//		Content: "Hello",
//		Tokens: &conversation.TokenUsage{Total: 5},
//	}
//	err = mgr.AddMessage(ctx, conv.ID, msg)
//
//	// Export conversation
//	data, err := mgr.Export(ctx, conv.ID, manager.ExportFormatJSON)
//
// # Context Window Strategies
//
// The package provides three context trimming strategies:
//
// - LRUContextStrategy: Removes oldest messages first
// - SlidingWindowStrategy: Keeps only N most recent messages
// - PriorityContextStrategy: Keeps system and recent messages, trims older user messages
//
// # Export Formats
//
// Supported export formats:
// - JSON: Canonical format, complete conversation structure
// - Markdown: Human-readable markdown with formatted messages
// - HTML: Rich HTML with syntax highlighting
// - JSONL: Line-delimited JSON (one message per line)
package manager
