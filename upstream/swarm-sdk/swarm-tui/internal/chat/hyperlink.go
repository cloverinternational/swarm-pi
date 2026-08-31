package chat

import (
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Terminal hyperlinks (OSC 8).
//
// A hyperlink is an out-of-band escape wrapped around ordinary text:
//
//	ESC ] 8 ; params ; URI ST   text   ESC ] 8 ; ; ST
//
// The terminal paints only the text and makes it clickable. Because the
// sequence is invisible, adding links does not change any column measurement —
// proven by TestOSC8IsZeroWidth, which gates this whole feature.
//
// The `id=` parameter groups non-contiguous runs into ONE logical link. That is
// what makes a link survive line wrapping: each wrapped segment re-opens the
// link with the same id, so the terminal treats them as one target and
// highlights them together on hover. Without it a wrapped link either dies at
// the wrap or bleeds onto everything after it.

const (
	oscPrefix  = "\x1b]8;"
	stringTerm = "\x1b\\"
	// linkClose ends the current hyperlink. Emitting it is mandatory; a link
	// left open applies to every cell painted afterwards.
	linkClose = oscPrefix + ";" + stringTerm
)

// allowedLinkSchemes is deliberately small. The URI is embedded in an escape
// sequence that the terminal will act on, so this is a capability grant, not a
// formatting choice. http/https/mailto are inert without user intent, and file
// is needed to open sources. Everything else — javascript:, data:, and any
// custom scheme a terminal may have been configured to hand to a program — is
// refused so that model or tool output cannot smuggle one through.
var allowedLinkSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
	"file":   true,
}

// hostnameOnce caches the hostname used in file:// URLs. A file URL without a
// host is ambiguous over SSH: the terminal is local, the path is remote, and a
// naive handler would open the wrong file. Naming the host lets a terminal
// decide correctly (and ignore it when it matches).
var (
	hostnameOnce sync.Once
	cachedHost   string
)

func linkHostname() string {
	hostnameOnce.Do(func() {
		if h, err := os.Hostname(); err == nil {
			cachedHost = h
		}
	})
	return cachedHost
}

// safeLinkURL validates and normalises a URI for embedding in OSC 8.
// It returns ok=false when the URI must not become a link.
//
// Two independent checks, because they catch different attacks:
//
//  1. Scheme allowlist — refuses a target the terminal would treat as code.
//  2. Control-byte rejection — the URI sits INSIDE an escape sequence, so a
//     raw ESC, BEL or ST in the URI would terminate that sequence early and
//     let the remainder be interpreted as terminal commands. Everything
//     outside printable ASCII must already be percent-encoded.
func safeLinkURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", false
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if !allowedLinkSchemes[strings.ToLower(parsed.Scheme)] {
		return "", false
	}

	// Re-encode through net/url so any unusual byte is percent-escaped.
	encoded := parsed.String()
	for i := 0; i < len(encoded); i++ {
		if c := encoded[i]; c < 0x20 || c > 0x7e {
			// A control or non-ASCII byte survived encoding: refuse rather
			// than emit a sequence that could break out of the escape.
			return "", false
		}
	}
	return encoded, true
}

// linkID derives a stable id from the URI. Stability matters twice over: the
// same link keeps one identity across re-renders, and two segments of one
// wrapped link necessarily agree without having to thread state through the
// wrapper. Distinct URIs must not share an id or the terminal would treat them
// as one link, so the id is a hash of the URI itself.
func linkID(uri string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(uri))
	return strconv.FormatUint(h.Sum64(), 36)
}

// Hyperlink wraps text in an OSC 8 terminal hyperlink. If the URI is not
// acceptable the text is returned unchanged — a refused link degrades to plain
// text, never to a broken escape.
func Hyperlink(text, uri string) string {
	safe, ok := safeLinkURL(uri)
	if !ok || text == "" {
		return text
	}
	return openLink(safe) + text + linkClose
}

// openLink builds the opening half of a hyperlink for a pre-validated URI.
// The wrapper uses this directly to re-open a link on a continuation line.
func openLink(safeURI string) string {
	return oscPrefix + "id=" + linkID(safeURI) + ";" + safeURI + stringTerm
}

// FileURL builds a file:// URI for a source location. line <= 0 omits the
// fragment. The fragment is how a terminal is told which line to open at:
// kitty exposes it to open-actions.conf as $FRAGMENT, so a user can bind
// "open $EDITOR at $FRAGMENT" (see the README section on clickable links).
func FileURL(path string, line int) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	u := &url.URL{Scheme: "file", Host: linkHostname(), Path: abs}
	if line > 0 {
		u.Fragment = strconv.Itoa(line)
	}
	return u.String()
}

// ── detection ───────────────────────────────────────────────────────────────

// bareURLPattern matches a URL sitting in prose. The trailing class excludes
// characters that commonly end a sentence rather than a URL; trailing
// punctuation is trimmed separately so "see https://x.dev." does not swallow
// the full stop.
var bareURLPattern = regexp.MustCompile(`(?i)\b(?:https?://|mailto:)[^\s<>"'` + "`" + `\x00-\x1f]+`)

// markdownLinkPattern matches [label](target). The label is non-greedy and
// rejects nested brackets so "[a] [b](u)" does not match across both.
var markdownLinkPattern = regexp.MustCompile(`\[([^\]\n]*)\]\(([^)\s\n]+)\)`)

// pathLinePattern matches a source location such as "main.go:42" or
// "src/app.ts:42:7". It requires an extension so ordinary "word:number" prose
// ("note:5") is not mistaken for a file.
var pathLinePattern = regexp.MustCompile(`(?:^|[\s(\[])((?:[\w.~-]+/)*[\w.~-]+\.[A-Za-z0-9]+):(\d+)(?::(\d+))?`)

// trailingPunctuation is stripped from a detected bare URL. Closing brackets
// are only stripped when unbalanced, so a URL containing "(1)" survives.
const trailingPunctuation = `.,;:!?`

func trimURLPunctuation(u string) string {
	for len(u) > 0 {
		last := u[len(u)-1]
		if strings.IndexByte(trailingPunctuation, last) >= 0 {
			u = u[:len(u)-1]
			continue
		}
		if last == ')' && strings.Count(u, ")") > strings.Count(u, "(") {
			u = u[:len(u)-1]
			continue
		}
		break
	}
	return u
}

// LinkifyOptions controls which detectors run. Tool output is held to a
// stricter standard than assistant prose: see AllowLabels.
type LinkifyOptions struct {
	// AllowLabels permits [label](target), where the visible text differs from
	// the destination. That is fine for assistant prose but not for raw tool
	// output, where a web result could otherwise render "click for docs"
	// pointing somewhere else entirely. When false, such a construct is left
	// as literal text.
	AllowLabels bool
	// WorkspaceRoot resolves relative file paths. Empty disables path linking,
	// because a relative path with no root cannot be resolved unambiguously.
	WorkspaceRoot string
}

// linkSpan is one detected target: the byte range it occupies in the source
// text, the text to display, and where it points.
type linkSpan struct {
	start, end int
	label      string
	uri        string
}

// Linkify wraps every recognised target in text with an OSC 8 hyperlink.
// It must run BEFORE any styling that inserts escape sequences, so the
// detectors only ever see plain text.
//
// All detectors run against the ORIGINAL text and their results are merged in
// one pass. Running them in sequence over each other's output would let a later
// detector match inside an escape sequence an earlier one emitted — the bare
// URL pattern would find the target inside a markdown link's own OSC sequence
// and splice a second link into the middle of it.
func Linkify(text string, opts LinkifyOptions) string {
	if text == "" || strings.Contains(text, "\x1b") {
		// Already carries escapes: refuse rather than risk splicing a link
		// into the middle of an existing sequence.
		return text
	}

	var spans []linkSpan

	if opts.AllowLabels {
		for _, m := range markdownLinkPattern.FindAllStringSubmatchIndex(text, -1) {
			label := text[m[2]:m[3]]
			target := text[m[4]:m[5]]
			if label == "" {
				label = target
			}
			if _, ok := safeLinkURL(target); !ok {
				continue // leave the markdown as written
			}
			spans = append(spans, linkSpan{start: m[0], end: m[1], label: label, uri: target})
		}
	}

	for _, m := range bareURLPattern.FindAllStringIndex(text, -1) {
		raw := trimURLPunctuation(text[m[0]:m[1]])
		if raw == "" {
			continue
		}
		spans = append(spans, linkSpan{start: m[0], end: m[0] + len(raw), label: raw, uri: raw})
	}

	if opts.WorkspaceRoot != "" {
		for _, m := range pathLinePattern.FindAllStringSubmatchIndex(text, -1) {
			path := text[m[2]:m[3]]
			lineStr := text[m[4]:m[5]]
			line, err := strconv.Atoi(lineStr)
			if err != nil {
				continue
			}
			resolved := path
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(opts.WorkspaceRoot, path)
			}
			// Only link locations that exist: a link that opens nothing is
			// worse than plain text.
			if info, err := os.Stat(resolved); err != nil || info.IsDir() {
				continue
			}
			end := m[5]
			if m[6] >= 0 { // optional :column
				end = m[7]
			}
			spans = append(spans, linkSpan{
				start: m[2], end: end, label: text[m[2]:end], uri: FileURL(resolved, line),
			})
		}
	}

	if len(spans) == 0 {
		return text
	}

	// Earliest span wins; overlaps are discarded. A markdown link starts at its
	// '[' so it precedes the bare URL nested inside it and takes precedence.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})

	var b strings.Builder
	b.Grow(len(text) + len(spans)*96)
	cursor := 0
	for _, span := range spans {
		if span.start < cursor {
			continue // overlaps a span already emitted
		}
		b.WriteString(text[cursor:span.start])
		b.WriteString(Hyperlink(span.label, span.uri))
		cursor = span.end
	}
	b.WriteString(text[cursor:])
	return b.String()
}

// linkWorkspaceRootValue is the root used to resolve relative file paths when
// linkifying. It is package-level because applyInlineStyles is reached from
// many render paths that do not carry app state, and an empty value simply
// disables path links.
var linkWorkspaceRootValue atomic.Value

// SetLinkWorkspaceRoot records the directory that relative file paths in
// messages are resolved against.
func SetLinkWorkspaceRoot(root string) { linkWorkspaceRootValue.Store(root) }

func linkWorkspaceRoot() string {
	if v, ok := linkWorkspaceRootValue.Load().(string); ok {
		return v
	}
	return ""
}

// mapOutsideLinkPayloads applies fn to the parts of s that are not part of a
// hyperlink, leaving each complete link — opening sequence, label and closing
// sequence — untouched.
//
// Two things depend on this. First, the target: linkification runs before
// inline styling, so the styling regexes would otherwise see the URL sitting
// inside an OSC payload, and a target containing markdown-significant
// characters (an underscore, a pair of asterisks, a backtick — all legal in a
// URL) would have style escapes spliced into the middle of the escape sequence,
// breaking the link and spilling raw bytes onto the screen.
//
// Second, the label: for an auto-detected bare URL the label IS the target, so
// styling it would make the visible text differ from where the link goes. The
// guarantee that what you see is where you go survives only if the label is
// left exactly as detected.
func mapOutsideLinkPayloads(s string, fn func(string) string) string {
	if !strings.Contains(s, oscPrefix) {
		return fn(s)
	}

	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for {
		idx := strings.Index(s[i:], oscPrefix)
		if idx < 0 {
			b.WriteString(fn(s[i:]))
			return b.String()
		}
		start := i + idx
		b.WriteString(fn(s[i:start]))

		end, width := -1, 0
		for j := start + len(oscPrefix); j < len(s); j++ {
			if s[j] == '\x07' {
				end, width = j, 1
				break
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				end, width = j, 2
				break
			}
		}
		if end < 0 {
			// Unterminated: emit verbatim rather than risk mangling it.
			b.WriteString(s[start:])
			return b.String()
		}
		// Skip the whole link: opening sequence, label, and terminator.
		spanEnd := end + width
		if rest := s[spanEnd:]; strings.Contains(rest, linkClose) {
			spanEnd += strings.Index(rest, linkClose) + len(linkClose)
		}
		b.WriteString(s[start:spanEnd])
		i = spanEnd
	}
}
