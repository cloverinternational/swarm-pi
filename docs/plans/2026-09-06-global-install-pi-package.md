# Global install: Pi-Swarm as a Pi package

Status: executed 2026-09-06 as a78643a (fork), 831c309 (manifest/prepare/deps),
f3239eb (doctor), and the docs/verification commit. Deviations found while
executing: `pi install <path>` writes `settings.packages` entries relative to
the settings file (`../../../clone`), not absolute — the doctor and the
probe's `--package` mode follow that shape; the package-path boot exposed
that `system-inspector` enumerated extensions/packages from the *workspace*
(reporting `EXTENSIONS (0)` from a foreign directory) and it now enumerates
from the checkout it was loaded from via the layer manifests. End-to-end
result from a foreign workspace: 91 registered tools, 35 extensions, 12
packages, model surface == baseline + `vault`, `ask_user_question` and
`codemode` loaded, doctor 16/16. Motivated by a Mac deployment that copied
`.pi/extensions/` by hand and hit, in order: missing repo modules, wrong
global paths, missing npm deps (`yaml croner absurd-sdk effect acorn
typescript`), node not on PATH in tmux, `maxTokens: 0` model metadata, a
missing theme, tmux exit, and a Plexus key scope. Items 1–3 and 6 are one
defect: Pi-Swarm is a repository-integrated runtime and only the repository
is a valid install unit. Items 4, 5, 7, 8 are host configuration; a doctor
check catches 4 and 5 before tmux is involved.

## Decisions (resolved with the user)

| Decision | Answer |
| --- | --- |
| Install shape | The whole repository is the Pi package. Remote: `pi install git:github.com/cloverinternational/swarm-pi@<tag>`; dev box: `pi install /home/swarm/Work/Pi-Swarm` (linked, not copied). |
| Remote | None yet; verify with a local temp clone; git form stays a placeholder in AGENTS.md until pushed. |
| `ask_user_question` | **Fork** `edlsh/pi-ask-user` v0.15.0 (MIT) into the tree; remove the submodule. |
| Build at install | `pi install` runs `npm install --omit=dev`, never `npm run build`. A root `prepare` script builds the two packages consumed from `dist/`. Verified: npm 11.17 runs `prepare` under `--omit=dev`. |
| Runtime deps | `typescript` (codemode transpiles at runtime) and `@types/node` (needed by `prepare`) move to root `dependencies`. |
| Manifest contents | Six layer dirs + `.pi/themes`. No `skills` key: the forked ask-user SKILL.md is kept unregistered so the `<available_skills>` block — a parity-compared prompt surface — does not change. |
| Doctor | Small `tools/install/doctor.mjs` + `npm run doctor`; no new dependencies. |

## Evidence (vendor/pi-mono/packages/coding-agent)

- `docs/packages.md`: git sources clone to `~/.pi/agent/git/<host>/<path>`,
  then `npm install` if `package.json` exists; local paths are linked without
  copying; project entry wins over global for the same identity.
- `src/core/package-manager.ts:1763-1787` `installGit`: `git clone` (no
  `--recurse-submodules`) then `npm install --omit=dev` (`:1709-1714`).
- `:534-560` `resolveExtensionEntries`: a directory listed in the manifest is
  expanded through its own `package.json` `pi.extensions` — our six layer
  manifests keep their load order.
- `:2429-2437` `addResource` dedupes by absolute path — a local-path install
  and this repo's own `.pi/` do not double-load.
- `src/core/extensions/loader.ts:48,110`: `@sinclair/typebox` is aliased to
  Pi's bundled typebox, so the fork's import resolves without a dependency.
- Runtime bare imports actually used (rg over `.pi` + `packages/*/src`):
  `effect`, `absurd-sdk`, `croner`, `acorn`, `yaml`, `typebox`, `typescript`,
  `@pi-swarm/core`, `@earendil-works/pi-*`. All but `typescript` are already
  declared as `dependencies`.
- Packages consumed from `dist/` at runtime: `@pi-swarm/core` (by schedule,
  mcp) and `@pi-swarm/runtime-contracts` (by agents). Everything else is
  imported as `src/`.

## Commits

### 1. `refactor(tools): fork pi-ask-user into .pi/extensions/30-tools/ask-user`

- `git submodule deinit -f vendor/pi-ask-user && git rm vendor/pi-ask-user`;
  drop its `.gitmodules` entry; `rm -rf .git/modules/pi-ask-user`.
- New `.pi/extensions/30-tools/ask-user/` with `index.ts`,
  `single-select-layout.ts`, `LICENSE` (MIT, Enzo Lucchesi), and
  `skills/ask-user/` (SKILL.md + reference), copied from the submodule at
  705fdc6 (v0.15.0). Each `.ts` gets a provenance header naming the upstream
  repo, tag, commit, and license.
- Fork edits, all in `index.ts`: replace the `createRequire`/`package.json`
  version lookup (`:37-39`) with `const ASK_USER_VERSION = "0.15.0+pi-swarm"`;
  nothing else changes.
- Delete the adapter `.pi/extensions/30-tools/pi-ask-user.ts`; layer manifest
  entry becomes `ask-user/index.ts` at the same position.
- Upstream tests are `bun:test` (3.2k lines) and are not ported in this
  change; recorded as a follow-up. `single-select-layout.test.ts` (108 lines,
  pure function) is ported to vitest at `.pi/test/tools/ask-user-layout.test.ts`.
- AGENTS.md: vendor row, extension inventory, capability locator (`ask-user`
  skill row → new path), and the 30-tools description.

Gate: `npm test`; parity probe tool list == baseline 28 + `vault` (the
`ask_user_question` name is interactive-only and gated the same as before);
`git submodule status` shows only `vendor/opencode`;
`rg -n 'pi-ask-user' --hidden -g '!vendor/**' -g '!docs/plans/**'` lists only
provenance headers and the LICENSE.

### 2. `feat(install): root pi manifest, prepare build, runtime deps`

Root `package.json`:

```json
"keywords": ["pi-package"],
"pi": {
  "extensions": [
    ".pi/extensions/00-runtime", ".pi/extensions/10-context",
    ".pi/extensions/20-policy",  ".pi/extensions/30-tools",
    ".pi/extensions/40-state",   ".pi/extensions/50-ui"
  ],
  "themes": [".pi/themes"]
},
"scripts": {
  "prepare": "npm run build:runtime",
  "build:runtime": "npm run build -w @pi-swarm/runtime-contracts && npm run build -w @pi-swarm/core",
  "doctor": "node tools/install/doctor.mjs",
  ...
},
"dependencies": { "typescript": "^5.9.0", "@types/node": "^22.0.0" }
```

`typescript`/`@types/node` are removed from `devDependencies` (single
declaration). `prepare` builds only what is consumed from `dist/`; the full
`npm run build` still needs `@types/pg` and stays a dev command. The
`workspaces` array is unchanged so `npm install --omit=dev` links
`@pi-swarm/*` and hoists `effect`, `croner`, `absurd-sdk`, `acorn`, `yaml`.

`.gitignore` gains `.pi/git/` and `.pi/npm/` (Pi's project-scope install
dirs) so a stray `pi install -l` never gets committed.

Gate: in a temp dir, `git clone /home/swarm/Work/Pi-Swarm clone &&
cd clone && npm install --omit=dev` succeeds and leaves
`packages/runtime/{core,runtime-contracts}/dist/index.js`; `ls node_modules |
grep -c '^vitest$'` == 0 (dev really omitted); `node -e
'import("typescript")'` and `import("effect")` resolve from the clone.

### 3. `feat(install): doctor check for a global Pi-Swarm install`

`tools/install/doctor.mjs` (node ≥ 22 built-ins only; no deps), run as
`npm run doctor` or `node tools/install/doctor.mjs [--agent-dir ~/.pi/agent]`.
Each check prints `ok`/`FAIL` with a one-line fix and exits non-zero on any
FAIL:

1. `node` ≥ 22 and `pi` ≥ 0.85 resolvable from `PATH` of a **non-interactive**
   shell (`bash -lc` vs `bash -c` both checked; reports the Homebrew path when
   only the login shell finds it — Mac item 4).
2. `packages/runtime/{core,runtime-contracts}/dist/index.js` present
   (otherwise "run `npm install`").
3. `import()` of every runtime bare dep from the repo root: `effect`,
   `absurd-sdk`, `croner`, `acorn`, `yaml`, `typescript` (Mac item 3).
4. `.pi/extensions/30-tools/ask-user/index.ts` present and `vendor/` not
   required at runtime (`rg`-free: checks the one path).
5. `~/.pi/agent/models.json`: every model has `maxTokens > 0` and a
   `contextWindow` (Mac item 5); reports provider/model ids only.
6. `~/.pi/agent/settings.json`: if `theme` names a `swarm-*` theme, the file
   exists in `.pi/themes/` and the repo is present in `packages[]` (Mac item 6).
7. `pi list` includes this repo (local path or git identity).

Gate: `npm run doctor` passes on this machine; a deliberately broken scratch
`--agent-dir` (models.json with `maxTokens: 0`, no packages entry) fails
checks 5 and 7 and nothing else.

### 4. `docs(install): global install and update procedure`

AGENTS.md gains an "Installing globally" subsection under Validation:

```sh
# dev box — link this checkout, no copy; dedupes against .pi/ by path
pi install /home/swarm/Work/Pi-Swarm
# any other machine — once a remote exists
pi install git:github.com/cloverinternational/swarm-pi@<tag>      # pinned; `pi update` reconciles the ref
npm run doctor                                   # from the installed checkout
```

plus the two rules that fall out of the evidence: never `pi install git:…`
on a machine that also opens this repo as a project (different absolute
paths → duplicate registration; use the local-path form there), and
`prepare` is the only build a global install gets, so anything consumed from
`dist/` must be listed in `build:runtime`. The Mac items 7 (tmux) and 8
(Plexus key scope) are noted as host configuration outside the package.

## End-to-end verification (after commit 4)

```sh
T=$(mktemp -d); git clone -q /home/swarm/Work/Pi-Swarm $T/clone
(cd $T/clone && npm install --omit=dev)                  # prepare builds dist
mkdir -p $T/home/.pi/agent; cp ~/.pi/agent/models.json $T/home/.pi/agent/
HOME=$T/home pi install $T/clone                          # writes packages[] to scratch settings
mkdir $T/foreign && cd $T/foreign
HOME=$T/home node /home/swarm/Work/Pi-Swarm/tools/parity/probe.mjs --workspace . --output $T/probe   # real boot
```

Pass criteria: probe `pi.json` has no `Cannot find module`; registered tool
names ⊇ baseline 28 + `vault` + `ask_user_question`; `codemode` loaded (present
in `pi.json`); theme `swarm-swarmcode` resolvable when set in the scratch
settings; `npm run doctor` (with `--agent-dir $T/home/.pi/agent`) passes.
Then `HOME=$T/home pi remove $T/clone` and `rm -rf $T`.

## Out of scope

- Publishing to npm or creating the GitHub remote.
- Porting `pi-ask-user/index.test.ts` from `bun:test` (follow-up task).
- Mac host items 7 and 8 (tmux launch shell, Plexus key scope).
- Registering the ask-user skill in the prompt (would change a
  parity-compared block).
