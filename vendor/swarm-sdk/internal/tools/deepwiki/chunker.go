package deepwiki

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// Chunker splits files into overlapping document chunks.
// This is the "broad coverage" layer — every file gets chunked for retrieval,
// regardless of whether the AST parser understands the language.
//
// Unlike DeepWiki-open which uses a dumb word-count splitter (350 words, 100 overlap),
// this chunker is structure-aware:
//   - Markdown: splits on headings
//   - Code: splits on blank-line boundaries (respects function boundaries)
//   - Config: splits on top-level keys
//   - Fallback: overlapping character windows
type Chunker struct {
	// Target chunk size in characters
	TargetSize int
	// Overlap between chunks in characters
	Overlap int
	// Maximum chunk size before forced split
	MaxSize int
}

// NewChunker creates a chunker with sensible defaults.
func NewChunker() *Chunker {
	return &Chunker{
		TargetSize: 1500,
		Overlap:    200,
		MaxSize:    3000,
	}
}

// ChunkFile reads a file and produces document chunks.
func (c *Chunker) ChunkFile(f FileInfo) ([]*DocumentChunk, error) {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	if len(strings.TrimSpace(content)) == 0 {
		return nil, nil
	}

	kind := classifyChunkKind(f.Language)

	var sections []textSection
	switch {
	case f.Language == "markdown" || f.Language == "rst":
		sections = c.splitMarkdown(content)
	case f.IsCode:
		sections = c.splitCode(content)
	case f.Language == "yaml" || f.Language == "toml" || f.Language == "json":
		sections = c.splitConfig(content)
	default:
		sections = c.splitOverlapping(content)
	}

	var chunks []*DocumentChunk
	for i, sec := range sections {
		text := strings.TrimSpace(sec.text)
		if text == "" {
			continue
		}

		id := chunkID(f.RelPath, i)
		chunks = append(chunks, &DocumentChunk{
			ID:         id,
			FilePath:   f.RelPath,
			Kind:       kind,
			Language:   f.Language,
			Content:    text,
			StartLine:  sec.startLine,
			EndLine:    sec.endLine,
			ChunkIndex: i,
			FileSize:   f.Size,
			IsCode:     f.IsCode,
		})
	}

	return chunks, nil
}

// textSection is a raw section extracted from a file.
type textSection struct {
	text      string
	startLine int
	endLine   int
}

// splitMarkdown splits on heading boundaries.
func (c *Chunker) splitMarkdown(content string) []textSection {
	lines := strings.Split(content, "\n")
	var sections []textSection
	var current strings.Builder
	startLine := 1

	for i, line := range lines {
		lineNum := i + 1
		isHeading := strings.HasPrefix(line, "#")

		if isHeading && current.Len() > 0 {
			// Flush current section
			sections = append(sections, textSection{
				text: current.String(), startLine: startLine, endLine: lineNum - 1,
			})
			current.Reset()
			startLine = lineNum
		}

		current.WriteString(line)
		current.WriteString("\n")

		// Force split if too large
		if current.Len() > c.MaxSize {
			sections = append(sections, textSection{
				text: current.String(), startLine: startLine, endLine: lineNum,
			})
			current.Reset()
			startLine = lineNum + 1
		}
	}

	if current.Len() > 0 {
		sections = append(sections, textSection{
			text: current.String(), startLine: startLine, endLine: len(lines),
		})
	}

	return sections
}

// splitCode splits on double-newline boundaries (respects function spacing).
func (c *Chunker) splitCode(content string) []textSection {
	lines := strings.Split(content, "\n")
	var sections []textSection
	var current strings.Builder
	startLine := 1
	prevBlank := false

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)
		isBlank := trimmed == ""

		// Split on double-blank-line if chunk is large enough
		if isBlank && prevBlank && current.Len() >= c.TargetSize {
			sections = append(sections, textSection{
				text: current.String(), startLine: startLine, endLine: lineNum - 2,
			})
			current.Reset()
			startLine = lineNum + 1
			prevBlank = false
			continue
		}

		current.WriteString(line)
		current.WriteString("\n")
		prevBlank = isBlank

		// Force split if too large
		if current.Len() > c.MaxSize {
			sections = append(sections, textSection{
				text: current.String(), startLine: startLine, endLine: lineNum,
			})
			current.Reset()
			startLine = lineNum + 1
		}
	}

	if current.Len() > 0 {
		sections = append(sections, textSection{
			text: current.String(), startLine: startLine, endLine: len(lines),
		})
	}

	return sections
}

// splitConfig splits YAML/TOML/JSON on top-level boundaries.
func (c *Chunker) splitConfig(content string) []textSection {
	// For configs, use overlapping windows — structure is hard to parse generically
	return c.splitOverlapping(content)
}

// splitOverlapping creates overlapping character-window chunks.
func (c *Chunker) splitOverlapping(content string) []textSection {
	lines := strings.Split(content, "\n")

	if len(content) <= c.TargetSize {
		return []textSection{{text: content, startLine: 1, endLine: len(lines)}}
	}

	var sections []textSection
	var current strings.Builder
	startLine := 1

	for i, line := range lines {
		lineNum := i + 1
		current.WriteString(line)
		current.WriteString("\n")

		if current.Len() >= c.TargetSize {
			sections = append(sections, textSection{
				text: current.String(), startLine: startLine, endLine: lineNum,
			})

			// Keep overlap
			overlapStart := lineNum - c.overlapLines(lines, lineNum)
			if overlapStart < lineNum {
				current.Reset()
				for j := overlapStart; j <= lineNum && j < len(lines); j++ {
					current.WriteString(lines[j])
					current.WriteString("\n")
				}
				startLine = overlapStart + 1
			} else {
				current.Reset()
				startLine = lineNum + 1
			}
		}
	}

	if current.Len() > 0 {
		sections = append(sections, textSection{
			text: current.String(), startLine: startLine, endLine: len(lines),
		})
	}

	return sections
}

func (c *Chunker) overlapLines(lines []string, endLine int) int {
	chars := 0
	count := 0
	for i := endLine - 1; i >= 0 && chars < c.Overlap; i-- {
		chars += len(lines[i]) + 1
		count++
	}
	return count
}

func classifyChunkKind(lang string) ChunkKind {
	switch lang {
	case "markdown", "rst", "text":
		return ChunkDoc
	case "yaml", "toml", "json", "xml":
		return ChunkConfig
	default:
		if isCodeLanguage(lang) {
			return ChunkCode
		}
		return ChunkData
	}
}

func chunkID(relPath string, index int) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s:%d", relPath, index))
	return fmt.Sprintf("chunk:%x", h[:8])
}
