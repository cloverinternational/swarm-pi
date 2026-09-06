#!/usr/bin/env node
/**
 * Rewrite relative module specifiers after files or directories move.
 *
 * Usage:
 *   node tools/repo/rewrite-imports.mjs --map moves.json [--check] [--dry-run]
 *
 * moves.json is `{ "<old path>": "<new path>", ... }` (repo-relative). Keys and
 * values may be files or directories; a directory entry maps every descendant.
 *
 * For every TypeScript / JavaScript file that lives under a *new* path, each
 * relative specifier (`from`, dynamic `import()`, and `new URL(..., import.meta.url)` forms)
 * is resolved from the file's OLD location to an absolute target, that target is
 * mapped through the move table, and the specifier is re-emitted relative to the
 * file's NEW location. Files that did not move but point at moved targets are
 * handled the same way (old location == new location).
 *
 * --check   after rewriting (or standalone), fail if any relative specifier in
 *           the scanned files does not resolve to an existing path
 *           (with .ts/.js/.mjs/index resolution).
 * --dry-run print the rewrites without writing.
 *
 * Template-literal specifiers are reported and left untouched.
 */
import { readFileSync, writeFileSync, existsSync, statSync, readdirSync } from "node:fs";
import { resolve, relative, dirname, join, sep, posix } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(fileURLToPath(new URL("../..", import.meta.url)));
const args = process.argv.slice(2);
const opt = (name) => { const i = args.indexOf(name); return i === -1 ? undefined : args[i + 1]; };
const has = (name) => args.includes(name);

const mapFile = opt("--map");
const moves = mapFile ? JSON.parse(readFileSync(resolve(ROOT, mapFile), "utf8")) : {};
const CHECK = has("--check");
const DRY = has("--dry-run");
const SKIP_DIRS = new Set(["node_modules", "dist", ".git", "vendor", "artifacts", ".swarm", "agent-sessions"]);
const EXT = /\.(ts|mts|cts|js|mjs|cjs)$/;

// Sort longest-old-path first so nested entries win over their parents.
const moveEntries = Object.entries(moves)
  .map(([o, n]) => [resolve(ROOT, o), resolve(ROOT, n)])
  .sort((a, b) => b[0].length - a[0].length);

function mapPath(abs) {
  for (const [o, n] of moveEntries) {
    if (abs === o) return n;
    if (abs.startsWith(o + sep)) return n + abs.slice(o.length);
  }
  return abs;
}
// Inverse: where did this (new) file live before the move?
const inverseEntries = moveEntries.map(([o, n]) => [n, o]).sort((a, b) => b[0].length - a[0].length);
function unmapPath(abs) {
  for (const [n, o] of inverseEntries) {
    if (abs === n) return o;
    if (abs.startsWith(n + sep)) return o + abs.slice(n.length);
  }
  return abs;
}

function* walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (SKIP_DIRS.has(entry.name)) continue;
    const p = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(p);
    else if (entry.isFile() && EXT.test(entry.name)) yield p;
  }
}

function resolvesToSomething(abs) {
  if (existsSync(abs)) return true;
  for (const ext of [".ts", ".mts", ".js", ".mjs", ".d.ts"]) if (existsSync(abs + ext)) return true;
  for (const idx of ["index.ts", "index.js", "index.mjs"]) if (existsSync(join(abs, idx))) return true;
  // ".js" specifiers under NodeNext may point at ".ts" sources.
  if (abs.endsWith(".js") && existsSync(abs.slice(0, -3) + ".ts")) return true;
  return false;
}

const SPEC_RE = /(\bfrom\s*|\bimport\s*\(\s*|\bnew URL\s*\(\s*)(["'`])(\.\.?\/[^"'`\n]*)\2/g;

let rewritten = 0, unresolved = 0, templates = 0;
for (const file of walk(ROOT)) {
  const src = readFileSync(file, "utf8");
  const oldFile = unmapPath(file);
  let out = src;
  const changes = [];
  out = src.replace(SPEC_RE, (m, lead, quote, spec) => {
    if (quote === "`" && spec.includes("${")) { templates++; console.warn(`TEMPLATE  ${relative(ROOT, file)}: ${spec}`); return m; }
    const targetOld = resolve(dirname(oldFile), spec);
    const targetNew = mapPath(targetOld);
    let next = relative(dirname(file), targetNew).split(sep).join(posix.sep);
    if (!next.startsWith(".")) next = "./" + next;
    if (spec.endsWith("/") && !next.endsWith("/")) next += "/";
    if (next === spec) return m;
    changes.push(`${spec} -> ${next}`);
    return `${lead}${quote}${next}${quote}`;
  });
  if (changes.length) {
    rewritten += changes.length;
    console.log(`${relative(ROOT, file)}\n  ${changes.join("\n  ")}`);
    if (!DRY) writeFileSync(file, out);
  }
  if (CHECK) {
    for (const m of out.matchAll(SPEC_RE)) {
      const spec = m[3];
      if (spec.includes("${")) continue;
      const abs = resolve(dirname(file), spec);
      if (!resolvesToSomething(abs)) { unresolved++; console.error(`UNRESOLVED ${relative(ROOT, file)}: ${spec}`); }
    }
  }
}
console.log(`\n${rewritten} specifier(s) rewritten${DRY ? " (dry run)" : ""}; ${templates} template literal(s) skipped; ${unresolved} unresolved.`);
if (CHECK && unresolved) process.exit(1);
