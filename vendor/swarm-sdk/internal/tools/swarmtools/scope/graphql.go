package scope

import "regexp"

// GraphQLDetector detects scope boundaries in GraphQL schema and query files.
// Tracks: type, interface, enum, input, union, query, mutation, subscription, fragment.
type GraphQLDetector struct{ *BraceDetector }

func init() {
	det := NewBraceDetector(BraceConfig{
		LangName:    "graphql",
		CountBraces: CStyleBraceCounter,
		Patterns: []PatternRule{
			// type
			{
				Re:    regexp.MustCompile(`^\s*type\s+(\w+)`),
				Label: func(m []string) string { return "type " + m[1] },
			},
			// interface
			{
				Re:    regexp.MustCompile(`^\s*interface\s+(\w+)`),
				Label: func(m []string) string { return "interface " + m[1] },
			},
			// enum
			{
				Re:    regexp.MustCompile(`^\s*enum\s+(\w+)`),
				Label: func(m []string) string { return "enum " + m[1] },
			},
			// input
			{
				Re:    regexp.MustCompile(`^\s*input\s+(\w+)`),
				Label: func(m []string) string { return "input " + m[1] },
			},
			// union
			{
				Re:    regexp.MustCompile(`^\s*union\s+(\w+)`),
				Label: func(m []string) string { return "union " + m[1] },
			},
			// query / mutation / subscription
			{
				Re:    regexp.MustCompile(`^\s*query\s+(\w+)`),
				Label: func(m []string) string { return "query " + m[1] },
			},
			{
				Re:    regexp.MustCompile(`^\s*mutation\s+(\w+)`),
				Label: func(m []string) string { return "mutation " + m[1] },
			},
			{
				Re:    regexp.MustCompile(`^\s*subscription\s+(\w+)`),
				Label: func(m []string) string { return "subscription " + m[1] },
			},
			// fragment
			{
				Re:    regexp.MustCompile(`^\s*fragment\s+(\w+)`),
				Label: func(m []string) string { return "fragment " + m[1] },
			},
			// extend
			{
				Re:    regexp.MustCompile(`^\s*extend\s+type\s+(\w+)`),
				Label: func(m []string) string { return "extend " + m[1] },
			},
		},
	})
	Register(det, ".graphql", ".gql")
}
