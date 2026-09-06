// Package deepwiki — PipelineAgent
//
// PipelineAgent is responsible for ONE thing: walk a repository's AST, extract
// all semantic code entities (functions, types, methods, structs, interfaces,
// constants, variables), build the call/dependency graph, and return the result.
//
// It knows nothing about embeddings, vector search, or the LLM text pipeline.
// It is safe to run concurrently alongside EmbedderAgent.
package deepwiki

import (
	"context"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// PipelineAgent
// ---------------------------------------------------------------------------

// PipelineAgent performs code-aware AST parsing and structural analysis on a
// repository. It is completely independent of EmbedderAgent and CompilerAgent.
type PipelineAgent struct {
	analyzer    *Analyzer
	excludeDirs []string
	focusDirs   []string
}

// PipelineResult is the output of PipelineAgent.Run().
type PipelineResult struct {
	// Entities contains all parsed code entities, keyed by qualified name.
	Entities map[string]*CodeEntity

	// Edges contains all directed relationships between entities.
	Edges []CodeEdge

	// Packages maps package name → list of entity qualified names.
	Packages map[string][]string

	// FileEntities maps file path → list of entity qualified names.
	FileEntities map[string][]string

	// TotalFiles is the number of files visited.
	TotalFiles int

	// Language is the primary language detected in the repository.
	Language string

	// AnalyzedAt is when the analysis completed.
	AnalyzedAt time.Time
}

// PipelineAgentOption configures a PipelineAgent.
type PipelineAgentOption func(*PipelineAgent)

// WithPipelineExcludeDirs adds directories to skip during analysis.
// Additive: appends to the existing exclude list (defaultExcludeDirs) rather
// than replacing it.
func WithPipelineExcludeDirs(dirs ...string) PipelineAgentOption {
	return func(a *PipelineAgent) { a.excludeDirs = append(a.excludeDirs, dirs...) }
}

// WithPipelineFocusDirs limits analysis to specific directories.
func WithPipelineFocusDirs(dirs ...string) PipelineAgentOption {
	return func(a *PipelineAgent) { a.focusDirs = dirs }
}

// NewPipelineAgent creates a new PipelineAgent.
func NewPipelineAgent(opts ...PipelineAgentOption) *PipelineAgent {
	a := &PipelineAgent{
		analyzer:    NewAnalyzer(),
		excludeDirs: defaultExcludeDirs,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Run walks the repository AST, extracts entities and edges, and returns the
// structured PipelineResult.
//
// progress is an optional callback invoked after each file is parsed:
//
//	progress(filesProcessed, totalFiles, currentFilePath)
//
// Pass nil to skip progress reporting.
func (a *PipelineAgent) Run(
	ctx context.Context,
	repoPath string,
	progress func(done, total int, file string),
) (*PipelineResult, error) {
	files, err := a.analyzer.WalkRepo(repoPath, a.excludeDirs, a.focusDirs)
	if err != nil {
		return nil, fmt.Errorf("pipeline agent: walk repo: %w", err)
	}

	result := &PipelineResult{
		Entities:     make(map[string]*CodeEntity),
		Edges:        nil,
		Packages:     make(map[string][]string),
		FileEntities: make(map[string][]string),
		TotalFiles:   len(files),
	}

	for i, f := range files {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		entities, edges, err := a.analyzer.AnalyzeFile(f)
		if err != nil {
			// Non-fatal: some files may be unparseable — skip them.
			continue
		}

		for _, ent := range entities {
			result.Entities[ent.QualifiedName] = ent
			result.FileEntities[ent.FilePath] = append(result.FileEntities[ent.FilePath], ent.QualifiedName)
			if ent.Package != "" {
				result.Packages[ent.Package] = append(result.Packages[ent.Package], ent.QualifiedName)
			}
		}
		for _, edge := range edges {
			result.Edges = append(result.Edges, edge)
		}

		if progress != nil {
			progress(i+1, len(files), f.Path)
		}
	}

	result.Language = a.analyzer.DetectPrimaryLanguage(files)
	result.AnalyzedAt = time.Now()

	return result, nil
}
