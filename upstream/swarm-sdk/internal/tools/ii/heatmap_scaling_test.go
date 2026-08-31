package ii

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReadTailScaling answers a specific question raised by the heatmap:
// does Read(tail=20) cost scale with FILE size or with the number of lines
// actually requested?
//
// If cost scales with file size, Read is loading the whole file to return 20
// lines -- the same failure shape as the io.ReadAll in the conversation
// metadata loader.
func TestReadTailScaling(t *testing.T) {
	if os.Getenv("SWARM_HEATMAP") != "1" {
		t.Skip("set SWARM_HEATMAP=1")
	}
	root := t.TempDir()
	reg := buildRegistry(t, root)
	ctx := context.Background()

	sizes := []int{1, 4, 16, 64} // MB
	fmt.Println()
	fmt.Printf("%-10s %12s %14s %14s %14s\n", "FILE_MB", "TAIL_20_MS", "HEAD_50_MS", "RANGE100_MS", "FULL_READ_MS")
	for _, mb := range sizes {
		path := filepath.Join(root, fmt.Sprintf("f%dmb.txt", mb))
		var b strings.Builder
		target := mb << 20
		for i := 0; b.Len() < target; i++ {
			fmt.Fprintf(&b, "%08d the quick brown fox jumps over the lazy dog padding padding\n", i)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		cap := 256 << 20

		timeIt := func(params map[string]any) time.Duration {
			params["file_path"] = path
			params["max_size"] = cap
			// warm
			if _, err := reg.Execute(ctx, "Read", params); err != nil {
				t.Fatalf("read: %v", err)
			}
			const n = 5
			start := time.Now()
			for i := 0; i < n; i++ {
				if _, err := reg.Execute(ctx, "Read", params); err != nil {
					t.Fatalf("read: %v", err)
				}
			}
			return time.Since(start) / n
		}

		tail := timeIt(map[string]any{"tail": 20})
		head := timeIt(map[string]any{"head": 50})
		rng := timeIt(map[string]any{"start_line": 100, "end_line": 200})
		full := timeIt(map[string]any{})

		fmt.Printf("%-10d %12.2f %14.2f %14.2f %14.2f\n",
			mb,
			float64(tail.Microseconds())/1000,
			float64(head.Microseconds())/1000,
			float64(rng.Microseconds())/1000,
			float64(full.Microseconds())/1000)
	}
	fmt.Println()
}
