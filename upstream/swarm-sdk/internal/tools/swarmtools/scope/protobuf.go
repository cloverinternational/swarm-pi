package scope

import "regexp"

// ProtobufDetector detects scope boundaries in Protocol Buffer files.
// Tracks: message, enum, service, rpc, oneof.
type ProtobufDetector struct{ *BraceDetector }

func init() {
	det := NewBraceDetector(BraceConfig{
		LangName:    "protobuf",
		CountBraces: CStyleBraceCounter,
		Patterns: []PatternRule{
			// message
			{
				Re:    regexp.MustCompile(`^\s*message\s+(\w+)`),
				Label: func(m []string) string { return "message " + m[1] },
			},
			// enum
			{
				Re:    regexp.MustCompile(`^\s*enum\s+(\w+)`),
				Label: func(m []string) string { return "enum " + m[1] },
			},
			// service
			{
				Re:    regexp.MustCompile(`^\s*service\s+(\w+)`),
				Label: func(m []string) string { return "service " + m[1] },
			},
			// rpc
			{
				Re:    regexp.MustCompile(`^\s*rpc\s+(\w+)`),
				Label: func(m []string) string { return "rpc " + m[1] },
			},
			// oneof
			{
				Re:    regexp.MustCompile(`^\s*oneof\s+(\w+)`),
				Label: func(m []string) string { return "oneof " + m[1] },
			},
		},
	})
	Register(det, ".proto")
}
