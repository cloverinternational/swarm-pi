# VHS Visual Testing

Use [VHS](https://github.com/charmbracelet/vhs) for deterministic TUI visual inspection without external provider traffic.

## Why this setup

- Uses `./swarm --debug-lineage-fixture`, which renders the real chat/error-lineage path with deterministic data.
- Produces both a GIF timeline and a PNG snapshot.
- Safe for LLM-driven workflows because key presses and timing are scripted.

## Install VHS

```bash
brew install vhs
```

## Run lineage fixture tape

From `/Users/neddana/GitHub/swarm/SCM/swarm-tui`:

```bash
make vhs-lineage
```

Artifacts are written to:

- `manual_tests/vhs/output/lineage_fixture.gif`
- `manual_tests/vhs/output/lineage_collapsed.png`
- `manual_tests/vhs/output/lineage_expanded.png`

## Baseline + verification loop

Create/update baseline images after intentional UI changes:

```bash
make vhs-baseline
```

Verify current rendering matches baseline (fails on any pixel drift):

```bash
make vhs-verify
```

Adjust comparison tolerance (default `0.0075` = `0.75%`):

```bash
PNGDIFF_MAX_CHANGED_RATIO=0.003 make vhs-verify
```

## Run a specific tape

```bash
./scripts/vhs-record.sh manual_tests/vhs/lineage_fixture.tape
```

Or via Make:

```bash
make vhs VHS_TAPE=manual_tests/vhs/lineage_fixture.tape
```

## Tape behavior

The lineage fixture tape:

1. Launches `./swarm --debug-lineage-fixture`
2. Captures collapsed lineage state screenshot
3. Sends `l` to toggle lineage expansion
4. Captures expanded lineage state screenshot
5. Exits
