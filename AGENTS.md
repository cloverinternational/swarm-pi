# Repository instructions

## `upstream/` is read-only

The `upstream/` directory contains vendored reference snapshots. **Do not edit, format, regenerate, delete, move, or otherwise mutate any file under `upstream/`.**

- Read and inspect upstream sources as needed.
- Implement changes in local packages, extensions, tools, or documentation instead.
- If an upstream change appears necessary, stop and ask for explicit approval.
- The local `.pi/extensions/upstream-readonly.ts` guard blocks mutation tool calls and shell commands targeting `upstream/`.
