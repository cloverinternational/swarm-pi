package deepwiki

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BuildGroundingLayer constructs the deterministic context used by Planner A
// (code-first) and the Synthesis Planner. No LLM is involved — everything
// is derived from the already-built CodeGraph plus the filesystem.
func BuildGroundingLayer(graph *CodeGraph, maxTopFiles int) *GroundingLayer {
	if maxTopFiles <= 0 {
		maxTopFiles = 5
	}

	gl := &GroundingLayer{
		ImportGraph: make(map[string][]string),
	}

	// 1. Build import graph from CodeEdge entries (EdgeImports)
	for _, edge := range graph.Edges {
		if edge.Kind == EdgeImports && edge.FilePath != "" {
			rel := graphRelPath(graph.RepoPath, edge.FilePath)
			gl.ImportGraph[rel] = append(gl.ImportGraph[rel], edge.To)
		}
	}
	// Also derive imports from CodeEntity.Imports slice
	for _, ent := range graph.Entities {
		if len(ent.Imports) > 0 {
			rel := graphRelPath(graph.RepoPath, ent.FilePath)
			for _, imp := range ent.Imports {
				gl.ImportGraph[rel] = append(gl.ImportGraph[rel], imp)
			}
		}
	}
	// Deduplicate import graph edges
	for k, v := range gl.ImportGraph {
		gl.ImportGraph[k] = graphDedupStrings(v)
	}

	// 2. Find entry points: files that contain main() or are in cmd/ directories
	entrySet := make(map[string]bool)
	for filePath, names := range graph.FileEntities {
		rel := graphRelPath(graph.RepoPath, filePath)
		isCmd := strings.Contains(rel, "cmd/") ||
			strings.HasSuffix(rel, "main.go") ||
			strings.Contains(rel, "/main.go")
		for _, qname := range names {
			if ent, ok := graph.Entities[qname]; ok {
				if ent.Kind == KindFunction && ent.Name == "main" {
					entrySet[rel] = true
				}
			}
		}
		if isCmd {
			entrySet[rel] = true
		}
	}
	for rel := range entrySet {
		gl.EntryPoints = append(gl.EntryPoints, rel)
	}
	sort.Strings(gl.EntryPoints)

	// 3. Collect all interface entities
	for _, ent := range graph.Entities {
		if ent.Kind == KindInterface {
			gl.Interfaces = append(gl.Interfaces, ent)
		}
	}
	sort.Slice(gl.Interfaces, func(i, j int) bool {
		return gl.Interfaces[i].QualifiedName < gl.Interfaces[j].QualifiedName
	})

	// 4. Find documentation files (README, AGENTS.md, etc.) at shallow depth
	docNames := map[string]bool{
		"readme.md": true, "agents.md": true, "contributing.md": true,
		"architecture.md": true, "design.md": true, "overview.md": true,
	}
	seenDocPaths := make(map[string]bool)
	for filePath := range graph.FileChunks {
		rel := graphRelPath(graph.RepoPath, filePath)
		depth := strings.Count(rel, string(filepath.Separator))
		if depth > 2 {
			continue
		}
		base := strings.ToLower(filepath.Base(rel))
		if !docNames[base] {
			continue
		}
		if seenDocPaths[rel] {
			continue
		}
		seenDocPaths[rel] = true
		absPath := filePath
		if !filepath.IsAbs(filePath) {
			absPath = filepath.Join(graph.RepoPath, filePath)
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		c := string(content)
		if len(c) > 6000 {
			c = c[:6000] + "\n... (truncated)"
		}
		gl.DocFiles = append(gl.DocFiles, DocFile{Path: rel, Content: c})
	}
	// Sort doc files so README comes first
	sort.Slice(gl.DocFiles, func(i, j int) bool {
		ai := strings.ToLower(gl.DocFiles[i].Path)
		aj := strings.ToLower(gl.DocFiles[j].Path)
		if strings.Contains(ai, "readme") {
			return true
		}
		if strings.Contains(aj, "readme") {
			return false
		}
		return ai < aj
	})

	// 5. Top N files by entity count (full content for Planner A)
	type fileCount struct {
		path    string
		absPath string
		count   int
	}
	var fileCounts []fileCount
	for fp, names := range graph.FileEntities {
		rel := graphRelPath(graph.RepoPath, fp)
		absPath := fp
		if !filepath.IsAbs(fp) {
			absPath = filepath.Join(graph.RepoPath, fp)
		}
		fileCounts = append(fileCounts, fileCount{rel, absPath, len(names)})
	}
	sort.Slice(fileCounts, func(i, j int) bool {
		return fileCounts[i].count > fileCounts[j].count
	})
	for i, f := range fileCounts {
		if i >= maxTopFiles {
			break
		}
		content, err := os.ReadFile(f.absPath)
		if err != nil {
			continue
		}
		c := string(content)
		if len(c) > 4000 {
			c = c[:4000] + "\n// ... (truncated)"
		}
		gl.TopFiles = append(gl.TopFiles, TopFile{Path: f.path, Content: c})
	}

	// 6. Render file tree
	gl.FileTree = renderFileTree(graph)

	return gl
}

// renderFileTree builds an indented directory tree string from all file paths
// in the CodeGraph. Used by the Synthesis Planner to verify focus_files.
func renderFileTree(graph *CodeGraph) string {
	pathSet := make(map[string]bool)
	for fp := range graph.FileEntities {
		pathSet[graphRelPath(graph.RepoPath, fp)] = true
	}
	for fp := range graph.FileChunks {
		pathSet[graphRelPath(graph.RepoPath, fp)] = true
	}

	var paths []string
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// Track which directories have been printed to avoid duplicates
	printedDirs := make(map[string]bool)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s/\n", graph.RepoName)

	for _, p := range paths {
		parts := strings.Split(filepath.ToSlash(p), "/")
		// Print any new intermediate directories
		for i := 0; i < len(parts)-1; i++ {
			dirKey := strings.Join(parts[:i+1], "/")
			if !printedDirs[dirKey] {
				printedDirs[dirKey] = true
				indent := strings.Repeat("  ", i+1)
				fmt.Fprintf(&sb, "%s%s/\n", indent, parts[i])
			}
		}
		// Print the file
		indent := strings.Repeat("  ", len(parts))
		fmt.Fprintf(&sb, "%s%s\n", indent, parts[len(parts)-1])
	}
	return sb.String()
}

// BuildTopicClusters groups files into up to k semantic clusters using their
// chunk embedding centroids. If embeddings are unavailable, falls back to
// grouping by top-level directory. Used by Planner B.
func BuildTopicClusters(graph *CodeGraph, k int) []TopicCluster {
	if k <= 0 {
		k = 8
	}

	// Collect per-file centroid vectors from chunk embeddings
	type fileVec struct {
		path string
		vec  []float32
	}
	var vecs []fileVec
	for filePath, chunkIDs := range graph.FileChunks {
		rel := graphRelPath(graph.RepoPath, filePath)
		var centroid []float32
		count := 0
		for _, cid := range chunkIDs {
			if chunk, ok := graph.Chunks[cid]; ok && len(chunk.Embedding) > 0 {
				if centroid == nil {
					centroid = make([]float32, len(chunk.Embedding))
				}
				for i, v := range chunk.Embedding {
					centroid[i] += v
				}
				count++
			}
		}
		if count > 0 {
			for i := range centroid {
				centroid[i] /= float32(count)
			}
			vecs = append(vecs, fileVec{rel, centroid})
		}
	}

	// Fallback: no embeddings available
	if len(vecs) == 0 {
		return clusterByDirectory(graph, k)
	}

	// Simple greedy k-means (1 iteration): pick k evenly-spaced seed files
	effectiveK := min(k, len(vecs))
	centroids := make([][]float32, effectiveK)
	step := max(1, len(vecs)/effectiveK)
	for i := range effectiveK {
		centroids[i] = vecs[i*step].vec
	}

	// Assign each file to its nearest centroid
	groups := make(map[int][]string, effectiveK)
	for _, fv := range vecs {
		best, bestSim := 0, float64(-1)
		for ci, cv := range centroids {
			if cv == nil {
				continue
			}
			sim := cosineSim(fv.vec, cv) // uses existing cosineSim from embedder.go
			if sim > bestSim {
				bestSim = sim
				best = ci
			}
		}
		groups[best] = append(groups[best], fv.path)
	}

	// Build TopicCluster entries
	var clusters []TopicCluster
	for ci, files := range groups {
		if len(files) == 0 {
			continue
		}
		_ = ci
		// Collect representative chunks (up to 3, capped at 300 chars each)
		var repChunks []string
		for _, fp := range files {
			absPath := fp
			if !filepath.IsAbs(fp) {
				absPath = filepath.Join(graph.RepoPath, fp)
			}
			chunkIDs := graph.FileChunks[absPath]
			if len(chunkIDs) == 0 {
				chunkIDs = graph.FileChunks[fp]
			}
			for _, cid := range chunkIDs {
				if chunk, ok := graph.Chunks[cid]; ok {
					c := chunk.Content
					if len(c) > 300 {
						c = c[:300] + "..."
					}
					repChunks = append(repChunks, c)
					if len(repChunks) >= 3 {
						break
					}
				}
			}
			if len(repChunks) >= 3 {
				break
			}
		}
		clusters = append(clusters, TopicCluster{
			Label:                fmt.Sprintf("cluster-%d", len(clusters)),
			Files:                files,
			RepresentativeChunks: repChunks,
		})
	}

	return clusters
}

// clusterByDirectory groups files by their top-level directory.
// Used as a fallback when no embedding vectors are available.
func clusterByDirectory(graph *CodeGraph, maxK int) []TopicCluster {
	groups := make(map[string][]string)
	for fp := range graph.FileEntities {
		rel := graphRelPath(graph.RepoPath, fp)
		parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
		dir := parts[0]
		groups[dir] = append(groups[dir], rel)
	}

	var clusters []TopicCluster
	for dir, files := range groups {
		if len(clusters) >= maxK {
			break
		}
		clusters = append(clusters, TopicCluster{
			Label: dir,
			Files: files,
		})
	}
	return clusters
}

// graphRelPath returns path relative to repoRoot.
// If path is already relative or not under repoRoot, returns path as-is.
func graphRelPath(repoRoot, path string) string {
	if repoRoot == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// graphDedupStrings returns a slice with duplicate strings removed (order preserved).
func graphDedupStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := ss[:0:len(ss)]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
