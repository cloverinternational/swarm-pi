// Package deepwiki — EmbedderAgent
//
// EmbedderAgent is responsible for ONE thing: take a repository path, chunk
// every file into overlapping windows, embed each chunk with the configured
// vector backend, and return the populated chunk map.
//
// It knows nothing about AST, wiki generation, or the LLM text pipeline.
// It is safe to run concurrently alongside PipelineAgent.
package deepwiki

import (
	"context"
	"fmt"
	"sync"
)

// ---------------------------------------------------------------------------
// EmbedderAgent
// ---------------------------------------------------------------------------

// EmbedderAgent chunks a repository and builds a vector index.
// It is completely independent of PipelineAgent and CompilerAgent.
type EmbedderAgent struct {
	chunker  *Chunker
	embedder *Embedder
	// Options
	excludeDirs []string
	focusDirs   []string
}

// EmbedderResult is the output of EmbedderAgent.Run().
type EmbedderResult struct {
	// Chunks is the complete vector-indexed chunk map.
	// Keys are chunk IDs (same format as CodeGraph.Chunks).
	// Each DocumentChunk.Embedding may be nil if the backend is NoOp.
	Chunks map[string]*DocumentChunk

	// FileChunks maps file path → list of chunk IDs.
	FileChunks map[string][]string

	// TotalFiles is the number of files that were chunked.
	TotalFiles int

	// BackendName is the embedding backend that was used ("ollama/...", "openai/...", "none").
	BackendName string
}

// EmbedderAgentOption configures an EmbedderAgent.
type EmbedderAgentOption func(*EmbedderAgent)

// WithEmbedderExcludeDirs adds directories to skip during chunking.
// Additive: appends to the existing exclude list (defaultExcludeDirs) rather
// than replacing it.
func WithEmbedderExcludeDirs(dirs ...string) EmbedderAgentOption {
	return func(a *EmbedderAgent) { a.excludeDirs = append(a.excludeDirs, dirs...) }
}

// WithEmbedderFocusDirs limits chunking to specific directories.
func WithEmbedderFocusDirs(dirs ...string) EmbedderAgentOption {
	return func(a *EmbedderAgent) { a.focusDirs = dirs }
}

// WithEmbedderBackend sets a pre-built Embedder on the agent.
func WithEmbedderBackend(e *Embedder) EmbedderAgentOption {
	return func(a *EmbedderAgent) { a.embedder = e }
}

// NewEmbedderAgent creates a new EmbedderAgent.
// By default it auto-detects the best available embedding backend via NewEmbedder().
func NewEmbedderAgent(opts ...EmbedderAgentOption) *EmbedderAgent {
	a := &EmbedderAgent{
		chunker:     NewChunker(),
		embedder:    NewEmbedder(),
		excludeDirs: defaultExcludeDirs,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Run chunks every file in repoPath, embeds all chunks using the backend's
// native batch+worker-pool machinery, and returns the result.
//
// Two phases:
//
//  1. Chunking — every file is split into overlapping DocumentChunks and
//     registered in the result maps.  The progress callback fires once per
//     file so the caller can report "chunking [n/total]" in real time.
//
//  2. Batch embedding — all chunks collected in phase 1 are embedded in
//     parallel batches sized to the backend's preferred batch size
//     (ollamaBatchSize=128, oaiBatchSize=256).  Worker concurrency follows
//     backend.workers() (Ollama=1 serialised; OpenAI=4 parallel).
//     This path mirrors Embedder.EmbedGraph() and is ~70× faster than the
//     previous per-chunk sequential approach for local Ollama backends.
//
// progress is an optional callback invoked after each file is chunked:
//
//	progress(filesProcessed, totalFiles, currentFilePath)
//
// Pass nil to skip progress reporting.
func (a *EmbedderAgent) Run(
	ctx context.Context,
	repoPath string,
	progress func(done, total int, file string),
) (*EmbedderResult, error) {
	// We need a minimal analyzer just to walk the repo file list.
	walker := NewAnalyzer()
	files, err := walker.WalkRepo(repoPath, a.excludeDirs, a.focusDirs)
	if err != nil {
		return nil, fmt.Errorf("embedder agent: walk repo: %w", err)
	}

	result := &EmbedderResult{
		Chunks:      make(map[string]*DocumentChunk),
		FileChunks:  make(map[string][]string),
		TotalFiles:  len(files),
		BackendName: a.embedder.BackendName(),
	}

	// ── Phase 1: Chunk all files ──────────────────────────────────────────────
	// Collect (chunk, enrichedText) pairs for the batch embedding phase.
	type chunkWork struct {
		chunk    *DocumentChunk
		enriched string
	}
	var work []chunkWork

	for i, f := range files {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		chunks, chunkErr := a.chunker.ChunkFile(f)
		if chunkErr != nil {
			// Non-fatal: skip unreadable files — they'll be absent from the index.
			if progress != nil {
				progress(i+1, len(files), f.Path)
			}
			continue
		}

		for _, chunk := range chunks {
			enriched := a.embedder.enrichChunk(chunk)
			work = append(work, chunkWork{chunk: chunk, enriched: enriched})
			// Register immediately so keyword fallback works even if batch embedding fails.
			result.Chunks[chunk.ID] = chunk
			result.FileChunks[chunk.FilePath] = append(result.FileChunks[chunk.FilePath], chunk.ID)
		}

		if progress != nil {
			progress(i+1, len(files), f.Path)
		}
	}

	if len(work) == 0 {
		return result, nil
	}

	// ── Phase 2: Batch embed all chunks ───────────────────────────────────────
	// Uses the same batch+worker-pool pattern as Embedder.EmbedGraph(), giving
	// ~70× speedup over sequential embed() calls on local Ollama backends.
	bsz := a.embedder.backend.batchSize()
	maxWorkers := a.embedder.backend.workers()
	batchSem := make(chan struct{}, maxWorkers)

	type batch struct {
		items []chunkWork
	}
	var batches []batch
	for i := 0; i < len(work); i += bsz {
		end := min(i+bsz, len(work))
		batches = append(batches, batch{items: work[i:end]})
	}

	var bwg sync.WaitGroup
	for _, b := range batches {
		if ctx.Err() != nil {
			break
		}
		b := b
		texts := make([]string, len(b.items))
		for i, w := range b.items {
			texts[i] = w.enriched
		}

		bwg.Add(1)
		batchSem <- struct{}{} // acquire worker slot
		go func() {
			defer bwg.Done()
			defer func() { <-batchSem }()

			vecs, embErr := a.embedder.backend.embedBatch(ctx, texts)
			if embErr != nil {
				// Non-fatal: chunks without embeddings still support keyword search.
				return
			}
			for i, vec := range vecs {
				if i < len(b.items) && vec != nil {
					b.items[i].chunk.Embedding = vec
				}
			}
		}()
	}
	bwg.Wait()

	return result, nil
}
