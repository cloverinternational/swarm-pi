#!/usr/bin/env node
/**
 * Pi-Swarm install doctor.
 *
 * Checks that a checkout of this repository is usable as a globally installed
 * Pi package (`pi install <path>` / `pi install git:...`). Each check prints
 * `ok` or `FAIL` with a one-line fix; the exit code is non-zero if any check
 * fails. Uses Node built-ins only so it runs before `npm install` has
 * succeeded. Never prints secrets: models.json and settings.json are read for
 * ids, names, and paths only.
 *
 *   node tools/install/doctor.mjs [--agent-dir ~/.pi/agent] [--repo <path>]
 */
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const args = process.argv.slice(2);
const opt = (name, fallback) => { const i = args.indexOf(name); return i === -1 ? fallback : args[i + 1]; };
const REPO = resolve(opt("--repo", resolve(dirname(fileURLToPath(import.meta.url)), "..", "..")));
const AGENT_DIR = resolve(opt("--agent-dir", join(process.env.PI_CODING_AGENT_DIR ?? join(homedir(), ".pi", "agent"))));

const MIN_NODE = 22;
const MIN_PI = [0, 85, 0];
/** Bare imports the extensions need at runtime (rg over .pi and the package src trees). */
const RUNTIME_DEPS = ["typescript", "effect", "absurd-sdk", "croner", "acorn", "yaml", "@pi-swarm/core", "@pi-swarm/runtime-contracts"];
/** Packages consumed from dist/ at runtime; built by the root `prepare` script. */
const DIST_PACKAGES = ["packages/runtime/core", "packages/runtime/runtime-contracts"];

const results = [];
const report = (name, ok, detail, fix) => { results.push({ name, ok, detail, fix }); };
const readJSON = (path) => { try { return JSON.parse(readFileSync(path, "utf8")); } catch { return undefined; } };
const run = (cmd, argv, options = {}) => spawnSync(cmd, argv, { encoding: "utf8", timeout: 20_000, ...options });
const semver = (text) => (text.match(/(\d+)\.(\d+)\.(\d+)/) ?? []).slice(1, 4).map(Number);
const gte = (a, b) => { for (let i = 0; i < b.length; i++) { if ((a[i] ?? 0) > b[i]) return true; if ((a[i] ?? 0) < b[i]) return false; } return true; };

// 1. node and pi on PATH from a NON-interactive shell (what tmux/ssh commands get).
function checkToolchain() {
  const shells = [["bash -c", ["bash", "-c"]], ["bash -lc", ["bash", "-lc"]]];
  for (const tool of ["node", "pi"]) {
    const seen = {};
    for (const [label, sh] of shells) {
      const r = run(sh[0], [...sh.slice(1), `command -v ${tool}`]);
      seen[label] = r.status === 0 ? r.stdout.trim() : "";
    }
    if (!seen["bash -c"] && seen["bash -lc"]) {
      report(`${tool} on non-interactive PATH`, false, `only the login shell finds it (${seen["bash -lc"]})`, `export PATH="${dirname(seen["bash -lc"])}:$PATH" in ~/.bashrc / ~/.zshenv, or launch pi by absolute path in tmux`);
      continue;
    }
    if (!seen["bash -c"]) { report(`${tool} on PATH`, false, "not found", tool === "node" ? "install Node >= 22" : "npm install -g @earendil-works/pi-coding-agent"); continue; }
    const v = semver(run(seen["bash -c"], ["--version"]).stdout ?? "");
    const min = tool === "node" ? [MIN_NODE, 0, 0] : MIN_PI;
    report(`${tool} >= ${min.join(".")}`, gte(v, min), `${v.join(".") || "unknown"} at ${seen["bash -c"]}`, `upgrade ${tool}`);
  }
}

// 2. dist/ for the packages consumed from dist.
function checkDist() {
  for (const pkg of DIST_PACKAGES) {
    const file = join(REPO, pkg, "dist", "index.js");
    report(`${pkg}/dist built`, existsSync(file), file, "run `npm install` in the checkout (its prepare script builds these)");
  }
}

// 3. every runtime bare dependency is present in the checkout's hoisted node_modules
//    (what Pi's jiti loader resolves against). Checked on disk, without executing anything.
function checkRuntimeDeps() {
  for (const dep of RUNTIME_DEPS) {
    const dir = join(REPO, "node_modules", ...dep.split("/"));
    const ok = existsSync(join(dir, "package.json"));
    report(`dependency ${dep}`, ok, ok ? dir : "not in node_modules", "run `npm install --omit=dev` in the checkout; runtime deps must be in `dependencies`, not `devDependencies`");
  }
}

// 4. no runtime import crosses into vendor/ (the installer never initialises submodules).
function checkVendorFree() {
  const fork = join(REPO, ".pi", "extensions", "30-tools", "ask-user", "index.ts");
  report("ask_user_question is in-tree (not vendor/)", existsSync(fork), fork, "the fork is missing; update the checkout");
}

// 5. models.json sanity (ids only).
function checkModels() {
  const path = join(AGENT_DIR, "models.json");
  const models = readJSON(path);
  if (!models) { report("models.json", true, "absent or unreadable (built-in providers only)"); return; }
  const bad = [];
  for (const [provider, cfg] of Object.entries(models.providers ?? {})) {
    for (const m of cfg?.models ?? []) {
      if (!(Number(m.maxTokens) > 0)) bad.push(`${provider}/${m.id}: maxTokens=${m.maxTokens}`);
      if (!(Number(m.contextWindow) > 0)) bad.push(`${provider}/${m.id}: contextWindow=${m.contextWindow}`);
    }
  }
  report("models.json model metadata", bad.length === 0, bad.length ? bad.join("; ") : path, "set maxTokens and contextWindow to positive integers for each listed model");
}

// 6/7. settings.json: theme resolvable, repo listed as a package.
function checkSettings() {
  const path = join(AGENT_DIR, "settings.json");
  const settings = readJSON(path) ?? {};
  const theme = settings.theme;
  if (typeof theme === "string" && theme.startsWith("swarm-")) {
    const file = join(REPO, ".pi", "themes", `${theme}.json`);
    report(`theme ${theme} shipped by this checkout`, existsSync(file), file, "pick a theme from .pi/themes or install this repo as a package so its themes load");
  } else report("theme", true, theme ? `${theme} (not a swarm-* theme)` : "default");
  const packages = (settings.packages ?? []).map((p) => (typeof p === "string" ? p : p?.source ?? "")).filter(Boolean);
  const listed = packages.some((p) => resolve(dirname(path), p) === REPO || p === REPO || /pi-swarm/i.test(p));
  report("this checkout is in settings.packages", listed, listed ? packages.find((p) => resolve(dirname(path), p) === REPO || /pi-swarm/i.test(p)) : `packages: ${packages.length ? packages.join(", ") : "(none)"}`, `pi install ${REPO}   # or pi install git:github.com/cloverinternational/swarm-pi@<tag>`);
}

checkToolchain();
checkDist();
checkRuntimeDeps();
checkVendorFree();
checkModels();
checkSettings();

const width = Math.max(...results.map((r) => r.name.length));
for (const r of results) {
  console.log(`${r.ok ? "ok  " : "FAIL"} ${r.name.padEnd(width)}  ${r.detail ?? ""}`);
  if (!r.ok && r.fix) console.log(`     fix: ${r.fix}`);
}
const failed = results.filter((r) => !r.ok).length;
console.log(`\n${results.length - failed}/${results.length} checks passed (repo ${REPO}, agent dir ${AGENT_DIR})`);
process.exit(failed ? 1 : 0);
