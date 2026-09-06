# Repository layout: Option C (domain-layered, one-to-one mirror)

Status: proposed. Executes as 8 green commits on `main`. Every commit must pass
the gate listed for it before the next commit starts.

## Findings that shape the design (verified in-repo)

- Pi discovers project extensions **non-recursively**: `extensions/*.ts`,
  `extensions/*/index.ts`, or `extensions/*/package.json` with a `"pi": {
  "extensions": [...] }` manifest (`vendor: pi-mono .../extensions/loader.ts`
  `discoverExtensionsInDir`). Layer folders therefore each carry a manifest
  package.json; every listed file stays an independent extension. Manifest order
  is load order, which replaces today's implicit `readdir` order.
- No extension declares a load-order dependency; all hook infrastructure is
  reached by module import (`hook-state.ts`), not by registration order.
- 25 of 30 files in `taskmanage/test/` import `../../.pi/...`; only 7 import
  `../src/`. Tests are `vitest run test` per package; there is no root runner.
- Inter-package source imports: `agents -> runtime-contracts`,
  `taskmanage -> agents, schedule`. Everything else is leaf.
- `.pi/keybindings.json` is dead: Pi reads keybindings only from the agent dir
  (`core/keybindings.ts:349`); nothing in `.pi/` reads it.
- `.pi/system-prompts.json` is a migrated legacy file (`LEGACY_PROMPT_FILE`).
- `upstream/opencode` is a gitlink (mode 160000) with **no** `.gitmodules`
  entry; remote is `https://github.com/anomalyco/opencode.git`.
- `.pi/agent-sessions/` has 25 tracked files despite being gitignored.
- `dist/` is tracked for 8 packages and ignored for 3.
- `.parity*` = 9 root dirs, 124 tracked probe outputs; `.parity-plexus` also
  holds two real scripts (`run-ab.sh`, `mock-upstream.py`).
- `swarm-themes/package.json` is an empty stub; real themes are `.pi/themes/`.
- Hardcoded path strings that must change: `upstream-readonly.ts` regex
  (`upstream`), `swarm-prompt/src/index.ts` `UPSTREAM_SOURCE`,
  `tools/parity/gen-plan-texts.mjs` (`upstream/swarm-sdk`, `.pi/lib/...`),
  `tools/parity/probe.mjs:968` (`.parity` default), root `package.json`
  scripts, `.pi/extensions/autogenskills.ts:12` (`.pi/swarm-settings.json`),
  `.pi/lib/swarm-prompt-context-config.ts:17-18`, `system-prompts.ts:53`.

## Layer taxonomy (single rule: "what does it primarily register/export?")

| # | Layer | Owns |
| --- | --- | --- |
| 00 | `runtime` | integration boundary, hook engine, tool surface/gating, transport parity, telemetry |
| 10 | `context` | system prompt, prompt/context config, plan mode, thinking, skills, inspector |
| 20 | `policy` | upstream read-only guard, disk hooks, sleep blocker, nudges |
| 30 | `tools` | every `registerTool` surface: bash, fs, search, agents, tasks, schedule, history, vault, research, MCP, ask-user, annoyed, codemode |
| 40 | `state` | durable session entries: memory history, conversation metadata |
| 50 | `ui` | control panel, metrics widgets, themes, tools-status command |

The same six names are used in `.pi/extensions/`, `.pi/lib/`, `.pi/test/`,
and `packages/`. If a thing does not fit one layer it is split, not parked.

## Target tree

```text
Pi-Swarm/
├── AGENTS.md                      # contract; repository map rewritten
├── package.json                   # npm workspaces: packages/*/*
├── package-lock.json              # single lockfile
├── tsconfig.base.json             # shared compiler options
├── vitest.config.ts               # single test runner (packages + .pi + tests)
├── .gitignore  .gitmodules
├── .pi/
│   ├── extensions/
│   │   ├── 00-runtime/  package.json  swarm-runtime.ts hooks.ts swarm-transport-parity.ts cache-telemetry.ts
│   │   ├── 10-context/  package.json  swarm-prompt.ts system-prompts.ts prompt-context-configure.ts
│   │   │                              system-inspector.ts swarm-thinking.ts swarm-plan-mode.ts swarm-skills.ts autogenskills.ts
│   │   ├── 20-policy/   package.json  upstream-readonly.ts swarm-disk-hooks.ts
│   │   ├── 30-tools/    package.json  swarm-bash.ts swarm-background-bash.ts swarm-fs-tools.ts swarm-search.ts
│   │   │                              swarm-agent-tools.ts taskmanage.ts control-task-tools.ts schedule.ts
│   │   │                              swarm-history-vault-tools.ts history-search.ts research-tools.ts exa-search.ts
│   │   │                              vault.ts pi-ask-user.ts annoyed/ codemode/
│   │   ├── 40-state/    package.json  memory-history.ts swarm-conversation-metadata.ts
│   │   └── 50-ui/       package.json  control-panel.ts conversation-metrics.ts swarm-themes.ts swarm-tools-status.ts
│   ├── lib/
│   │   ├── runtime/   hook-state.ts hook-observations.ts hook-presenter.ts hook-render-bridge.ts
│   │   │              swarm-builtin-hooks.ts swarm-builtin-hooks-runtime.ts swarm-transport-parity.ts
│   │   │              swarm-tool-surface.ts swarm-tool-gating.ts swarm-toolclass.ts swarm-toolout.ts
│   │   │              swarm-configmigrate.ts swarm-stdin-conflict.ts
│   │   ├── context/   swarm-context.ts swarm-prompt-context-config.ts swarm-plan-mode.ts
│   │   │              swarm-plan-mode-texts.ts swarm-skill-registry.ts
│   │   ├── policy/    swarm-sleep-blocker.ts swarm-annoyance-nudge.ts
│   │   ├── tools/     swarm-bash.ts swarm-bgprocess.ts swarm-apply-patch.ts swarm-read-image.ts
│   │   │              swarm-agent-tools.ts swarm-history-tools.ts swarm-vault-tools.ts swarm-annoyed-publish.ts
│   │   └── state/     swarm-conversation-metadata.ts
│   ├── test/          <layer>/*.test.ts   (23 files relocated from taskmanage/test)
│   ├── config/        swarm-settings.json prompt-context.json  (api-keys.json stays ignored)
│   └── themes/        (unchanged)
├── packages/
│   ├── runtime/  core/ contract/ runtime-contracts/
│   ├── context/  prompt/ skills/ autogenskills/
│   ├── policy/   policy/
│   └── tools/    agents/ taskmanage/ mcp/ schedule/
├── tests/parity/                  parity-probe.test.mjs
├── tools/
│   ├── parity/                    probe.mjs tui-probe.mjs hookprobe.sh scripted-model.mjs gen-plan-texts.mjs fixtures/
│   ├── parity/plexus/             run-ab.sh mock-upstream.py   (from .parity-plexus)
│   ├── integration/               run-postgres-integration.mjs cache-dogfood.mjs
│   └── repo/                      rewrite-imports.mjs (used by this migration; kept for future moves)
├── docs/
│   ├── architecture/  modular-pi-architecture.md  swarm-tui-to-pi-map.md  swarm-skills-tasks-hooks-model.md
│   │                  hooks-prompts-tools-pi-equivalence.md  daemon-control-plane-architecture.md  absurd-pi-integration.md
│   ├── reference/     persistence.md  profiles.md  memory-history.md  upstream-snapshot.md
│   │                  pi-missing-tools-and-ecosystem.md  history-search.md  research-tools.md
│   │                  taskmanage-reference-contract.md  taskmanage-workflows.md
│   ├── parity/        parity-plan.md  acceptance.md  media/pi-swarm-parity.{tape,gif,mp4,png}
│   └── plans/
│       ├── active/    improve-plan-mode-swarm.md  post-acting-verification-parity.md
│       │              unified-prompt-context-selector.md  hook-ordering-first-principles.md
│       ├── archive/   modular-pi-snapshot.md  swarm-tui-to-pi.md  pi-swarm-hook-rendering-surgical.md
│       └── 2026-09-06-repo-layout-option-c.md   (this file)
├── infra/
│   ├── postgres/                  (unchanged)
│   └── bridges-go/                bridge.go bridge_test.go go.mod   (from bridges/)
├── vendor/                        pi-mono/ swarm-sdk/ opencode/ (submodule) pi-ask-user/ (submodule)
└── artifacts/                     GITIGNORED  parity/<run>/  (all former .parity*)
```

Naming rules enforced by the sweep in commit 8:

- directories and Markdown files: `kebab-case`; the only capitalised file is `AGENTS.md`;
- package dir name == npm name suffix (`packages/*/prompt` is `@pi-swarm/prompt`);
- one `README.md` per package; `dist/`, `node_modules/`, `artifacts/`,
  `.pi/agent-sessions/`, `.swarm/` are never tracked;
- generated code says so in its header and names its generator.

## Commit sequence and gates

### Commit 0 — checkpoint (`wip: checkpoint in-flight parity work before layout migration`)

The tree has ~35 modified and ~20 untracked source files. Moving modified files
is fine for `git mv`, but mixing feature work into layout commits destroys
rename detection and bisectability. Commit everything as-is first. If you
prefer a stash, say so at approval time; the rest of the plan is unchanged.

Gate: `npm test` and `npm run test:parity` run and their pass/fail set is
recorded to `artifacts/baseline-tests.txt` (untracked). Later gates compare
against this baseline, not against "all green".

### Commit 1 — hygiene (`chore(layout): untrack generated state, fix ignores, delete dead files`)

1. `git rm -r --cached .pi/agent-sessions .parity .parity-agents .parity-budget
   .parity-hooks .parity-minute .parity-review .parity-shapes .parity-tools`
   and the non-script parts of `.parity-plexus`.
2. `git mv .parity-plexus/run-ab.sh .parity-plexus/mock-upstream.py tools/parity/plexus/`;
   move the remaining `.parity*` dirs to `artifacts/parity/<name>/` on disk.
3. `git rm -r --cached` every tracked `dist/` (agents, mcp, runtime-contracts,
   schedule, skills, swarm-contract, swarm-core, swarm-prompt).
4. Delete: `.pi/keybindings.json`, `.pi/system-prompts.json`,
   `swarm-themes/` (stub), per-package `package-lock.json` (workspaces make one).
5. `.gitignore` becomes: `artifacts/`, `**/dist/`, `**/node_modules/`,
   `.pi/agent-sessions/`, `.pi/config/api-keys.json`, `.pi/config/*.local.json`, `.swarm/`.
6. `.gitmodules`: add `vendor/opencode` entry now under its current path
   `upstream/opencode` (url above) so the gitlink is no longer orphaned.
7. `tools/parity/probe.mjs:968` default output → `artifacts/parity/default`;
   root `package.json` `parity:probe` `--output artifacts/parity/default`.

Gate: `git ls-files | grep -Ec '(^\.parity|/dist/|agent-sessions)'` == 0;
`npm run test:parity` matches baseline.

### Commit 2 — docs (`docs(layout): move design records under docs/ with one naming scheme`)

`git mv` per the target tree. Content edits limited to: relative links
between moved docs, and a one-line "Status: archived — superseded by X" header
on the three archived plans. `taskmanage/REFERENCE_CONTRACT.md` and
`WORKFLOWS.md` move to `docs/reference/` (they describe the tool contract, not
the package's build). `.tape/` moves whole to `docs/parity/media/` and the
`.tape` file's output paths are updated.

Gate: `rg -l '\]\((\.\./)*[A-Z_]+\.md' docs AGENTS.md` == 0 (no links to old
SCREAMING names); `git ls-files '*.md' | grep -v '^vendor/' | grep -E '[A-Z]'`
lists only `AGENTS.md` and per-package `README.md`.

### Commit 3 — vendor + infra (`chore(layout): vendor/ for read-only references, infra/ for local services`)

1. `git mv upstream vendor` (rename detection handles 3.8k files);
   `git mv pi-ask-user vendor/pi-ask-user`; `.gitmodules` paths updated;
   `git submodule sync`.
2. `git mv bridges infra/bridges-go`; `git mv tools/run-postgres-integration.mjs
   tools/cache-dogfood.mjs tools/integration/`.
3. Code edits: `upstream-readonly.ts` — regex and `path.resolve(root, "upstream")`
   → `"vendor"`, message text → "vendor/ is read-only"; `UPSTREAM_SOURCE`
   string → `vendor/swarm-sdk/...`; `gen-plan-texts.mjs` root path;
   `.pi/extensions/pi-ask-user.ts` re-export path; root `package.json`
   `test:integration` path; any test asserting the old message text.

Gate: `rg -n '\bupstream/' --glob '!vendor/**' --glob '!docs/plans/archive/**'
--glob '!docs/reference/upstream-snapshot.md'` == 0; `npm test` matches baseline.

### Commit 4 — packages/ + workspaces (`refactor(layout): packages/<layer>/<name> with npm workspaces`)

1. `git mv` each package into `packages/<layer>/<name>`; rename dirs
   `swarm-core→core`, `swarm-contract→contract`, `swarm-prompt→prompt`;
   set `@pi-swarm/prompt` in its package.json (was `@pi-swarm/swarm-prompt`).
2. Root `package.json`: `"workspaces": ["packages/*/*"]`; scripts become
   `build: npm run build --workspaces --if-present`, `test: vitest run`,
   `test:parity`, `parity:probe`, `test:integration`, `dogfood`.
3. Add `tsconfig.base.json` (the options every package already repeats);
   each package `tsconfig.json` → `"extends": "../../../tsconfig.base.json"`
   plus its `rootDir/outDir/include`.
4. Add root `vitest.config.ts` including `packages/**/test/**/*.test.ts`,
   `.pi/test/**/*.test.ts`, `tests/**/*.test.ts`. Per-package `test` scripts
   are removed (one runner); `vitest` moves to root devDependencies.
5. Rewrite the relative cross-package imports (`agents→runtime-contracts`,
   `taskmanage→agents,schedule`, and every `.pi/**` import of `../../<pkg>/src`)
   with `tools/repo/rewrite-imports.mjs`: it takes a JSON move map
   `{old: new}`, resolves each relative specifier from the file's **old**
   location to an absolute target, maps it through the move map, and re-emits
   it relative to the file's **new** location. No string-sed of paths.
6. Remove the 11 per-package `node_modules/`; `npm install` at root.

Gate: `npm install && npm run build && npm test` match baseline;
`rg -n 'from "\.\./\.\./(agents|skills|taskmanage|policy|mcp|schedule|swarm-|runtime-contracts)'` == 0.

### Commit 5 — `.pi/` layering (`refactor(layout): .pi extensions, lib, and tests mirror the six layers`)

1. `git mv` extensions and lib files per the target tree (move map extends the
   one from commit 4; same rewrite script).
2. Write six `package.json` manifests, e.g.
   `.pi/extensions/00-runtime/package.json`:
   `{"name":"@pi-swarm/ext-runtime","private":true,"pi":{"extensions":["swarm-runtime.ts","hooks.ts","swarm-transport-parity.ts","cache-telemetry.ts"]}}`.
   Order inside a manifest = today's alphabetical order within that layer, so
   observed behaviour is unchanged; layer prefixes `00..50` fix cross-layer
   order. `30-tools` lists `annoyed/index.ts` and `codemode/index.ts` explicitly.
3. Move the 23 `.pi`-targeting tests from `taskmanage/test/` to
   `.pi/test/<layer>/` (layer = layer of the primary import). The 7 tests that
   import `../src/` stay in `packages/tools/taskmanage/test/`.
4. `gen-plan-texts.mjs` output path → `.pi/lib/context/swarm-plan-mode-texts.ts`;
   regenerate once to prove the generator still works.

Gate: `npm test` matches baseline; `npm run parity:probe` boots Pi with the
project extensions and its `tools.json` lists the same tool names as
`artifacts/baseline-tools.json` captured in commit 0 (extension discovery is
the thing this commit can break, and only a real Pi boot proves it).

### Commit 6 — config paths (`refactor(layout): project config lives in .pi/config/`)

The only commit that changes runtime behaviour visible to a user of this repo.

1. `git mv .pi/swarm-settings.json .pi/prompt-context.json .pi/config/`.
2. `swarm-prompt-context-config.ts`: `CONFIG_FILE = ".pi/config/prompt-context.json"`;
   migration chain becomes `system-prompts.json → prompt-context.json →
   config/prompt-context.json` (extend the existing `onRepair` migration; add
   a test case for the middle step).
3. `autogenskills.ts:12` and `system-prompts.ts:53` use the new path.

Gate: `npm test`; new migration test passes; `npm run parity:probe` prompt
output identical to commit 5.

### Commit 7 — root tests and tools (`chore(layout): tests/parity, tools/integration`)

`git mv tests/parity-probe.test.mjs tests/parity/`; update its
`../tools/parity/probe.mjs` import and the `test:parity` script.

Gate: `npm run test:parity` matches baseline.

### Commit 8 — contract + sweep (`docs(agents): repository map for the layered layout`)

1. Rewrite `AGENTS.md` sections "Repository map", "Extension inventory",
   "Package boundaries", "Validation" (commands), "Safety and upstream policy"
   (`vendor/`), "Capability locator" paths, and the "AGENTS.md discovery"
   section for `.pi/extensions/<layer>/` and `packages/<layer>/<name>/`.
   Add the one-rule layer table above and the naming rules.
2. Stale-path sweep over everything except `vendor/` and `docs/plans/archive/`:
   `rg -n -e 'taskmanage/test' -e '\.parity' -e 'upstream/' -e 'swarm-core' -e
   'swarm-contract' -e 'swarm-prompt/' -e '\.pi/lib/swarm-' -e
   '\.pi/extensions/[a-z]' -e '\.tape' -e 'bridges/'` must be empty.
3. Delete `PLAN_*`/`PI_*`/`SWARM_*` stragglers if any survived commit 2.

Gate: `npm run dogfood` (build + test) matches baseline; `git status` clean.

## Breaking points and how each is caught

| Risk | Caught by |
| --- | --- |
| Pi fails to discover an extension after layering | commit 5 gate: real Pi boot via parity probe, tool-name diff against baseline |
| Import rewrite misses a specifier or hits a dynamic import | build + vitest; plus `rg` for any `from "\.\./` that no longer resolves (`tools/repo/rewrite-imports.mjs --check` walks every relative specifier and `fs.existsSync`s it) |
| `upstream-readonly` guard stops blocking after rename | existing `disk-hooks.test.ts` asserts the block; message text updated in the same commit |
| Submodule move breaks `git submodule update` | commit 3 gate: `git submodule status` shows both entries at `vendor/*` with no `-`/`+` prefix |
| Config path move strands a user's `.pi/prompt-context.json` | migration chain + test in commit 6 |
| Tracked/untracked confusion in `artifacts/` | commit 1 gate on `git ls-files` |
| Load order regression | manifests preserve intra-layer alphabetical order; cross-layer order is now explicit and documented |

## Explicitly out of scope (follow-ups, not part of this migration)

- Switching `../../<pkg>/src` imports to `@pi-swarm/<pkg>` workspace names
  (needs a build-order or vitest alias decision; separate change).
- Consolidating `annoyed/` and `codemode/` nested packages into workspaces.
- Any behavioural change beyond the config-path move in commit 6.
