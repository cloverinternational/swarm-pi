package history

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// This file makes HistorySearch "read" conversations the way a human would:
// it strips the machine noise that dominates raw first-message previews
// (system-reminder blocks, task-nudge boilerplate, the naming-agent
// "JSON only. Message: ..." wrapper), derives a human title when none was
// persisted, and matches a query as a set of terms ranked by relevance rather
// than a single dumb substring over noisy text.

var (
	// Paired <system-reminder ...>...</system-reminder> blocks (dotall).
	systemReminderBlockRe = regexp.MustCompile(`(?is)<system-reminder[^>]*>.*?</system-reminder>`)
	// Any stray/unclosed system-reminder tag left after truncation.
	systemReminderTagRe = regexp.MustCompile(`(?is)</?system-reminder[^>]*>`)
	runtimeGuidanceRe   = regexp.MustCompile(`(?is)<swarm_runtime_guidance[^>]*>.*?</swarm_runtime_guidance>`)
	globalContextRe     = regexp.MustCompile(`(?is)<(?:globalclaudemd|mcp_context)[^>]*>.*?</(?:globalclaudemd|mcp_context)>`)
	// Injected runtime/skills/context scaffolding wrappers (paired, dotall).
	// These leak font/skill boilerplate into conversation titles when the model
	// summarizes the first user message, so they must be stripped wholesale.
	availableSkillsRe       = regexp.MustCompile(`(?is)<available_skills[^>]*>.*?</available_skills>`)
	runtimeSkillsRe         = regexp.MustCompile(`(?is)<swarm_runtime_skills[^>]*>.*?</swarm_runtime_skills>`)
	runtimeCapabilitiesRe   = regexp.MustCompile(`(?is)<swarm_runtime_capabilities[^>]*>.*?</swarm_runtime_capabilities>`)
	effectiveCapabilitiesRe = regexp.MustCompile(`(?is)<effective_capabilities[^>]*>.*?</effective_capabilities>`)
	namedContextRe          = regexp.MustCompile(`(?is)<context\s+name="[^"]*"[^>]*>.*?</context>`)
	envBlockRe              = regexp.MustCompile(`(?is)<env[^>]*>.*?</env>`)
	// Standalone reminder lines that carry no search signal.
	skillReminderLineRe = regexp.MustCompile(`(?im)^\s*\[SKILL REMINDER\].*$`)
	taskNudgeLineRe     = regexp.MustCompile(`(?im)^\s*\[Task Nudge\].*$`)
	// The naming-agent envelope: `JSON only. Message: "<text>" Output: {...}`.
	// Captures the inner user text, tolerating a trailing (possibly truncated) tail.
	namingEnvelopeRe = regexp.MustCompile(`(?is)^\s*json only\.?\s*message:\s*"?(.*?)"?\s*(?:\\n)?\s*output:.*$`)
	// Task-nudge / task-maintenance boilerplate that carries no search signal.
	taskNudgeRe  = regexp.MustCompile(`(?is)\[task (?:nudge|maintenance reminder)\].*$`)
	trackWorkRe  = regexp.MustCompile(`(?is)\*\*track your work with tasks:\*\*.*$`)
	whitespaceRe = regexp.MustCompile(`\s+`)
)

// truncationPrefixes are opening tags of injected scaffolding. If any of these
// appears with NO matching closing tag (the message was truncated mid-block),
// everything from the opening tag to end-of-string is machine noise and must be
// stripped so leaked skill/font text can never survive into a title.
var truncationPrefixes = []struct {
	open  string
	close string
}{
	{open: "<swarm_runtime_guidance", close: "</swarm_runtime_guidance>"},
	{open: "<available_skills", close: "</available_skills>"},
	{open: "<swarm_runtime_capabilities", close: "</swarm_runtime_capabilities>"},
	{open: "<swarm_runtime_skills", close: "</swarm_runtime_skills>"},
	{open: "<effective_capabilities", close: "</effective_capabilities>"},
	{open: "<context name=", close: "</context>"},
	{open: "<system-reminder", close: "</system-reminder>"},
}

// stripTruncatedScaffolding removes any injected scaffolding block whose opening
// tag is present but whose closing tag is absent (truncated message). For each
// such prefix it truncates from the first opening-tag occurrence to end-of-string.
func stripTruncatedScaffolding(s string) string {
	lower := strings.ToLower(s)
	cut := len(s)
	for _, p := range truncationPrefixes {
		idx := strings.Index(lower, p.open)
		if idx < 0 {
			continue
		}
		// A matching close after the open means the block was already handled
		// by the paired regexes above; only truncate when no close exists.
		if strings.Contains(lower[idx:], strings.ToLower(p.close)) {
			continue
		}
		if idx < cut {
			cut = idx
		}
	}
	if cut < len(s) {
		return s[:cut]
	}
	return s
}

// CleanText removes conversation machine-noise and normalises whitespace,
// preserving the original human-authored intent for both display and matching.
// It is exported so the summary source (client/shim) can clean the FULL message
// content before truncating to a preview — otherwise a long leading
// system-reminder fills the whole preview window and the real prompt is lost.
func CleanText(s string) string {
	if s == "" {
		return ""
	}
	out := s
	// Some conversations store the first message with JSON/unicode-escaped
	// punctuation (e.g. "\u003csystem-reminder\u003e"). Decode the handful of
	// escapes that matter so the noise-stripping regexes below can see real tags.
	out = unescapePunctuation(out)
	// Unwrap possibly-nested naming-agent envelopes — the inner text is the
	// real user prompt (naming-agent conversations double-wrap on retries).
	for i := 0; i < 3; i++ {
		unwrapped, changed := unwrapNamingEnvelope(out)
		if !changed {
			break
		}
		out = unwrapped
	}
	out = stripRuntimeScaffolding(out)
	out = strings.ReplaceAll(out, "\\n", " ")
	out = whitespaceRe.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// stripRuntimeScaffolding removes the injected wrappers while preserving the
// surrounding human-authored text and its line boundaries. Keeping this as the
// shared primitive prevents exclude_runtime and HistoryGet's CleanText path
// from developing separate marker/block rules.
func stripRuntimeScaffolding(out string) string {
	out = systemReminderBlockRe.ReplaceAllString(out, " ")
	out = runtimeGuidanceRe.ReplaceAllString(out, " ")
	out = globalContextRe.ReplaceAllString(out, " ")
	out = availableSkillsRe.ReplaceAllString(out, " ")
	out = runtimeSkillsRe.ReplaceAllString(out, " ")
	out = runtimeCapabilitiesRe.ReplaceAllString(out, " ")
	out = effectiveCapabilitiesRe.ReplaceAllString(out, " ")
	out = namedContextRe.ReplaceAllString(out, " ")
	out = envBlockRe.ReplaceAllString(out, " ")
	// After stripping paired blocks, any surviving OPENING scaffolding tag means
	// the message was truncated mid-block: strip from that tag to end-of-string.
	out = stripTruncatedScaffolding(out)
	out = systemReminderTagRe.ReplaceAllString(out, " ")
	out = skillReminderLineRe.ReplaceAllString(out, " ")
	out = taskNudgeLineRe.ReplaceAllString(out, " ")
	out = taskNudgeRe.ReplaceAllString(out, " ")
	out = trackWorkRe.ReplaceAllString(out, " ")
	return out
}

// FirstSubstantiveUserText returns the first human-authored user request,
// skipping compaction-resume envelopes and injected runtime state. A reminder
// block followed by a real prompt in the same message is preserved by CleanText.
func FirstSubstantiveUserText(messages []*conversation.Message) string {
	for _, message := range messages {
		if message == nil || message.Role != conversation.RoleUser {
			continue
		}
		cleaned := CleanText(message.Content)
		if isSubstantiveUserText(cleaned) {
			return cleaned
		}
	}
	return ""
}

func isSubstantiveUserText(cleaned string) bool {
	lower := strings.ToLower(strings.TrimSpace(cleaned))
	if lower == "" || lower == "continue" {
		return false
	}
	if strings.Contains(lower, "continue") && len(strings.Fields(lower)) <= 6 {
		return false
	}
	for _, prefix := range runtimeMarkerPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

// runtimeMarkerPrefixes is the SINGLE source of truth for "this text was
// injected by the runtime, not typed by a human". It is shared by
// isSubstantiveUserText (title/preview derivation, used by HistoryGet's
// human_only path via CleanText) and by excludeRuntimeText (HistorySearch's
// exclude_runtime post-filter) so the two can never drift apart.
// All entries MUST be lowercase; callers lowercase the haystack first.
var runtimeMarkerPrefixes = []string{
	"this session is being continued from a previous conversation",
	"## mcp context",
	"## current tasks",
	"**current mode**:",
	"**current mode**",
	"please continue the conversation from where we left off",
	"continue from where",
	"<system-reminder",
	"[scheduled]",
	"[task nudge]",
	"[skill reminder]",
}

// isRuntimeInjectedLine reports whether one line of stored conversation text is
// runtime-injected scaffolding rather than human/assistant authored prose. It
// reuses CleanText (which erases every paired scaffolding block) plus the shared
// runtimeMarkerPrefixes list — no second, divergent marker table exists.
func isRuntimeInjectedLine(line string) bool {
	cleaned := strings.TrimSpace(line)
	return cleaned != "" && !isSubstantiveUserText(cleaned)
}

// excludeRuntimeText drops runtime-injected lines from a body haystack.
// Bounded: it scans at most maxRuntimeFilterLines lines and stops appending
// once the retained text reaches maxFieldScanBytes, so a 1.58 GB conversation
// can never be fully materialised into a second string.
func excludeRuntimeText(body string) string {
	if body == "" {
		return ""
	}
	// Remove paired and truncated runtime blocks before scanning lines. Filtering
	// only their opening/closing tag lines would accidentally retain the interior
	// of a multi-line <system-reminder> block as apparently human text.
	body = stripRuntimeScaffolding(unescapePunctuation(body))
	var out strings.Builder
	lines := 0
	for len(body) > 0 && lines < maxRuntimeFilterLines && out.Len() < maxFieldScanBytes {
		line := body
		if idx := strings.IndexByte(body, '\n'); idx >= 0 {
			line, body = body[:idx], body[idx+1:]
		} else {
			body = ""
		}
		lines++
		if line == "" || isRuntimeInjectedLine(line) {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// escapeReplacer decodes the small set of unicode/JSON escapes that appear in
// stored message previews and would otherwise hide machine-noise markers.
var escapeReplacer = strings.NewReplacer(
	`\u003c`, "<", `\u003C`, "<",
	`\u003e`, ">", `\u003E`, ">",
	`\u0026`, "&", `\u0026`, "&",
	`\u0027`, "'", `\u0022`, `"`,
	`\"`, `"`,
)

func unescapePunctuation(s string) string {
	if !strings.Contains(s, `\u`) && !strings.Contains(s, `\"`) {
		return s
	}
	return escapeReplacer.Replace(s)
}

// cleanText is the internal alias retained for readability at call sites.
func cleanText(s string) string { return CleanText(s) }

// unwrapNamingEnvelope peels one `JSON only. Message: "<text>" Output: {...}`
// layer, tolerating truncation (missing Output marker / closing quote).
func unwrapNamingEnvelope(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(trimmed), "json only") {
		return s, false
	}
	if m := namingEnvelopeRe.FindStringSubmatch(trimmed); m != nil && strings.TrimSpace(m[1]) != "" {
		return m[1], true
	}
	idx := strings.Index(strings.ToLower(trimmed), "message:")
	if idx < 0 {
		return s, false
	}
	rest := strings.TrimSpace(trimmed[idx+len("message:"):])
	rest = strings.TrimPrefix(rest, `"`)
	rest = strings.TrimSuffix(rest, `"`)
	if strings.TrimSpace(rest) == "" || rest == trimmed {
		return s, false
	}
	return rest, true
}

// deriveTitle produces a short, human-readable title from cleaned preview text
// when a conversation never had a title persisted.
func deriveTitle(cleanedPreview string) string {
	if cleanedPreview == "" {
		return "(untitled conversation)"
	}
	title := cleanedPreview
	// Prefer the first sentence/line if it is reasonably short.
	if idx := strings.IndexAny(title, ".!?\n"); idx > 0 && idx < 80 {
		title = title[:idx]
	}
	return truncateRunes(strings.TrimSpace(title), 80)
}

// DeriveTitle returns a stable display title for a substantive prompt.
func DeriveTitle(text string) string { return deriveTitle(CleanText(text)) }

// truncateRunes trims s to at most n runes on a word boundary, appending an ellipsis.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	cut := string(runes[:n])
	if sp := strings.LastIndex(cut, " "); sp > n/2 {
		cut = cut[:sp]
	}
	return strings.TrimSpace(cut) + "…"
}

// prepareSummary returns a display-cleaned copy of item plus the lowercased
// title/body haystacks used for matching. The original (pre-derivation) title is
// used for match weighting so a derived title doesn't inflate relevance.
func prepareSummary(item Summary, searchBody ...bool) (out Summary, titleHaystack, bodyHaystack string) {
	cleanTitle := CleanText(boundField(item.Title))
	cleanPreview := CleanText(boundField(item.Preview))
	out = item
	out.Preview = cleanPreview
	if cleanTitle != "" && !isGenericHistoryTitle(cleanTitle) {
		out.Title = cleanTitle
	} else {
		out.Title = deriveTitle(cleanPreview)
	}
	body := cleanPreview
	if len(searchBody) > 0 && searchBody[0] && item.Body != "" {
		body += " " + CleanText(boundField(item.Body))
	}
	body = boundField(body)
	return out, strings.ToLower(cleanTitle), strings.ToLower(body)
}

func isGenericHistoryTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	return lower == "" ||
		lower == "(untitled conversation)" ||
		strings.HasPrefix(lower, "continue from where") ||
		strings.HasPrefix(lower, "please continue the conversation")
}

// parseQueryTerms splits a query into distinct lowercased search terms.
func parseQueryTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		terms = append(terms, f)
	}
	return terms
}

// scoreSummary implements AND-of-terms matching with title weighting. Every term
// must appear in the title or body (precise), and title hits are weighted higher
// so the most on-topic conversations rank first. An empty term set matches all
// (recent-listing mode) with a neutral score.
func scoreSummary(titleHaystack, bodyHaystack string, terms []string) (int, bool) {
	if len(terms) == 0 {
		return 0, true
	}
	score := 0
	for _, term := range terms {
		switch {
		case titleHaystack != "" && strings.Contains(titleHaystack, term):
			score += 3
		case strings.Contains(bodyHaystack, term):
			score++
		default:
			return 0, false
		}
	}
	return score, true
}

// ---------------------------------------------------------------------------
// Post-filter: regex / case / field targeting / snippets / runtime exclusion.
//
// Everything below runs INSIDE this package on the candidate summaries returned
// by the search backend. SQLite FTS5 cannot evaluate RE2, so regex can only be
// applied after candidates come back. That makes over-fetching mandatory (see
// candidateFetchLimit) and makes bounding mandatory (a single conversation body
// can be >1 GB).
// ---------------------------------------------------------------------------

const (
	// maxFieldScanBytes caps how much of any single field we will regex/scan.
	// RE2 is linear in input size, but 1.58 GB * N candidates is still fatal;
	// 1 MiB of cleaned conversation text is far past where relevance decays.
	maxFieldScanBytes = 1 << 20
	// maxRuntimeFilterLines caps line-splitting work in excludeRuntimeText.
	maxRuntimeFilterLines = 20000
	// Snippet bounds. defaultSnippetContext is bytes of context on EACH side of
	// the match; the emitted snippet is hard-capped at maxSnippetChars runes.
	defaultSnippetContext = 60
	maxSnippetContext     = 200
	maxSnippetChars       = 320
	defaultMaxSnippets    = 3
	maxSnippetsPerResult  = 10
	// Over-fetch: a post-filter discards candidates, so asking the backend for
	// exactly `limit` rows would under-fill the result set. Ask for
	// limit*candidateFetchMultiplier, clamped to candidateFetchCap so a broad
	// regex over an 8 GB corpus cannot drag an unbounded pool into memory.
	candidateFetchMultiplier = 10
	candidateFetchCap        = 500
)

// matchField identifies which part of a summary a match came from.
type matchField string

const (
	fieldTitle   matchField = "title"
	fieldPreview matchField = "preview"
	fieldBody    matchField = "body"
)

var allMatchFields = []matchField{fieldTitle, fieldPreview, fieldBody}

// matchOptions is the resolved, validated post-filter configuration. Its ZERO
// VALUE means "no post-filter": active() reports false and the caller keeps the
// exact legacy scoreSummary path.
type matchOptions struct {
	Regex          *regexp.Regexp // nil when no regex was requested
	RegexSource    string
	CaseSensitive  bool
	Fields         []matchField // nil/empty means all fields (legacy behavior)
	Snippets       bool
	SnippetContext int
	MaxSnippets    int
	ExcludeRuntime bool
}

// active reports whether any post-filter capability was requested. When false
// the caller MUST take the untouched legacy path so existing behavior, scoring
// and ordering are bit-for-bit preserved.
func (o matchOptions) active() bool {
	return o.Regex != nil || o.CaseSensitive || len(o.Fields) > 0 || o.Snippets || o.ExcludeRuntime
}

// filterActive reports whether matching itself can discard a backend candidate.
// Snippets are presentation-only and must preserve backend SearchMatched results.
func (o matchOptions) filterActive() bool {
	return o.Regex != nil || o.CaseSensitive || len(o.Fields) > 0 || o.ExcludeRuntime
}

// compileSearchRegex compiles pattern as RE2, prefixing (?i) for the
// case-insensitive default rather than lowercasing the haystack (lowercasing
// breaks character classes like \p{Lu} and [A-Z]). The pattern is passed
// verbatim to regexp.Compile — no untrusted text is ever interpolated into it.
// An invalid pattern returns a clear, actionable error instead of panicking.
func compileSearchRegex(pattern string, caseSensitive bool) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	expr := pattern
	if !caseSensitive {
		expr = "(?i)" + expr
	}
	compiled, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid regex %q: %w (Go RE2 syntax; lookahead/backreferences are unsupported — use a plain query instead)", pattern, err)
	}
	return compiled, nil
}

// parseMatchFields validates the requested field names. An empty request means
// all fields, matching today's behavior.
func parseMatchFields(names []string) ([]matchField, error) {
	if len(names) == 0 {
		return nil, nil
	}
	seen := make(map[matchField]struct{}, len(names))
	fields := make([]matchField, 0, len(names))
	for _, raw := range names {
		name := matchField(strings.ToLower(strings.TrimSpace(raw)))
		switch name {
		case fieldTitle, fieldPreview, fieldBody:
		default:
			return nil, fmt.Errorf("fields must contain only title, preview or body (got %q)", raw)
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		fields = append(fields, name)
	}
	return fields, nil
}

// selected reports whether field participates in matching.
func (o matchOptions) selected(field matchField) bool {
	if len(o.Fields) == 0 {
		return true
	}
	for _, candidate := range o.Fields {
		if candidate == field {
			return true
		}
	}
	return false
}

// candidateFetchLimit returns how many rows to request from the backend so the
// post-filter can still fill `limit` results after discarding non-matches.
func candidateFetchLimit(limit int, opts matchOptions) int {
	if !opts.filterActive() || limit <= 0 {
		return limit
	}
	fetch := limit * candidateFetchMultiplier
	if fetch > candidateFetchCap {
		fetch = candidateFetchCap
	}
	if fetch < limit {
		fetch = limit
	}
	return fetch
}

// Snippet is one bounded piece of matching text plus the field it came from.
type Snippet struct {
	Field string `json:"field"`
	Text  string `json:"text"`
}

// boundField trims a field to at most maxFieldScanBytes on a rune boundary so
// regex work and snippet slicing stay bounded on multi-hundred-MB bodies.
func boundField(s string) string {
	if len(s) <= maxFieldScanBytes {
		return s
	}
	cut := maxFieldScanBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// expandSnippet returns text[start:end] widened by `context` bytes on each side
// and snapped OUTWARD to UTF-8 rune boundaries, so a snippet can never split a
// multi-byte rune (CJK, emoji). The result is then capped at maxSnippetChars
// runes, again on rune boundaries, with ellipsis markers when clipped.
func expandSnippet(text string, start, end, context int) string {
	if start < 0 || end > len(text) || start > end {
		return ""
	}
	left := start - context
	if left < 0 {
		left = 0
	}
	for left > 0 && !utf8.RuneStart(text[left]) {
		left--
	}
	right := end + context
	if right > len(text) {
		right = len(text)
	}
	for right < len(text) && !utf8.RuneStart(text[right]) {
		right++
	}
	snippet := strings.TrimSpace(text[left:right])
	// Hard rune cap: keep the head of the window (which contains the match,
	// since context is bounded well below maxSnippetChars).
	if utf8.RuneCountInString(snippet) > maxSnippetChars {
		count := 0
		cut := len(snippet)
		for byteIndex := range snippet {
			if count == maxSnippetChars {
				cut = byteIndex
				break
			}
			count++
		}
		snippet = strings.TrimSpace(snippet[:cut]) + "…"
	}
	if left > 0 {
		snippet = "…" + snippet
	}
	if right < len(text) {
		snippet += "…"
	}
	return runeLimitedSnippet(snippet, maxSnippetChars)
}

// runeLimitedSnippet enforces the final output cap after adding clipping
// ellipses. It walks rune boundaries directly and therefore cannot split UTF-8.
func runeLimitedSnippet(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	count := 0
	for byteIndex := range text {
		if count == limit {
			return text[:byteIndex]
		}
		count++
	}
	return text
}

// findMatchRanges locates matches of the active matcher in text, returning at
// most `max` byte ranges. Plain terms match the first occurrence of each term.
func findMatchRanges(text string, opts matchOptions, terms []string, max int) [][]int {
	if text == "" || max <= 0 {
		return nil
	}
	if opts.Regex != nil {
		return opts.Regex.FindAllStringIndex(text, max)
	}
	ranges := make([][]int, 0, max)
	for _, term := range terms {
		if len(ranges) >= max {
			break
		}
		if opts.CaseSensitive {
			if idx := strings.Index(text, term); idx >= 0 {
				ranges = append(ranges, []int{idx, idx + len(term)})
			}
			continue
		}
		// QuoteMeta makes the plain term literal; (?i) delegates Unicode case
		// folding and original-string byte offsets to RE2 instead of indexing a
		// lowercased copy whose byte lengths may differ from the source.
		matcher := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(term))
		if loc := matcher.FindStringIndex(text); loc != nil {
			ranges = append(ranges, loc)
		}
	}
	return ranges
}

// parseTermsWithCase splits a query into distinct terms, preserving case when
// caseSensitive is set. With caseSensitive=false it is identical to
// parseQueryTerms, so the legacy path is unchanged.
func parseTermsWithCase(query string, caseSensitive bool) []string {
	if !caseSensitive {
		return parseQueryTerms(query)
	}
	fields := strings.Fields(query)
	seen := make(map[string]struct{}, len(fields))
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		terms = append(terms, f)
	}
	return terms
}

// fieldTexts returns the per-field haystacks for a summary under opts, already
// cleaned, runtime-filtered (when requested) and length-bounded.
func fieldTexts(item Summary, cleaned Summary, opts matchOptions, searchBody bool) map[matchField]string {
	texts := make(map[matchField]string, 3)
	if opts.selected(fieldTitle) {
		texts[fieldTitle] = CleanText(boundField(item.Title))
	}
	if opts.selected(fieldPreview) {
		texts[fieldPreview] = boundField(cleaned.Preview)
	}
	if opts.selected(fieldBody) && searchBody && item.Body != "" {
		body := boundField(item.Body)
		if opts.ExcludeRuntime {
			body = excludeRuntimeText(body)
		}
		texts[fieldBody] = boundField(CleanText(body))
	}
	return texts
}

// postFilterMatch evaluates the extended matcher against one candidate.
// It returns whether the candidate matches, a relevance score using the SAME
// title=3 / body=1 weighting as scoreSummary, and bounded snippets.
//
// Semantics (documented in the tool schema): `query` narrows candidates via the
// backend and the normal term matcher; `regex` then FILTERS those candidates.
// Both must be satisfied when both are supplied.
func postFilterMatch(item Summary, cleaned Summary, opts matchOptions, terms []string, searchBody bool) (score int, ok bool, snippets []Snippet) {
	texts := fieldTexts(item, cleaned, opts, searchBody)
	foldedTexts := texts
	if !opts.CaseSensitive && len(terms) > 0 {
		foldedTexts = make(map[matchField]string, len(texts))
		for field, text := range texts {
			foldedTexts[field] = strings.ToLower(text)
		}
	}
	weight := func(field matchField) int {
		if field == fieldTitle {
			return 3
		}
		return 1
	}

	// 1. Plain terms: AND semantics, every term must appear in some selected field.
	if len(terms) > 0 {
		for _, term := range terms {
			hit := false
			best := 0
			for _, field := range allMatchFields {
				text, present := texts[field]
				if !present || text == "" {
					continue
				}
				haystack := foldedTexts[field]
				needle := term
				if !opts.CaseSensitive {
					needle = strings.ToLower(term)
				}
				if strings.Contains(haystack, needle) {
					hit = true
					if w := weight(field); w > best {
						best = w
					}
				}
			}
			if !hit {
				return 0, false, nil
			}
			score += best
		}
	}

	// 2. Regex filter: must match at least one selected field.
	if opts.Regex != nil {
		regexHit := false
		for _, field := range allMatchFields {
			text, present := texts[field]
			if !present || text == "" {
				continue
			}
			if loc := opts.Regex.FindStringIndex(text); loc != nil {
				regexHit = true
				score += weight(field)
			}
		}
		if !regexHit {
			return 0, false, nil
		}
	}

	// A no-criteria post-filter (e.g. exclude_runtime alone) matches everything,
	// exactly like an empty term set does on the legacy path.
	if len(terms) == 0 && opts.Regex == nil {
		score = 0
	}

	if opts.Snippets {
		snippets = collectSnippets(texts, opts, terms)
	}
	return score, true, snippets
}

// collectSnippets emits at most opts.MaxSnippets bounded snippets across the
// selected fields, in title → preview → body order.
func collectSnippets(texts map[matchField]string, opts matchOptions, terms []string) []Snippet {
	limit := opts.MaxSnippets
	if limit <= 0 {
		limit = defaultMaxSnippets
	}
	if limit > maxSnippetsPerResult {
		limit = maxSnippetsPerResult
	}
	context := opts.SnippetContext
	if context <= 0 {
		context = defaultSnippetContext
	}
	if context > maxSnippetContext {
		context = maxSnippetContext
	}
	out := make([]Snippet, 0, limit)
	for _, field := range allMatchFields {
		if len(out) >= limit {
			break
		}
		text, present := texts[field]
		if !present || text == "" {
			continue
		}
		for _, loc := range findMatchRanges(text, opts, terms, limit-len(out)) {
			if len(out) >= limit {
				break
			}
			snippet := expandSnippet(text, loc[0], loc[1], context)
			if snippet == "" {
				continue
			}
			out = append(out, Snippet{Field: string(field), Text: snippet})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// segmentOptions is deliberately separate from matchOptions: active() is false
// only when every newly introduced request field has its zero value, which is
// the compatibility gate for the byte-identical legacy path.
type segmentOptions struct {
	ToolName        string
	ToolOutcome     string
	SegmentKind     string
	Stats           bool
	NGram           int
	TopTerms        int
	RequestNGram    int
	RequestTopTerms int
	TopTermsCapped  bool
	Requested       bool
}

func (o segmentOptions) active() bool {
	return o.Requested
}

func parseSegmentOptions(params map[string]any) (segmentOptions, error) {
	var opts segmentOptions
	if value, ok := params["tool_name"]; ok && value != nil {
		name, valid := value.(string)
		if !valid {
			return opts, fmt.Errorf("tool_name must be a string")
		}
		opts.ToolName = strings.TrimSpace(name)
		opts.Requested = opts.ToolName != ""
	}
	if value, ok := params["tool_outcome"]; ok && value != nil {
		opts.Requested = true
		outcome, valid := value.(string)
		if !valid {
			return opts, fmt.Errorf("tool_outcome must be one of any, failed, succeeded")
		}
		switch outcome {
		case "any":
			// The index contract represents "any" as the zero value.
		case searchindex.OutcomeFailed, searchindex.OutcomeSucceeded:
			opts.ToolOutcome = outcome
		default:
			return opts, fmt.Errorf("tool_outcome must be one of any, failed, succeeded (got %q)", outcome)
		}
	}
	if value, ok := params["segment_kind"]; ok && value != nil {
		opts.Requested = true
		kind, valid := value.(string)
		if !valid {
			return opts, fmt.Errorf("segment_kind must be one of message, tool_call, tool_result")
		}
		switch kind {
		case searchindex.SegmentMessage, searchindex.SegmentToolCall, searchindex.SegmentToolResult:
			opts.SegmentKind = kind
		default:
			return opts, fmt.Errorf("segment_kind must be one of message, tool_call, tool_result (got %q)", kind)
		}
	}
	stats, err := boolParam(params, "stats", false)
	if err != nil {
		return opts, err
	}
	opts.Stats = stats
	if opts.Stats {
		opts.Requested = true
	}

	if value, ok := params["ngram"]; ok && value != nil {
		opts.Requested = true
		ngram, parseErr := integerParam(value, "ngram")
		if parseErr != nil || ngram < 1 || ngram > searchindex.MaxStatsNGram {
			return opts, fmt.Errorf("ngram must be an integer between 1 and %d; use 1 for vocabulary or 2-3 for phrases", searchindex.MaxStatsNGram)
		}
		opts.RequestNGram = ngram
		opts.NGram = ngram
	}
	if opts.NGram == 0 {
		opts.NGram = 1
	}

	if value, ok := params["top_terms"]; ok && value != nil {
		opts.Requested = true
		top, parseErr := integerParam(value, "top_terms")
		if parseErr != nil || top < 1 {
			return opts, fmt.Errorf("top_terms must be a positive integer; values above %d are capped", searchindex.MaxStatsTop)
		}
		opts.RequestTopTerms = top
		opts.TopTerms = top
		if opts.TopTerms > searchindex.MaxStatsTop {
			opts.TopTerms = searchindex.MaxStatsTop
			opts.TopTermsCapped = true
		}
	}
	if opts.TopTerms == 0 {
		opts.TopTerms = searchindex.DefaultStatsTop
	}
	if !opts.Stats && (opts.RequestNGram != 0 || opts.RequestTopTerms != 0) {
		return opts, fmt.Errorf("ngram and top_terms are only meaningful with stats=true")
	}
	return opts, nil
}

func integerParam(value any, name string) (int, error) {
	return boundedIntParam(map[string]any{name: value}, name, 1, int(^uint(0)>>1))
}

func (t *SearchTool) executeSegmentSearch(
	ctx context.Context,
	request SearchRequest,
	segmentOpts segmentOptions,
	matchOpts matchOptions,
	selectedWorkspace string,
	scope string,
) (*tools.ToolResult, error) {
	filter := searchindex.SegmentFilter{
		WorkspacePath:  selectedWorkspace,
		ToolName:       segmentOpts.ToolName,
		Kind:           segmentOpts.SegmentKind,
		Outcome:        segmentOpts.ToolOutcome,
		ExcludeRuntime: request.ExcludeRuntime,
	}
	if segmentOpts.Stats {
		return t.executeSegmentStats(ctx, filter, segmentOpts, scope)
	}

	hitLimit := request.Limit * candidateFetchMultiplier
	if hitLimit > candidateFetchCap {
		hitLimit = candidateFetchCap
	}
	if hitLimit < searchindex.DefaultSegmentLimit {
		hitLimit = searchindex.DefaultSegmentLimit
	}
	hits, err := t.segments.SearchSegments(ctx, searchindex.SegmentQuery{
		Filter: filter,
		Text:   strings.TrimSpace(request.Query),
		Limit:  hitLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: search segments: %w", err)
	}
	hits = filterSegmentHits(hits, request.Query, matchOpts)

	// Ask the existing backend for metadata. The six request fields let a
	// segment-aware adapter pre-narrow efficiently; this package still treats the
	// segment hits above as authoritative and joins by conversation ID.
	resultLimit := request.Limit
	request.Limit = candidateFetchCap
	var summaries []Summary
	if t.searchV2 != nil {
		summaries, err = t.searchV2(ctx, request)
	} else {
		summaries, err = t.search(ctx, request.Query, selectedWorkspace)
	}
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: %w", err)
	}
	byID := make(map[string]Summary, len(summaries))
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}

	type segmentMatch struct {
		summary Summary
		hit     searchindex.SegmentHit
	}
	matches := make([]segmentMatch, 0, resultLimit)
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if _, duplicate := seen[hit.ConversationID]; duplicate {
			continue
		}
		if selectedWorkspace != "" && !workspaceMatches(selectedWorkspace, hit.WorkspacePath) {
			continue
		}
		seen[hit.ConversationID] = struct{}{}
		summary, found := byID[hit.ConversationID]
		if !found {
			summary = Summary{
				ID:            hit.ConversationID,
				Title:         deriveTitle(CleanText(hit.Text)),
				Preview:       truncateRunes(CleanText(hit.Text), maxSnippetChars),
				WorkspacePath: hit.WorkspacePath,
				UpdatedAt:     hit.Timestamp,
			}
		}
		if summary.MessageCount < request.MinMessages ||
			(request.Origin != "" && summary.Origin != request.Origin) {
			continue
		}
		summary, _, _ = prepareSummary(summary, request.SearchBody)
		matches = append(matches, segmentMatch{summary: summary, hit: hit})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		cmp := 0
		switch request.Sort {
		case "message_count":
			cmp = compareInt(matches[i].summary.MessageCount, matches[j].summary.MessageCount)
		case "recency":
			cmp = compareTime(matches[i].summary.UpdatedAt, matches[j].summary.UpdatedAt)
		default:
			cmp = compareFloat(matches[i].hit.Score, matches[j].hit.Score)
		}
		if cmp == 0 {
			cmp = compareTime(matches[i].summary.UpdatedAt, matches[j].summary.UpdatedAt)
		}
		if cmp == 0 {
			cmp = strings.Compare(matches[i].summary.ID, matches[j].summary.ID)
		}
		if request.Order == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})
	truncated := len(matches) > resultLimit
	if truncated {
		matches = matches[:resultLimit]
	}
	results := make([]map[string]any, len(matches))
	for i, match := range matches {
		results[i] = segmentSummaryRow(match.summary, match.hit, scope)
	}
	out := map[string]any{
		"scope":          scope,
		"query":          reportedSearchQuery(request.Query, request.CaseSensitive),
		"sort":           request.Sort,
		"order":          request.Order,
		"segment_filter": segmentFilterOutput(filter),
		"results":        results,
	}
	if selectedWorkspace != "" {
		out["workspace_path"] = selectedWorkspace
	}
	if truncated {
		out["truncated"] = true
	}
	return jsonResult(out)
}

func (t *SearchTool) executeSegmentStats(
	ctx context.Context,
	filter searchindex.SegmentFilter,
	opts segmentOptions,
	scope string,
) (*tools.ToolResult, error) {
	count, err := t.segments.SegmentCount(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: count segments: %w", err)
	}
	stats, err := t.segments.TermStats(ctx, filter, searchindex.StatsOptions{
		NGram: opts.NGram,
		Top:   opts.TopTerms,
	})
	if err != nil {
		return nil, fmt.Errorf("HistorySearch: term stats: %w", err)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Occurrences != stats[j].Occurrences {
			return stats[i].Occurrences > stats[j].Occurrences
		}
		return stats[i].Term < stats[j].Term
	})
	if len(stats) > opts.TopTerms {
		stats = stats[:opts.TopTerms]
	}
	rows := make([]map[string]any, len(stats))
	for i, stat := range stats {
		rows[i] = map[string]any{
			"term":          stat.Term,
			"occurrences":   stat.Occurrences,
			"segments":      stat.Segments,
			"conversations": stat.Conversations,
		}
	}
	out := map[string]any{
		"stats":            true,
		"scope":            scope,
		"filter":           segmentFilterOutput(filter),
		"ngram":            opts.NGram,
		"top_terms":        opts.TopTerms,
		"segments_scanned": count,
		"terms":            rows,
	}
	if opts.TopTermsCapped {
		out["top_terms_capped"] = true
		out["requested_top_terms"] = opts.RequestTopTerms
	}
	return jsonResult(out)
}

func filterSegmentHits(hits []searchindex.SegmentHit, query string, opts matchOptions) []searchindex.SegmentHit {
	if opts.Regex == nil && !opts.CaseSensitive {
		return hits
	}
	terms := parseTermsWithCase(query, opts.CaseSensitive)
	filtered := hits[:0]
	for _, hit := range hits {
		if opts.CaseSensitive {
			ok := true
			for _, term := range terms {
				if !strings.Contains(hit.Text, term) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		if opts.Regex != nil && !opts.Regex.MatchString(hit.Text) {
			continue
		}
		filtered = append(filtered, hit)
	}
	return filtered
}

func segmentSummaryRow(summary Summary, hit searchindex.SegmentHit, scope string) map[string]any {
	row := map[string]any{"id": summary.ID, "title": summary.Title}
	if summary.Preview != "" && summary.Preview != summary.Title {
		row["preview"] = summary.Preview
	}
	if summary.MessageCount > 0 {
		row["message_count"] = summary.MessageCount
	}
	if !summary.UpdatedAt.IsZero() {
		row["updated_at"] = summary.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if scope == "all" && summary.WorkspacePath != "" {
		row["workspace_path"] = summary.WorkspacePath
	}
	if summary.Origin != "" {
		row["origin"] = summary.Origin
	}
	matched := map[string]any{
		"kind":    hit.Kind,
		"ordinal": hit.Ordinal,
		"text":    truncateRunes(CleanText(hit.Text), maxSnippetChars),
	}
	if hit.MessageID != "" {
		matched["message_id"] = hit.MessageID
	}
	if hit.Role != "" {
		matched["role"] = hit.Role
	}
	if hit.ToolName != "" {
		matched["tool_name"] = hit.ToolName
	}
	if hit.Kind == searchindex.SegmentToolResult {
		if hit.Failed {
			matched["outcome"] = searchindex.OutcomeFailed
		} else {
			matched["outcome"] = searchindex.OutcomeSucceeded
		}
	}
	row["matched_segment"] = matched
	return row
}

func segmentFilterOutput(filter searchindex.SegmentFilter) map[string]any {
	out := map[string]any{
		"segment_kind":    "any",
		"tool_name":       "any",
		"tool_outcome":    "any",
		"exclude_runtime": filter.ExcludeRuntime,
	}
	if filter.WorkspacePath != "" {
		out["workspace_path"] = filter.WorkspacePath
	}
	if filter.Kind != "" {
		out["segment_kind"] = filter.Kind
	}
	if filter.ToolName != "" {
		out["tool_name"] = filter.ToolName
	}
	if filter.Outcome != "" {
		out["tool_outcome"] = filter.Outcome
	}
	return out
}

func reportedSearchQuery(query string, caseSensitive bool) string {
	query = strings.TrimSpace(query)
	if !caseSensitive {
		query = strings.ToLower(query)
	}
	return query
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareFloat(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareTime(left, right time.Time) int {
	switch {
	case left.Before(right):
		return -1
	case left.After(right):
		return 1
	default:
		return 0
	}
}
