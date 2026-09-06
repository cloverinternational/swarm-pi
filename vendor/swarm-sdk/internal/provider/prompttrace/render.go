package prompttrace

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteBanner renders the collector's entries as a fixed-width table and
// writes it to w. The output is intended to be prepended to a REQUEST BODY
// dump in stderr so a human can correlate each section/message with the
// code path that produced it.
//
// Layout matches the design in the task brief:
//
//	══════════════════════════════════════════════════════════════════════
//	[PROMPT PROVENANCE] system prompt assembled from N sections
//	──────────────────────────────────────────────────────────────────────
//	  section                 chars   source
//	  ...
//	──────────────────────────────────────────────────────────────────────
//	  TOTAL                   N       chars (~N tokens at 4 ch/tok)
//	══════════════════════════════════════════════════════════════════════
//	[PROMPT PROVENANCE] messages constructed from M sites
//	──────────────────────────────────────────────────────────────────────
//	  index  role         chars   source
//	  ...
//	──────────────────────────────────────────────────────────────────────
func WriteBanner(w io.Writer, entries []Entry) {
	if w == nil {
		return
	}
	sections := make([]Entry, 0, len(entries))
	tools := make([]Entry, 0, len(entries))
	messages := make([]Entry, 0, len(entries))
	for _, e := range entries {
		switch e.Kind {
		case "section":
			sections = append(sections, e)
		case "tool":
			tools = append(tools, e)
		case "message":
			messages = append(messages, e)
		}
	}

	const eq = "══════════════════════════════════════════════════════════════════════"
	const dash = "──────────────────────────────────────────────────────────────────────"

	// Sections table.
	fmt.Fprintf(w, "\n%s\n", eq)
	fmt.Fprintf(w, "[PROMPT PROVENANCE] system prompt assembled from %d sections\n", len(sections))
	fmt.Fprintf(w, "%s\n", dash)
	if len(sections) == 0 {
		fmt.Fprintf(w, "  (no sections recorded — system prompt was empty or builder was not instrumented)\n")
	} else {
		fmt.Fprintf(w, "  %-26s %8s   %s\n", "section", "chars", "source")
		total := 0
		for _, e := range sections {
			source := e.Source
			if source == "" {
				source = "???:0"
			}
			fmt.Fprintf(w, "  %-26s %8d   %s\n", truncateLabel(e.Label, 26), e.Chars, source)
			total += e.Chars
		}
		fmt.Fprintf(w, "%s\n", dash)
		// Approximate token count at 4 chars/token (the conventional rough estimate).
		fmt.Fprintf(w, "  %-26s %8d   chars (~%s tokens at 4 ch/tok)\n",
			"TOTAL", total, formatThousands(total/4))
	}
	fmt.Fprintf(w, "%s\n", eq)

	// Tools table. Tool JSON schemas are part of provider context but live in
	// the request's Tools field, not the system-prompt string — so they get a
	// dedicated table. Sorted largest-first so the dominant schema is on top.
	fmt.Fprintf(w, "[PROMPT PROVENANCE] tool schemas: %d tools\n", len(tools))
	fmt.Fprintf(w, "%s\n", dash)
	if len(tools) == 0 {
		fmt.Fprintf(w, "  (no tools recorded — request had no Tools or builder was not instrumented)\n")
	} else {
		sortEntriesByCharsDesc(tools)
		fmt.Fprintf(w, "  %-26s %8s\n", "tool", "chars")
		total := 0
		for _, e := range tools {
			fmt.Fprintf(w, "  %-26s %8d\n", truncateLabel(e.Label, 26), e.Chars)
			total += e.Chars
		}
		fmt.Fprintf(w, "%s\n", dash)
		fmt.Fprintf(w, "  %-26s %8d   chars (~%s tokens at 4 ch/tok)\n",
			"TOTAL", total, formatThousands(total/4))
	}
	fmt.Fprintf(w, "%s\n", eq)

	// Messages table.
	fmt.Fprintf(w, "[PROMPT PROVENANCE] messages constructed from %d sites\n", len(messages))
	fmt.Fprintf(w, "%s\n", dash)
	if len(messages) == 0 {
		fmt.Fprintf(w, "  (no messages recorded — message-construction sites are not instrumented)\n")
	} else {
		fmt.Fprintf(w, "  %-6s %-12s %8s   %s\n", "index", "role", "chars", "source")
		for _, e := range messages {
			source := e.Source
			if source == "" {
				source = "???:0"
			}
			role := e.Role
			if role == "" {
				role = "?"
			}
			label := e.Label
			if label != "" {
				role = role + " " + truncateLabel("("+label+")", 10)
			}
			fmt.Fprintf(w, "  [%-4d] %-12s %8d   %s\n", e.MsgIndex, truncateLabel(role, 12), e.Chars, source)
		}
	}
	fmt.Fprintf(w, "%s\n", dash)
}

// sortEntriesByCharsDesc orders entries largest-first so the dominant
// contributor surfaces at the top of the table.
func sortEntriesByCharsDesc(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Chars > entries[j].Chars
	})
}

// truncateLabel keeps a label within max runes, padding nothing (the caller's
// %-Ns verb handles padding). Long labels are truncated with a trailing '…'.
func truncateLabel(label string, max int) string {
	if len(label) <= max {
		return label
	}
	if max <= 1 {
		return label[:max]
	}
	return label[:max-1] + "…"
}

// formatThousands formats an integer with comma separators (e.g. 12345 -> "12,345").
func formatThousands(n int) string {
	if n < 0 {
		return "-" + formatThousands(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if len(s) > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
