# Clickable links in the TUI

The chat viewport turns URLs, markdown links and source locations into clickable
terminal hyperlinks. Implementation: `internal/chat/hyperlink.go`.

## What becomes clickable

| Input in a message | Becomes | Notes |
| --- | --- | --- |
| `https://example.com/docs` | link to itself | trailing `.,;:!?` is not swallowed; a trailing `)` is kept when balanced, so `…/foo(1)` survives |
| `mailto:someone@example.com` | link to itself | |
| `[the docs](https://example.com)` | `the docs`, pointing at the URL | assistant prose only — see the trust boundary below |
| `internal/chat/components.go:2172` | `file://<host>/abs/path#2172` | needs a workspace root and the file must exist |
| `src/app.ts:42:7` | same, column ignored by most terminals | an extension is required, so `note:5` stays plain text |

Detection runs on the plain text **before** any styling, and all detectors are
merged into a single pass. Sequential passes would let one detector match inside
an escape sequence another had just emitted (the bare-URL pattern happily finds
the URL inside a markdown link's own escape and splices a second link into the
middle of it). Styling is then applied only *outside* complete link spans, so a
URL containing `_` or `*` never has SGR escapes spliced into the target.

## Trust boundary: prose vs tool output

`LinkifyOptions.AllowLabels` distinguishes the two sources.

- **Assistant prose** — labels allowed. `[click here](url)` renders as `click here`.
- **Raw tool output** — labels refused. A web-search result could otherwise
  render `click for docs` pointing somewhere else entirely. The markdown
  construct is left as literal text; bare URLs in tool output still link,
  because there the visible text *is* the destination.

`LinkifyOptions.WorkspaceRoot` resolves relative file paths. Empty disables path
linking entirely, since a relative path with no root cannot be resolved.

## Safety

The URI is embedded in an escape sequence the terminal acts on, so a link target
is a capability grant, not formatting. Two independent checks:

1. **Scheme allowlist** — only `http`, `https`, `mailto` and `file`. Everything
   else (`javascript:`, `data:`, and any custom scheme a terminal may have been
   configured to hand to a program) is refused.
2. **Control-byte rejection** — a raw `ESC`, `BEL` or `ST` inside the URI would
   terminate the escape early and let the remainder execute as terminal
   commands. Anything outside printable ASCII must already be percent-encoded.

A refused link degrades to plain text; it never emits a broken escape. Raw tool
output keeps the strict sanitizer, which strips escapes wholesale; hyperlinks are
re-admitted only for viewport-bound content, through a scoped sanitizer that
validates OSC 8 specifically.

## Why links survive wrapping

An OSC 8 hyperlink is:

```
ESC ] 8 ; params ; URI ST   visible text   ESC ] 8 ; ; ST
```

The binding is emitted once at the start of the run, so a naive re-wrap orphans
everything after the first physical line — the tail stops being clickable, or the
link bleeds onto whatever follows.

The fix is the `id=` parameter: segments sharing an `id` are treated by the
terminal as **one** logical link and highlight together on hover. The id is a
hash of the URI, so two segments of one wrapped link agree without threading
state through the wrapper, and two distinct URIs can never collapse into one
link. Each physical line closes the link at its end and re-opens it with the same
id at the start of the next.

Link state is tracked **per physical line**, not per logical string, because
helpers such as `ansi.TruncateLeft` sometimes re-emit the link at a cut and
sometimes do not. A fragment that merely *ends* a link contains an OSC but does
not open one, so the wrapper checks whether the fragment's *first* sequence
opens before deciding to re-open.

The whole feature is gated on `TestOSC8IsZeroWidth`: `lipgloss.Width`,
`ansi.StringWidth` and the project's `PrintableWidth` must all agree that the
escape occupies zero columns. If they ever disagree, links shift layout and the
test fails rather than the UI silently drifting.

## Terminal support

Most modern terminals support OSC 8 (kitty, WezTerm, foot, iTerm2, Ghostty,
recent VTE-based terminals, Windows Terminal).

### tmux

tmux passes hyperlinks through from 3.4, but it must be told the outer terminal
can render them:

```tmux
set -ga terminal-features "*:hyperlinks"
```

### kitty: jump to a line in your editor

File locations are encoded as `file://<host>/<abs path>#<line>`. The host is
named deliberately: over SSH the terminal is local and the path is remote, and an
unqualified `file://` URL would open the wrong local file.

kitty exposes the fragment to `open-actions.conf` as `$FRAGMENT`, alongside
`$FILE_PATH` and `$EDITOR`:

```
# ~/.config/kitty/open-actions.conf
protocol file
fragment_matches [0-9]+
action launch --type=overlay --cwd=current -- $EDITOR +$FRAGMENT -- $FILE_PATH

protocol file
action launch --type=overlay --cwd=current -- $EDITOR -- $FILE_PATH
```

Clicking `components.go:2172` then opens the file at line 2172.

## Troubleshooting

- **Nothing is clickable** — the terminal does not support OSC 8, or tmux is in
  the way without `terminal-features "*:hyperlinks"`.
- **File locations are plain but URLs work** — no workspace root was resolved, or
  the file does not exist on disk. Only paths that resolve are linked.
- **A markdown link shows as literal `[text](url)`** — it came from raw tool
  output, where labels are refused on purpose.
- **A URL is not linked at all** — its scheme is outside the allowlist, or it
  contains raw control bytes.
