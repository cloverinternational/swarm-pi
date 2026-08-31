package toolout

import (
	"strings"
	"unicode/utf8"
)

// BoundResult contains the retained head and tail of a bounded string together
// with enough provenance for callers to render an honest elision marker.
// Character counts are UTF-8 byte counts, matching Go's len(string) and the
// agent's existing tool-output limits.
type BoundResult struct {
	Output        string
	Head          string
	Tail          string
	OriginalChars int
	OriginalLines int
	HeadChars     int
	TailChars     int
	ElidedChars   int
	ElidedLines   int
	Truncated     bool
}

// Bound retains the head and tail of input while enforcing positive character
// and line limits. A non-positive limit disables that limit. Bound does not
// allocate a copy of input: Head and Tail are substrings backed by input, and
// Output is only assembled when truncation is required.
func Bound(input string, maxChars, maxLines int) BoundResult {
	originalLines := countLines(input)
	result := BoundResult{
		Output:        input,
		Head:          input,
		OriginalChars: len(input),
		OriginalLines: originalLines,
		HeadChars:     len(input),
	}
	overChars := maxChars > 0 && len(input) > maxChars
	overLines := maxLines > 0 && originalLines > maxLines
	if !overChars && !overLines {
		return result
	}

	headBudget, tailBudget := len(input), len(input)
	if maxChars > 0 {
		headBudget = (maxChars + 1) / 2
		tailBudget = maxChars / 2
	}
	headEnd := utf8SafePrefixEnd(input, headBudget)
	tailStart := utf8SafeSuffixStart(input, tailBudget)

	if maxLines > 0 {
		if maxLines == 1 && originalLines > 1 {
			headEnd = min(headEnd, strings.IndexByte(input, '\n'))
			tailStart = max(tailStart, strings.LastIndexByte(input, '\n')+1)
		} else {
			headLines := (maxLines + 1) / 2
			tailLines := maxLines / 2
			headEnd = min(headEnd, prefixEndForLines(input, headLines))
			tailStart = max(tailStart, suffixStartForLines(input, tailLines))
		}
	}
	if headEnd > tailStart {
		headEnd = tailStart
	}

	result.Head = input[:headEnd]
	result.Tail = input[tailStart:]
	result.Output = result.Head + result.Tail
	result.HeadChars = len(result.Head)
	result.TailChars = len(result.Tail)
	result.ElidedChars = tailStart - headEnd
	result.ElidedLines = originalLines - countLines(result.Output)
	if result.ElidedLines < 0 {
		result.ElidedLines = 0
	}
	result.Truncated = result.ElidedChars > 0
	return result
}

func countLines(input string) int {
	if input == "" {
		return 0
	}
	return strings.Count(input, "\n") + 1
}

func utf8SafePrefixEnd(input string, budget int) int {
	if budget >= len(input) {
		return len(input)
	}
	if budget <= 0 {
		return 0
	}
	end := budget
	for end > 0 && !utf8.RuneStart(input[end]) {
		end--
	}
	return end
}

func utf8SafeSuffixStart(input string, budget int) int {
	if budget >= len(input) {
		return 0
	}
	if budget <= 0 {
		return len(input)
	}
	start := len(input) - budget
	for start < len(input) && !utf8.RuneStart(input[start]) {
		start++
	}
	return start
}

func prefixEndForLines(input string, lines int) int {
	if lines <= 0 {
		return 0
	}
	end := 0
	for range lines {
		index := strings.IndexByte(input[end:], '\n')
		if index < 0 {
			return len(input)
		}
		end += index + 1
	}
	return end
}

func suffixStartForLines(input string, lines int) int {
	if lines <= 0 {
		return len(input)
	}
	start := len(input)
	for range lines {
		index := strings.LastIndexByte(input[:start], '\n')
		if index < 0 {
			return 0
		}
		start = index
	}
	return start + 1
}
