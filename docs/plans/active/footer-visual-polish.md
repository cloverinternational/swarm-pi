# Footer visual polish

## Goal

Refine the Pi footer into a sleek two-row identity-and-metrics display. The
current footer is a wrapped sentence with styling applied after wrapping,
which makes the hierarchy weak and can mis-handle ANSI styling.

## Choreography

1. Add small pure helpers in `.pi/extensions/50-ui/conversation-metrics.ts` to
   build an unstyled identity row and metrics row, then wrap each row before
   applying theme colors.
2. Render a compact identity row containing a state glyph, provider/model
   identity, and a restrained separator. Render a secondary metrics row with
   walltime, output tokens, registered footer segments, and the Bash hint.
3. Keep expanded running-work mode separate, but apply the same semantic color
   hierarchy to its header, selection marker, and status glyphs.
4. Extend the focused UI tests for two-row content, missing model fallback,
   wrapping, and width safety.
5. Run focused Vitest tests and `git diff --check`; inspect the final diff to
   ensure only the footer implementation and focused tests changed.

## Invariants and risks

- Every returned line must fit the requested terminal width.
- Wrapping must happen on plain text before ANSI styling is added.
- Footer segments from other extensions remain visible in the metrics row.
- Missing model context must not produce an empty or malformed identity row.
- No vendor files or unrelated working-tree changes may be modified.

## Verification

Run:

```sh
npx vitest run .pi/test/ui/conversation-metrics.test.ts .pi/test/ui/running-work.test.ts
git diff --check
```
