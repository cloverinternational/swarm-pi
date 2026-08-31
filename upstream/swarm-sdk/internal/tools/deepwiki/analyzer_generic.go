package deepwiki

import (
	"fmt"
	"regexp"
	"strings"
)

// langPatterns defines regex patterns for extracting code entities from a language.
type langPatterns struct {
	Function  *regexp.Regexp
	Class     *regexp.Regexp
	Method    *regexp.Regexp
	Interface *regexp.Regexp
	Import    *regexp.Regexp
}

var pythonPatterns = &langPatterns{
	Function: regexp.MustCompile(`(?m)^((?:#[^\n]*\n|"""[\s\S]*?"""\s*\n)*)?(def\s+(\w+)\s*\([^)]*\)[^:]*:)`),
	Class:    regexp.MustCompile(`(?m)^((?:#[^\n]*\n)*)?(class\s+(\w+)(?:\([^)]*\))?\s*:)`),
	Import:   regexp.MustCompile(`(?m)^(?:from\s+(\S+)\s+)?import\s+(.+)`),
}

var typescriptPatterns = &langPatterns{
	Function:  regexp.MustCompile(`(?m)^((?://[^\n]*\n|/\*[\s\S]*?\*/\s*\n)*)?\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)`),
	Class:     regexp.MustCompile(`(?m)^((?://[^\n]*\n|/\*[\s\S]*?\*/\s*\n)*)?\s*(?:export\s+)?(?:abstract\s+)?class\s+(\w+)`),
	Interface: regexp.MustCompile(`(?m)^((?://[^\n]*\n|/\*[\s\S]*?\*/\s*\n)*)?\s*(?:export\s+)?interface\s+(\w+)`),
	Import:    regexp.MustCompile(`(?m)^import\s+.*?from\s+['"]([^'"]+)['"]`),
}

var javascriptPatterns = &langPatterns{
	Function: regexp.MustCompile(`(?m)^((?://[^\n]*\n|/\*[\s\S]*?\*/\s*\n)*)?\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)`),
	Class:    regexp.MustCompile(`(?m)^((?://[^\n]*\n|/\*[\s\S]*?\*/\s*\n)*)?\s*(?:export\s+)?class\s+(\w+)`),
	Import:   regexp.MustCompile(`(?m)^(?:import|const\s+\w+\s*=\s*require\().*?['"]([^'"]+)['"]`),
}

var rustPatterns = &langPatterns{
	Function:  regexp.MustCompile(`(?m)^((?:///[^\n]*\n)*)?\s*(?:pub(?:\([\w:]+\))?\s+)?(?:async\s+)?fn\s+(\w+)`),
	Class:     regexp.MustCompile(`(?m)^((?:///[^\n]*\n)*)?\s*(?:pub(?:\([\w:]+\))?\s+)?struct\s+(\w+)`),
	Interface: regexp.MustCompile(`(?m)^((?:///[^\n]*\n)*)?\s*(?:pub(?:\([\w:]+\))?\s+)?trait\s+(\w+)`),
	Import:    regexp.MustCompile(`(?m)^use\s+([^;]+);`),
}

// GenericParser extracts entities using regex patterns.
// Not as precise as a real AST parser, but works across many languages.
type GenericParser struct {
	lang         string
	funcPatterns *langPatterns
}

func (p *GenericParser) ParseFile(info FileInfo, source []byte) ([]*CodeEntity, []CodeEdge, error) {
	if p.funcPatterns == nil {
		return nil, nil, nil
	}

	src := string(source)
	lines := strings.Split(src, "\n")
	modName := moduleNameFromPath(info.RelPath, p.lang)

	var entities []*CodeEntity
	var edges []CodeEdge

	// File entity
	fileEntity := &CodeEntity{
		QualifiedName: modName + "/" + info.RelPath,
		Kind:          KindFile,
		Name:          info.RelPath,
		Package:       modName,
		FilePath:      info.RelPath,
		StartLine:     1,
		EndLine:       len(lines),
	}

	// Extract imports
	if p.funcPatterns.Import != nil {
		matches := p.funcPatterns.Import.FindAllStringSubmatch(src, -1)
		for _, m := range matches {
			imp := m[len(m)-1]
			fileEntity.Imports = append(fileEntity.Imports, strings.TrimSpace(imp))
		}
	}
	entities = append(entities, fileEntity)

	// Extract functions
	if p.funcPatterns.Function != nil {
		locs := p.funcPatterns.Function.FindAllStringIndex(src, -1)
		matches := p.funcPatterns.Function.FindAllStringSubmatch(src, -1)
		for i, m := range matches {
			name := m[len(m)-1]
			startLine := lineOfOffset(src, locs[i][0])
			endLine := findBlockEndLine(lines, startLine, p.lang)

			doc := ""
			if len(m) > 2 {
				doc = strings.TrimSpace(m[1])
			}

			qname := fmt.Sprintf("%s.%s", modName, name)
			body := extractLines(source, startLine, endLine)

			ent := &CodeEntity{
				QualifiedName: qname,
				Kind:          KindFunction,
				Name:          name,
				Package:       modName,
				FilePath:      info.RelPath,
				StartLine:     startLine,
				EndLine:       endLine,
				Signature:     strings.TrimSpace(extractLines(source, startLine, startLine)),
				DocComment:    doc,
				Body:          body,
			}
			entities = append(entities, ent)
			edges = append(edges, CodeEdge{
				From: fileEntity.QualifiedName, To: qname,
				Kind: EdgeContains, FilePath: info.RelPath,
			})
		}
	}

	// Extract classes/structs
	if p.funcPatterns.Class != nil {
		locs := p.funcPatterns.Class.FindAllStringIndex(src, -1)
		matches := p.funcPatterns.Class.FindAllStringSubmatch(src, -1)
		for i, m := range matches {
			name := m[len(m)-1]
			startLine := lineOfOffset(src, locs[i][0])
			endLine := findBlockEndLine(lines, startLine, p.lang)

			doc := ""
			if len(m) > 2 {
				doc = strings.TrimSpace(m[1])
			}

			qname := fmt.Sprintf("%s.%s", modName, name)

			ent := &CodeEntity{
				QualifiedName: qname,
				Kind:          KindClass,
				Name:          name,
				Package:       modName,
				FilePath:      info.RelPath,
				StartLine:     startLine,
				EndLine:       endLine,
				Signature:     strings.TrimSpace(extractLines(source, startLine, startLine)),
				DocComment:    doc,
				Body:          extractLines(source, startLine, endLine),
			}
			entities = append(entities, ent)
			edges = append(edges, CodeEdge{
				From: fileEntity.QualifiedName, To: qname,
				Kind: EdgeContains, FilePath: info.RelPath,
			})
		}
	}

	// Extract interfaces/traits
	if p.funcPatterns.Interface != nil {
		locs := p.funcPatterns.Interface.FindAllStringIndex(src, -1)
		matches := p.funcPatterns.Interface.FindAllStringSubmatch(src, -1)
		for i, m := range matches {
			name := m[len(m)-1]
			startLine := lineOfOffset(src, locs[i][0])
			endLine := findBlockEndLine(lines, startLine, p.lang)

			qname := fmt.Sprintf("%s.%s", modName, name)

			ent := &CodeEntity{
				QualifiedName: qname,
				Kind:          KindInterface,
				Name:          name,
				Package:       modName,
				FilePath:      info.RelPath,
				StartLine:     startLine,
				EndLine:       endLine,
				Signature:     strings.TrimSpace(extractLines(source, startLine, startLine)),
				Body:          extractLines(source, startLine, endLine),
			}
			entities = append(entities, ent)
			edges = append(edges, CodeEdge{
				From: fileEntity.QualifiedName, To: qname,
				Kind: EdgeContains, FilePath: info.RelPath,
			})
		}
	}

	return entities, edges, nil
}

// --- helpers ---

func moduleNameFromPath(relPath, lang string) string {
	dir := strings.TrimSuffix(relPath, "/"+lastPathComponent(relPath))
	if dir == relPath || dir == "" {
		return "root"
	}
	return strings.ReplaceAll(dir, "/", ".")
}

func lastPathComponent(p string) string {
	parts := strings.Split(p, "/")
	return parts[len(parts)-1]
}

func lineOfOffset(src string, offset int) int {
	return strings.Count(src[:offset], "\n") + 1
}

// findBlockEndLine estimates the end of a block starting at startLine.
// Uses brace counting for brace languages, indentation for Python.
func findBlockEndLine(lines []string, startLine int, lang string) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}

	if lang == "python" {
		// Python: indentation-based block detection
		if startLine >= len(lines) {
			return startLine
		}
		baseLine := lines[startLine-1]
		baseIndent := countLeadingSpaces(baseLine)
		end := startLine
		for i := startLine; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed[0] == '#' {
				end = i + 1
				continue
			}
			indent := countLeadingSpaces(line)
			if indent <= baseIndent {
				break
			}
			end = i + 1
		}
		return end
	}

	// Brace-based languages
	depth := 0
	started := false
	for i := startLine - 1; i < len(lines); i++ {
		line := lines[i]
		for _, ch := range line {
			if ch == '{' {
				depth++
				started = true
			} else if ch == '}' {
				depth--
				if started && depth <= 0 {
					return i + 1
				}
			}
		}
	}

	// Fallback: return startLine + some reasonable limit
	end := min(startLine+50, len(lines))
	return end
}

func countLeadingSpaces(s string) int {
	count := 0
	for _, ch := range s {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 4
		} else {
			break
		}
	}
	return count
}
