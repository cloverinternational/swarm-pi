package scope

import (
	"regexp"
	"strings"
)

// SQLDetector detects scope boundaries in SQL source files.
// Tracks: CREATE TABLE/VIEW/FUNCTION/PROCEDURE, BEGIN/END blocks,
// IF/ELSE, LOOP, CASE, WITH (CTEs), subqueries.
type SQLDetector struct{}

func init() {
	Register(&SQLDetector{}, ".sql", ".psql", ".plsql", ".pgsql")
}

func (d *SQLDetector) Name() string { return "sql" }

var sqlPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// CREATE TABLE / VIEW / INDEX
	{
		re:    regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:TEMP(?:ORARY)?\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`),
		label: func(m []string) string { return "TABLE " + m[1] },
	},
	{
		re:    regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:MATERIALIZED\s+)?VIEW\s+(\w+)`),
		label: func(m []string) string { return "VIEW " + m[1] },
	},
	// CREATE FUNCTION / PROCEDURE / TRIGGER
	{
		re:    regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+(\w+)`),
		label: func(m []string) string { return "FUNCTION " + m[1] },
	},
	{
		re:    regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?PROCEDURE\s+(\w+)`),
		label: func(m []string) string { return "PROCEDURE " + m[1] },
	},
	{
		re:    regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?TRIGGER\s+(\w+)`),
		label: func(m []string) string { return "TRIGGER " + m[1] },
	},
	// BEGIN block
	{
		re:    regexp.MustCompile(`(?i)^\s*BEGIN\s*$`),
		label: func(m []string) string { return "BEGIN" },
	},
	// IF
	{
		re:    regexp.MustCompile(`(?i)^\s*(?:ELSE\s+)?IF\s+`),
		label: func(m []string) string { return "IF" },
	},
	// ELSE
	{
		re:    regexp.MustCompile(`(?i)^\s*ELSE\s*$`),
		label: func(m []string) string { return "ELSE" },
	},
	// LOOP / WHILE / FOR
	{
		re:    regexp.MustCompile(`(?i)^\s*(?:WHILE|FOR)\s+`),
		label: func(m []string) string { return "LOOP" },
	},
	{
		re:    regexp.MustCompile(`(?i)^\s*LOOP\s*$`),
		label: func(m []string) string { return "LOOP" },
	},
	// CASE
	{
		re:    regexp.MustCompile(`(?i)^\s*CASE\s`),
		label: func(m []string) string { return "CASE" },
	},
	// WITH (CTE)
	{
		re:    regexp.MustCompile(`(?i)^\s*WITH\s+(\w+)\s+AS`),
		label: func(m []string) string { return "CTE " + m[1] },
	},
}

func (d *SQLDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	type scopeLevel struct {
		indent int
		label  string
	}
	var stack []scopeLevel

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			if i > 0 {
				result[i] = copyChain(result[i-1])
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		indent := measureIndent(line)

		// END closes a scope
		if upper == "END" || upper == "END;" || strings.HasPrefix(upper, "END ") ||
			upper == "END IF;" || upper == "END LOOP;" || upper == "END CASE;" {
			chain := make(ScopeChain, len(stack))
			for j, s := range stack {
				chain[j] = ScopeEntry{Label: s.label}
			}
			result[i] = chain

			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		// ) at start of line (closing subquery/CTE)
		if trimmed == ")" || trimmed == ")," || trimmed == ");" {
			if len(stack) > 0 {
				chain := make(ScopeChain, len(stack))
				for j, s := range stack {
					chain[j] = ScopeEntry{Label: s.label}
				}
				result[i] = chain
				stack = stack[:len(stack)-1]
				continue
			}
		}

		// Pop deeper scopes
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		label := d.matchLabel(line)
		if label != "" {
			stack = append(stack, scopeLevel{indent: indent, label: label})
		}
	}

	return result
}

func (d *SQLDetector) matchLabel(line string) string {
	for _, p := range sqlPatterns {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return p.label(m)
		}
	}
	return ""
}
