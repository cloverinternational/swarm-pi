package deepwiki

import "math"

// FilterChunks removes low-relevance results from a search result set using
// BM25-inspired term frequency scoring before they reach any LLM.
//
// Research shows that semantically related but wrong chunks (distractors) cause
// 6-11 point accuracy drops even when the correct passage is present.
// Better retrievers surface MORE dangerous distractors, making this filter
// especially important when using strong embedding backends.
//
// Parameters:
//   - results:   ranked search results from Embedder.Search
//   - query:     the section-specific search query (NOT the page title)
//   - maxK:      maximum number of results to return (default 6)
//   - threshold: minimum normalised BM25 score to keep (0.0–1.0, default 0.15)
//
// Always returns at least 1 result even if all score below threshold.
func FilterChunks(results []SearchResult, query string, maxK int, threshold float64) []SearchResult {
	if len(results) == 0 {
		return results
	}
	if maxK <= 0 {
		maxK = 6
	}
	if threshold <= 0 {
		threshold = 0.15
	}

	// tokenize uses the existing package-level function from embedder.go
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		if len(results) > maxK {
			return results[:maxK]
		}
		return results
	}

	// Build corpus text slice for IDF calculation
	corpus := make([]string, 0, len(results))
	for _, r := range results {
		corpus = append(corpus, resultText(r))
	}

	idf := filterComputeIDF(queryTerms, corpus)

	type scoredResult struct {
		r     SearchResult
		score float64
	}
	scored := make([]scoredResult, len(results))
	for i, r := range results {
		scored[i] = scoredResult{r, filterBM25Score(queryTerms, resultText(r), idf)}
	}

	// Find max score to normalise
	maxScore := 0.0
	for _, s := range scored {
		if s.score > maxScore {
			maxScore = s.score
		}
	}

	var out []SearchResult
	for _, s := range scored {
		norm := 0.0
		if maxScore > 0 {
			norm = s.score / maxScore
		}
		if norm >= threshold {
			out = append(out, s.r)
		}
		if len(out) >= maxK {
			break
		}
	}

	// Always return at least 1
	if len(out) == 0 && len(results) > 0 {
		out = []SearchResult{results[0]}
	}
	return out
}

// resultText extracts the text to score from a SearchResult.
func resultText(r SearchResult) string {
	if r.Chunk != nil {
		return r.Chunk.Content
	}
	if r.Entity != nil {
		return r.Entity.Body + " " + r.Entity.Signature + " " + r.Entity.DocComment
	}
	return ""
}

// filterBM25Score computes a BM25-inspired relevance score.
// k1=1.5, b=0.75 are standard BM25 parameters.
// avgDocLen=500 is a reasonable approximation for code/doc chunks.
func filterBM25Score(queryTerms []string, doc string, idf map[string]float64) float64 {
	const k1 = 1.5
	const b = 0.75
	const avgDocLen = 500.0

	docTerms := tokenize(doc)
	docLen := float64(len(docTerms))
	tf := make(map[string]int, len(docTerms))
	for _, t := range docTerms {
		tf[t]++
	}

	score := 0.0
	for _, qt := range queryTerms {
		if idfVal, ok := idf[qt]; ok {
			f := float64(tf[qt])
			numerator := f * (k1 + 1)
			denominator := f + k1*(1-b+b*docLen/avgDocLen)
			if denominator > 0 {
				score += idfVal * (numerator / denominator)
			}
		}
	}
	return score
}

// filterComputeIDF computes inverse document frequency for query terms
// across the given corpus.
func filterComputeIDF(queryTerms []string, corpus []string) map[string]float64 {
	N := float64(len(corpus))
	if N == 0 {
		N = 1
	}

	df := make(map[string]int)
	for _, doc := range corpus {
		docTerms := tokenize(doc)
		seen := make(map[string]bool)
		for _, t := range docTerms {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	idf := make(map[string]float64, len(queryTerms))
	for _, qt := range queryTerms {
		docFreq := float64(df[qt])
		idf[qt] = math.Log((N-docFreq+0.5)/(docFreq+0.5) + 1)
	}
	return idf
}
