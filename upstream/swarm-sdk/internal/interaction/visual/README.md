# Visual Primitives for ask_user_question

Use `type: "visual_choice"` when the answer is easier to *see* than to read.
For text choices, keep using `choice` / `multi_choice`.

## Quick reference

| Kind | Shape | When to use |
|---|---|---|
| `options` | `{multiselect?, items: [{key, label, description?}]}` | Text A/B/C with descriptions |
| `cards` | `{multiselect?, items: [{key, title, description?, body?}]}` | 2–4 rich-preview comparisons |
| `split` | `{items: [{key, label, body?}, {key, label, body?}]}` | Exactly two side-by-side options |
| `markdown` | `{content}` | Prose + fenced code (including ` ```mermaid ` blocks) |
| `mermaid` | `{diagram, caption?}` | A single mermaid diagram |
| `raw_html` | `{content}` | Escape hatch; sanitized (no `<script>`, no `on*` handlers) |

## Composition

Interactive primitives' `body` accepts any presentation primitive:

```json
{
  "kind": "cards",
  "items": [
    {"key": "flat", "title": "Flat list",
     "body": {"kind": "mermaid", "diagram": "graph TD\nA-->B\nA-->C"}},
    {"key": "tree", "title": "Tree",
     "body": {"kind": "mermaid", "diagram": "graph TD\nRoot-->A\nRoot-->B\nA-->C"}}
  ]
}
```

## Return values

- Single-select: the selected `key` comes back as `Answer`.
- Multi-select: selected keys come back as `Answers`.
- Cancelled question: error.

## Anti-patterns

- Don't embed `raw_html` when a primitive covers it — primitives keep styling consistent; RawHTML diverges.
- Don't write mermaid diagrams larger than ~30 nodes — browser rendering degrades.
- Don't use `visual_choice` for yes/no — use `confirm` instead.
