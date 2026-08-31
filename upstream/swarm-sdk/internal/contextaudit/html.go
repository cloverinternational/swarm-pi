package contextaudit

import (
	"fmt"
	"html"
	"io"
)

// bucket colors used across the segmented summary bar and per-row bars.
const (
	colorSystem   = "#4f8cff"
	colorTools    = "#ff9f43"
	colorMessages = "#2ecc71"
)

// RenderHTML writes a self-contained HTML document (no external assets) that
// visualizes the breakdown: a segmented summary bar showing the three buckets'
// share of the grand total, followed by one table per bucket where each row
// carries a horizontal bar sized relative to the largest entry in that bucket.
// title is shown in the page header and <title>.
func (r Report) RenderHTML(w io.Writer, title string) {
	sysTotal := sumBytes(r.System)
	toolTotal := sumBytes(r.Tools)
	msgTotal := sumBytes(r.Messages)
	grand := sysTotal + toolTotal + msgTotal

	fmt.Fprintf(w, htmlHead, html.EscapeString(title))

	// Header + grand total.
	fmt.Fprintf(w, `<header><h1>%s</h1>`, html.EscapeString(title))
	fmt.Fprintf(w, `<div class="grand">~%s tokens <span class="sub">(%s bytes at 4 ch/tok — approximate)</span></div></header>`,
		commas(estTokens(grand)), commas(grand))

	// Segmented summary bar.
	io.WriteString(w, `<div class="summary"><div class="segbar">`)
	writeSegment(w, "system prompt", sysTotal, grand, colorSystem)
	writeSegment(w, "tool schemas", toolTotal, grand, colorTools)
	writeSegment(w, "messages", msgTotal, grand, colorMessages)
	io.WriteString(w, `</div><div class="legend">`)
	writeLegend(w, "system prompt", sysTotal, grand, colorSystem)
	writeLegend(w, "tool schemas", toolTotal, grand, colorTools)
	writeLegend(w, "messages", msgTotal, grand, colorMessages)
	io.WriteString(w, `</div></div>`)

	// Per-bucket tables.
	writeBucketHTML(w, "System prompt", colorSystem, r.System, sysTotal)
	writeBucketHTML(w, "Tool schemas", colorTools, r.Tools, toolTotal)
	writeBucketHTML(w, "Messages", colorMessages, r.Messages, msgTotal)

	io.WriteString(w, htmlTail)
}

func writeSegment(w io.Writer, name string, part, whole int, color string) {
	if whole <= 0 || part <= 0 {
		return
	}
	pct := 100 * float64(part) / float64(whole)
	fmt.Fprintf(w,
		`<div class="seg" style="width:%.3f%%;background:%s" title="%s: %s tokens (%.1f%%)"></div>`,
		pct, color, html.EscapeString(name), commas(estTokens(part)), pct)
}

func writeLegend(w io.Writer, name string, part, whole int, color string) {
	pct := 0.0
	if whole > 0 {
		pct = 100 * float64(part) / float64(whole)
	}
	fmt.Fprintf(w,
		`<span class="lg"><i style="background:%s"></i>%s — ~%s tok (%.1f%%)</span>`,
		color, html.EscapeString(name), commas(estTokens(part)), pct)
}

func writeBucketHTML(w io.Writer, title, color string, comps []Component, total int) {
	fmt.Fprintf(w, `<section><h2>%s <span class="bt">~%s tokens · %s bytes · %d items</span></h2>`,
		html.EscapeString(title), commas(estTokens(total)), commas(total), len(comps))

	if len(comps) == 0 {
		io.WriteString(w, `<p class="empty">(none)</p></section>`)
		return
	}

	maxBytes := 0
	for _, c := range comps {
		if c.Bytes > maxBytes {
			maxBytes = c.Bytes
		}
	}

	io.WriteString(w, `<table><thead><tr><th>item</th><th class="barcol"></th><th>bytes</th><th>~tokens</th><th>share</th></tr></thead><tbody>`)
	for _, c := range comps {
		barPct := 0.0
		if maxBytes > 0 {
			barPct = 100 * float64(c.Bytes) / float64(maxBytes)
		}
		share := 0.0
		if total > 0 {
			share = 100 * float64(c.Bytes) / float64(total)
		}
		fmt.Fprintf(w,
			`<tr><td class="label">%s</td><td class="barcol"><div class="bar"><div class="fill" style="width:%.3f%%;background:%s"></div></div></td><td class="num">%s</td><td class="num">%s</td><td class="num">%.1f%%</td></tr>`,
			html.EscapeString(c.Label), barPct, color, commas(c.Bytes), commas(c.Tokens()), share)
	}
	io.WriteString(w, `</tbody></table></section>`)
}

func sumBytes(comps []Component) int {
	t := 0
	for _, c := range comps {
		t += c.Bytes
	}
	return t
}

const htmlHead = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { color-scheme: dark; }
* { box-sizing: border-box; }
body { margin:0; font:14px/1.5 -apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;
       background:#0f1115; color:#e6e6e6; padding:24px; }
header { margin-bottom:20px; }
h1 { font-size:20px; margin:0 0 4px; }
.grand { font-size:28px; font-weight:700; color:#fff; }
.grand .sub { font-size:13px; font-weight:400; color:#8b93a1; }
.summary { background:#171a21; border:1px solid #232733; border-radius:10px; padding:16px; margin-bottom:24px; }
.segbar { display:flex; height:26px; width:100%%; border-radius:6px; overflow:hidden; background:#232733; }
.seg { height:100%%; }
.legend { margin-top:12px; display:flex; flex-wrap:wrap; gap:16px; font-size:13px; color:#c3c9d4; }
.lg i { display:inline-block; width:11px; height:11px; border-radius:2px; margin-right:6px; vertical-align:baseline; }
section { background:#171a21; border:1px solid #232733; border-radius:10px; padding:16px 18px; margin-bottom:18px; }
h2 { font-size:15px; margin:0 0 12px; }
h2 .bt { font-weight:400; font-size:12px; color:#8b93a1; margin-left:6px; }
table { width:100%%; border-collapse:collapse; }
th, td { padding:5px 8px; text-align:left; border-bottom:1px solid #1f2330; }
th { font-size:11px; text-transform:uppercase; letter-spacing:.04em; color:#7c8597; font-weight:600; }
td.label { font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:12.5px; white-space:nowrap; max-width:340px; overflow:hidden; text-overflow:ellipsis; }
td.num, th:nth-child(n+3) { text-align:right; font-variant-numeric:tabular-nums; white-space:nowrap; }
.barcol { width:42%%; }
.bar { background:#232733; border-radius:4px; height:14px; width:100%%; }
.fill { height:100%%; border-radius:4px; min-width:2px; }
.empty { color:#7c8597; }
tbody tr:hover { background:#1c2029; }
</style></head><body>`

const htmlTail = `<footer style="color:#7c8597;font-size:12px;margin-top:8px">
Token counts are estimates (4 bytes ≈ 1 token), not a real tokenizer — use for relative attribution.
</footer></body></html>`
