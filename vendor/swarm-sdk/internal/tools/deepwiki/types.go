// Package deepwiki provides a dual-mode code intelligence engine for local
// wiki generation.
//
// Two complementary analysis approaches run together:
//
//  1. Code-aware analysis — Parses code into semantic units (functions, types,
//     interfaces, methods), extracts call graphs and dependency edges, embeds
//     enriched representations that capture what code IS and what it RELATES to.
//
//  2. Document chunking — Splits every file (code, docs, configs) into
//     overlapping chunks with metadata, giving broad coverage of content the
//     AST parser can't reach (comments, READMEs, YAML, prose).
//
// Both feed into a unified knowledge graph. The wiki generator queries both
// layers — structural code queries hit the entity graph, broad content queries
// hit the document chunks, and results are fused with reciprocal-rank fusion.
//
// Architecture:
//
//	┌─────────┐     ┌──────────┐
//	│ Analyzer │────▶│CodeGraph │──┐
//	│ (AST)    │     │(entities)│  │
//	└─────────┘     └──────────┘  │    ┌──────────┐    ┌───────────┐
//	                               ├───▶│ Embedder │───▶│ Generator │──▶ Wiki
//	┌─────────┐     ┌──────────┐  │    │(enriched)│    │ (LLM)     │
//	│ Chunker │────▶│DocChunks │──┘    └──────────┘    └───────────┘
//	│ (files) │     │(coverage)│
//	└─────────┘     └──────────┘
package deepwiki

import "time"

// ---------------------------------------------------------------------------
// Code analysis types — deep structural understanding
// ---------------------------------------------------------------------------

// EntityKind classifies a parsed code element.
type EntityKind string

const (
	KindPackage   EntityKind = "package"
	KindFile      EntityKind = "file"
	KindImport    EntityKind = "import"
	KindFunction  EntityKind = "function"
	KindMethod    EntityKind = "method"
	KindType      EntityKind = "type"
	KindStruct    EntityKind = "struct"
	KindInterface EntityKind = "interface"
	KindConst     EntityKind = "const"
	KindVar       EntityKind = "var"
	KindClass     EntityKind = "class"    // Python/TS/JS
	KindModule    EntityKind = "module"   // Python/TS/JS
	KindEndpoint  EntityKind = "endpoint" // HTTP handlers, routes
)

// CodeEntity is a parsed semantic unit of code.
type CodeEntity struct {
	// Identity
	QualifiedName string     `json:"qualified_name"` // e.g. "package.TypeName.MethodName"
	Kind          EntityKind `json:"kind"`
	Name          string     `json:"name"`
	Package       string     `json:"package,omitempty"`
	FilePath      string     `json:"file_path"`

	// Source location
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`

	// Content
	Signature  string `json:"signature,omitempty"`   // func sig, type def header
	DocComment string `json:"doc_comment,omitempty"` // godoc, docstring, jsdoc
	Body       string `json:"body"`                  // full source text

	// Relationships (populated during graph building)
	Receiver   string   `json:"receiver,omitempty"`   // method receiver type
	Imports    []string `json:"imports,omitempty"`    // packages/modules imported
	Calls      []string `json:"calls,omitempty"`      // functions/methods called
	References []string `json:"references,omitempty"` // types/vars referenced
	Implements []string `json:"implements,omitempty"` // interfaces implemented
}

// EdgeKind classifies a relationship between code entities.
type EdgeKind string

const (
	EdgeCalls      EdgeKind = "calls"
	EdgeImports    EdgeKind = "imports"
	EdgeContains   EdgeKind = "contains"   // package→file, file→func
	EdgeImplements EdgeKind = "implements" // struct→interface
	EdgeReferences EdgeKind = "references" // uses a type/const/var
	EdgeEmbeds     EdgeKind = "embeds"     // struct embedding
	EdgeReturns    EdgeKind = "returns"    // function→return type
	EdgeReceives   EdgeKind = "receives"   // method→receiver type
)

// CodeEdge is a directed relationship between two entities.
type CodeEdge struct {
	From     string   `json:"from"` // qualified name
	To       string   `json:"to"`
	Kind     EdgeKind `json:"kind"`
	FilePath string   `json:"file_path,omitempty"`
}

// ---------------------------------------------------------------------------
// Document chunk types — broad file coverage
// ---------------------------------------------------------------------------

// ChunkKind classifies a document chunk.
type ChunkKind string

const (
	ChunkCode   ChunkKind = "code"   // source code chunk
	ChunkDoc    ChunkKind = "doc"    // documentation (md, rst, txt)
	ChunkConfig ChunkKind = "config" // configuration (yaml, json, toml)
	ChunkData   ChunkKind = "data"   // other structured data
)

// DocumentChunk is a piece of a file with metadata.
// This is the "broad coverage" layer — every file gets chunked regardless of
// whether the AST parser understands the language.
type DocumentChunk struct {
	ID       string    `json:"id"`
	FilePath string    `json:"file_path"`
	Kind     ChunkKind `json:"chunk_kind"`
	Language string    `json:"language"` // file language (go, python, markdown, etc.)

	// Content
	Content    string `json:"content"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	ChunkIndex int    `json:"chunk_index"` // position within the file

	// Metadata
	FileSize   int64 `json:"file_size"`
	IsCode     bool  `json:"is_code"`
	TokenCount int   `json:"token_count,omitempty"`

	// Embedding vector
	Embedding []float32 `json:"embedding,omitempty"`
}

// ---------------------------------------------------------------------------
// Unified knowledge graph — combines both approaches
// ---------------------------------------------------------------------------

// CodeGraph holds the complete understanding of a repository.
// It contains BOTH the structural code graph AND the document chunks.
type CodeGraph struct {
	// Root info
	RepoPath string `json:"repo_path"`
	RepoName string `json:"repo_name"`
	Language string `json:"language,omitempty"` // primary language detected

	// --- Code-aware layer ---

	// Parsed entities keyed by qualified name
	Entities map[string]*CodeEntity `json:"entities"`
	// Structural relationships between entities
	Edges []CodeEdge `json:"edges"`
	// Package summaries (package → list of entity qualified names)
	Packages map[string][]string `json:"packages"`

	// --- Document chunk layer ---

	// All document chunks, keyed by chunk ID
	Chunks map[string]*DocumentChunk `json:"chunks"`

	// --- Shared indexes ---

	// File index: file path → entity qualified names in that file
	FileEntities map[string][]string `json:"file_entities"`
	// File index: file path → chunk IDs in that file
	FileChunks map[string][]string `json:"file_chunks"`

	// Stats
	TotalFiles    int `json:"total_files"`
	TotalEntities int `json:"total_entities"`
	TotalEdges    int `json:"total_edges"`
	TotalChunks   int `json:"total_chunks"`

	// Timing
	AnalyzedAt time.Time `json:"analyzed_at"`
}

// NewCodeGraph creates an empty code graph for a repository.
func NewCodeGraph(repoPath, repoName string) *CodeGraph {
	return &CodeGraph{
		RepoPath:     repoPath,
		RepoName:     repoName,
		Entities:     make(map[string]*CodeEntity),
		Edges:        nil,
		Packages:     make(map[string][]string),
		Chunks:       make(map[string]*DocumentChunk),
		FileEntities: make(map[string][]string),
		FileChunks:   make(map[string][]string),
	}
}

// AddEntity registers a code entity and indexes it.
func (g *CodeGraph) AddEntity(e *CodeEntity) {
	g.Entities[e.QualifiedName] = e
	g.FileEntities[e.FilePath] = append(g.FileEntities[e.FilePath], e.QualifiedName)
	if e.Package != "" {
		g.Packages[e.Package] = append(g.Packages[e.Package], e.QualifiedName)
	}
	g.TotalEntities = len(g.Entities)
}

// AddEdge registers a relationship between entities.
func (g *CodeGraph) AddEdge(from, to string, kind EdgeKind, filePath string) {
	g.Edges = append(g.Edges, CodeEdge{From: from, To: to, Kind: kind, FilePath: filePath})
	g.TotalEdges = len(g.Edges)
}

// AddChunk registers a document chunk and indexes it.
func (g *CodeGraph) AddChunk(c *DocumentChunk) {
	g.Chunks[c.ID] = c
	g.FileChunks[c.FilePath] = append(g.FileChunks[c.FilePath], c.ID)
	g.TotalChunks = len(g.Chunks)
}

// ---------------------------------------------------------------------------
// Enriched embedding types
// ---------------------------------------------------------------------------

// EmbeddingUnit is the enriched text prepared for embedding.
// For code entities we embed signature + doc + relationship context.
// For document chunks we embed the content with file-path context.
type EmbeddingUnit struct {
	// Identity — either a qualified entity name or a chunk ID
	ID       string     `json:"id"`
	Kind     EntityKind `json:"kind,omitempty"`       // set for code entities
	ChunkKnd ChunkKind  `json:"chunk_kind,omitempty"` // set for doc chunks
	FilePath string     `json:"file_path"`

	// The enriched text to embed
	EnrichedText string `json:"enriched_text"`

	// The raw source for display
	SourceText string `json:"source_text"`

	// Vector
	Embedding []float32 `json:"embedding,omitempty"`
}

// ---------------------------------------------------------------------------
// Wiki output types
// ---------------------------------------------------------------------------

// WikiStructure is the plan for a generated wiki.
type WikiStructure struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Sections    []WikiSection `json:"sections"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Provider    string        `json:"provider,omitempty"`
	Model       string        `json:"model,omitempty"`
}

// WikiSection groups related wiki pages.
type WikiSection struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	PageIDs     []string `json:"page_ids"`
}

// WikiPage is a single generated documentation page.
type WikiPage struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	FilePaths    []string `json:"file_paths"`
	Entities     []string `json:"entities,omitempty"`  // code entities covered
	ChunkIDs     []string `json:"chunk_ids,omitempty"` // document chunks used
	Importance   string   `json:"importance"`          // "high", "medium", "low"
	RelatedPages []string `json:"related_pages,omitempty"`
	SectionID    string   `json:"section_id,omitempty"`
	GeneratedAt  string   `json:"generated_at,omitempty"`
}

// WikiCache stores a fully generated wiki.
type WikiCache struct {
	Structure      WikiStructure       `json:"wiki_structure"`
	GeneratedPages map[string]WikiPage `json:"generated_pages"`
	RepoPath       string              `json:"repo_path"`
	Provider       string              `json:"provider"`
	Model          string              `json:"model"`
	Language       string              `json:"language"`
	GraphStats     CodeGraphStats      `json:"graph_stats"`
	// SmartCache enables incremental updates by tracking file hashes
	SmartCache *SmartCacheState `json:"smart_cache,omitempty"`
}

// CodeGraphStats summarizes the analysis results.
type CodeGraphStats struct {
	TotalFiles      int            `json:"total_files"`
	TotalEntities   int            `json:"total_entities"`
	TotalEdges      int            `json:"total_edges"`
	TotalChunks     int            `json:"total_chunks"`
	PrimaryLanguage string         `json:"primary_language"`
	EntityCounts    map[string]int `json:"entity_counts"` // kind → count
}

// ---------------------------------------------------------------------------
// Request/response types
// ---------------------------------------------------------------------------

// GenerateRequest configures a wiki generation run.
type GenerateRequest struct {
	RepoPath    string   `json:"repo_path"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	Language    string   `json:"language,omitempty"`
	MaxPages    int      `json:"max_pages,omitempty"`
	FocusDirs   []string `json:"focus_dirs,omitempty"`
	ExcludeDirs []string `json:"exclude_dirs,omitempty"`

	// Workers overrides the global DEEPWIKI_WORKERS concurrency limit for this
	// run only.  0 means "use the global default" (v4LLMWorkers / v3WorkerSections).
	Workers int `json:"workers,omitempty"`

	// OnPlanReady is called once the wiki plan (sections + page list) is ready,
	// before any content generation begins. Allows the caller to show the full
	// planned structure in the UI immediately. Optional; nil is a no-op.
	OnPlanReady func(*FinalPlan) `json:"-"`
}

// GenerateProgress reports progress during wiki generation.
type GenerateProgress struct {
	Phase   string  `json:"phase"` // "analyzing", "chunking", "embedding", "planning", "generating", "done"
	Step    string  `json:"step"`
	Current int     `json:"current"`
	Total   int     `json:"total"`
	Pct     float64 `json:"pct"`
}

// ResearchRequest configures a deep research session.
type ResearchRequest struct {
	RepoPath      string `json:"repo_path"`
	Query         string `json:"query"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	MaxIterations int    `json:"max_iterations,omitempty"`
	Language      string `json:"language,omitempty"`
}

// ResearchResult is the output of a deep research session.
type ResearchResult struct {
	Query      string   `json:"query"`
	Iterations []string `json:"iterations"`
	Conclusion string   `json:"conclusion"`
	FilesUsed  []string `json:"files_used"`
	Entities   []string `json:"entities"` // code entities investigated
}

// ---------------------------------------------------------------------------
// Planner Council types — Stage 3
// ---------------------------------------------------------------------------

// GroundingLayer is the deterministic context assembled before any LLM call.
// Built entirely from CodeGraph — no LLM involved.
type GroundingLayer struct {
	FileTree    string              // rendered directory tree (indented)
	ImportGraph map[string][]string // fileA → []fileB (direct imports)
	EntryPoints []string            // file paths with 0 in-degree (mains, cmds)
	Interfaces  []*CodeEntity       // all KindInterface entities
	DocFiles    []DocFile           // README.md, AGENTS.md, *.md content
	TopFiles    []TopFile           // top-N files by entity count (for Planner A)
}

// DocFile holds the content of a documentation file.
type DocFile struct {
	Path    string
	Content string // full content, truncated at 6000 chars
}

// TopFile holds a file path and its full source content for Planner A.
type TopFile struct {
	Path    string
	Content string // full content, truncated at 4000 chars
}

// TopicCluster is a group of semantically similar files (from Planner B embedding clustering).
type TopicCluster struct {
	Label                string   // short descriptive label
	Files                []string // file paths in this cluster
	RepresentativeChunks []string // top-3 chunk contents (not IDs)
	EdgeCount            int      // import edges to other clusters
}

// StructuralMap is Planner A's output — the code-first structural view.
type StructuralMap struct {
	Layers     []StructuralLayer `json:"layers"`
	Boundaries []string          `json:"boundaries"` // "LayerA → LayerB via InterfaceX"
}

// StructuralLayer is one architectural layer identified by Planner A.
type StructuralLayer struct {
	Name        string   `json:"name"`
	Files       []string `json:"files"`
	Interfaces  []string `json:"interfaces"`   // interface entity names
	EntryPoints []string `json:"entry_points"` // entry point file paths
}

// ThematicMap is Planner B's output — the embedding-based thematic view.
type ThematicMap struct {
	Themes []Theme `json:"themes"`
}

// Theme is one thematic area identified by Planner B from semantic clustering.
type Theme struct {
	Name      string   `json:"name"`
	Label     string   `json:"label,omitempty"` // optional alias used in tests and display
	Files     []string `json:"files"`
	Workflows []string `json:"workflows"`
	Concepts  []string `json:"concepts"`
	DataFlows []string `json:"data_flows"`
}

// ---------------------------------------------------------------------------
// Enhanced plan types — FinalPlan from Synthesis Planner
// ---------------------------------------------------------------------------

// FinalPlan is the synthesis planner's output.
type FinalPlan struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Sections    []FinalSection `json:"sections"`
}

// FinalSection groups FinalPage entries under a named section.
type FinalSection struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Pages       []FinalPage `json:"pages"`
}

// FinalPage is a page entry in the FinalPlan with full generation metadata.
type FinalPage struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Kind             string            `json:"kind"` // overview|architecture|flow|concepts|reference
	FocusEntities    []string          `json:"focus_entities"`
	FocusFiles       []string          `json:"focus_files"`       // verified against FileTree
	RequiredSections []string          `json:"required_sections"` // ordered H2 headings
	SectionQueries   map[string]string `json:"section_queries"`   // heading → search query
	RelatedPages     []string          `json:"related_pages"`     // page IDs in this plan
	ModelHint        string            `json:"model_hint"`        // "fast"|"medium"|"heavy"
	Importance       string            `json:"importance"`        // "high"|"medium"|"low"
}

// ---------------------------------------------------------------------------
// Page builder types — Stage 4
// ---------------------------------------------------------------------------

// SectionPlan is the skeleton pass output: one entry per H2 section.
type SectionPlan struct {
	Heading      string   `json:"heading"`
	Scope        string   `json:"scope"`        // 1-sentence description of what this covers
	SearchQuery  string   `json:"search_query"` // specific query for embedder.Search
	NeedsDiagram bool     `json:"needs_diagram"`
	NeedsTable   bool     `json:"needs_table"`
	FilesToRead  []string `json:"files_to_read"` // subset of focus_files relevant to this section
}

// VerificationReport is the reflector pass output.
type VerificationReport struct {
	Valid  bool                `json:"valid"`
	Issues []VerificationIssue `json:"issues"`
}

// VerificationIssue describes a single factual problem found by the Reflector.
type VerificationIssue struct {
	Type        string `json:"type"`        // "bad_file_ref"|"bad_entity_ref"|"hallucinated_claim"
	Location    string `json:"location"`    // e.g. "## Auth Flow section"
	Description string `json:"description"` // what is wrong
}

// PageSummary is the ACE delta appended after each page completes.
// Subsequent pages receive these summaries to avoid repetition and enable cross-refs.
type PageSummary struct {
	PageID   string   `json:"page_id"`
	Title    string   `json:"title"`
	Bullets  []string `json:"bullets"`  // 3 key points extracted from the page
	Files    []string `json:"files"`    // files covered by this page
	Entities []string `json:"entities"` // entity names mentioned
}

// SmartCacheState tracks file hashes and page dependencies for incremental updates.
type SmartCacheState struct {
	// FileHashes maps file paths to their content hash (SHA256)
	FileHashes map[string]string `json:"file_hashes"`

	// FileToPages is a reverse index: which pages reference each source file
	FileToPages map[string][]string `json:"file_to_pages"`

	// PageContentHash stores hash of generated page content (detect content drift)
	PageContentHash map[string]string `json:"page_content_hash"`

	// LastFullGeneration timestamp of last complete wiki generation
	LastFullGeneration time.Time `json:"last_full_generation"`

	// IncrementalUpdates count of successful incremental updates
	IncrementalUpdates int `json:"incremental_updates"`
}
