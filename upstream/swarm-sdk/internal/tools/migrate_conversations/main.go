// migrate_conversations is a one-shot CLI that triggers the DirectoryFileStorage
// migration of legacy flat conversation files into the new workspace-partitioned
// layout. Safe to run multiple times (idempotent).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

func main() {
	convDir := paths.ConversationsDir()
	if len(os.Args) > 1 {
		convDir = os.Args[1]
	}

	fmt.Printf("Migrating conversations in: %s\n", convDir)

	// Instantiating DirectoryFileStorage triggers migrateFlatFiles() automatically.
	s, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{
		BaseDir: convDir,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialise storage: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	// Report final metrics.
	m, err := s.Metrics(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "metrics error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Migration complete.\n")
	fmt.Printf("  Total conversations in new layout: %d\n", m.TotalConversations)
	fmt.Printf("  Total size: %.1f MB\n", float64(m.TotalSizeBytes)/1024/1024)
}
