package chat

import (
	"context"
	"os"
	"runtime"
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
)

// realCorpusEnv opts this benchmark in. It is off by default on purpose: it
// reads the developer's actual conversation store, which is private, multi-
// gigabyte, and machine-specific. A benchmark that touched it automatically
// would make `go test ./...` both slow and invasive.
const realCorpusEnv = "SWARM_BENCH_REAL_CORPUS"

// BenchmarkIndexedHistorySearchRealCorpus measures indexedHistorySearch against
// the real on-disk corpus.
//
// It exists because swarm-sdk/cmd/history-probe CANNOT measure this code. That
// probe builds its own searchFn closure over internal/conversation + storage and
// hands it to NewSearchToolWithOptions, so it exercises a re-implementation of
// the backend rather than indexedHistorySearch, and it structurally cannot call
// this function: the probe lives in the swarm-sdk module while this package
// lives in swarm-tui, which depends on the SDK and not the reverse. The only
// production caller of indexedHistorySearch is
// swarm-tui/internal/chat/sdk_integration.go.
//
// Consequently any before/after number taken from history-probe is measuring a
// different code path, and an unchanged result there says nothing about a change
// here. Run this instead:
//
//	SWARM_BENCH_REAL_CORPUS=1 /usr/bin/time -f 'wall=%es rss=%MkB cpu=%Us' \
//	  go test ./internal/chat/ -run '^$' \
//	  -bench BenchmarkIndexedHistorySearchRealCorpus -benchtime 1x -timeout 30m
//
// /usr/bin/time supplies peak RSS, which the testing package does not report and
// which is the figure that actually hurts here.
func BenchmarkIndexedHistorySearchRealCorpus(b *testing.B) {
	if os.Getenv(realCorpusEnv) != "1" {
		b.Skipf("set %s=1 to run against the real conversation corpus", realCorpusEnv)
	}

	corpus := paths.ConversationsDir()
	if _, err := os.Stat(corpus); err != nil {
		b.Skipf("conversation corpus unavailable at %s: %v", corpus, err)
	}

	ctx := context.Background()
	// No HOME override and no temp storage dir: the point is to hit the real
	// store and the real FTS index that lives beside it.
	client, err := sdkclient.New(
		sdkclient.WithoutAutoConfig(),
		sdkclient.WithProvider("anthropic", "claude-sonnet-4-5"),
		sdkclient.WithAPIKey("dummy"),
		sdkclient.WithStorageDir(corpus),
	)
	if err != nil {
		b.Fatalf("client.New: %v", err)
	}

	request := historytools.SearchRequest{
		Query:      "hook cache",
		SearchBody: true,
		Sort:       "relevance",
		Order:      "desc",
		Limit:      5,
		// An empty WorkspacePath is how the tool expresses scope="all": see
		// searchWorkspace in internal/tools/history/history.go, which returns an
		// empty workspace for that scope. There is no Scope field on the request.
		WorkspacePath: "",
	}

	var peakHeapSys uint64
	var lastResults int

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := indexedHistorySearch(ctx, client, request)
		if err != nil {
			b.Fatalf("search: %v", err)
		}
		lastResults = len(results)

		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if m.HeapSys > peakHeapSys {
			peakHeapSys = m.HeapSys
		}
	}
	b.StopTimer()

	// Reported so a run that returns nothing cannot be mistaken for a fast run.
	// Zero results with a fast time means the query missed, not that the index
	// worked.
	b.ReportMetric(float64(lastResults), "results")
	b.ReportMetric(float64(peakHeapSys)/(1<<20), "heapSysMB")
}
