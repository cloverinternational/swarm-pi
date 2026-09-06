/**
 * Port of the Swarm TUI context orchestrator's prompt output.
 *
 * Source of truth (swarm-sdk @ ff0309c7):
 *   swarm-tui/internal/chat/context/types.go        builtinSources
 *   swarm-tui/internal/chat/context/orchestrator.go  resolveFileSourcePath,
 *     loadSourceContent, getGitStatus, renderSources, trimSourceContent,
 *     applyBudget, truncateWithNote, block assembly (Refresh)
 *   swarm-tui/internal/chat/context/injection.go     InjectContextBlocks
 *
 * Every literal string and newline below mirrors the Go implementation so the
 * bytes Pi sends match `swarm -p`. Only the default source set is modelled
 * (no MCP sources, no user overrides, no nested INDEX.md tracking).
 */
import { execFileSync } from "node:child_process";
import { existsSync, openSync, readSync, closeSync, readFileSync, statSync } from "node:fs";
import { basename, join, resolve } from "node:path";

export const CACHED_START = "<swarmos_cached_context>";
export const CACHED_END = "</swarmos_cached_context>";
export const EPHEMERAL_START = "<swarmos_context>";
export const EPHEMERAL_END = "</swarmos_context>";

export const DEFAULT_MAX_SOURCE_CHARS = 4000;
export const DEFAULT_MAX_SOURCE_LINES = 200;
export const DEFAULT_MAX_TOTAL_CHARS = 20000;

export type CachePolicy = "cached" | "ephemeral";
export interface SourceSpec { id: string; name: string; enabled: boolean; cache: CachePolicy }

/** types.go builtinSources, in declaration order; injection-kind sources omitted (never rendered). */
export const BUILTIN_SOURCES: readonly SourceSpec[] = [
  { id: "global_claude_md", name: "globalClaudeMd", enabled: false, cache: "cached" },
  { id: "global_swarm_md", name: "globalSwarmMd", enabled: false, cache: "cached" },
  { id: "project_claude_md", name: "claudeMd", enabled: true, cache: "cached" },
  { id: "project_swarm_md", name: "swarmMd", enabled: true, cache: "cached" },
  { id: "agents_md", name: "agentsMd", enabled: true, cache: "cached" },
  { id: "index_md", name: "indexMd", enabled: false, cache: "cached" },
  { id: "git_status", name: "gitStatus", enabled: true, cache: "ephemeral" },
  { id: "project_name", name: "projectName", enabled: true, cache: "cached" },
  { id: "current_date", name: "currentDate", enabled: true, cache: "ephemeral" },
];

export interface ContextOptions {
  workDir: string;
  home?: string;
  now?: () => Date;
  maxSourceChars?: number;
  maxSourceLines?: number;
  maxTotalChars?: number;
  /** Override enabled flags by source id (mirrors user config). */
  enabled?: Partial<Record<string, boolean>>;
}

// Go strings.TrimSpace trims Unicode whitespace; JS trim() is a superset that
// also strips \uFEFF. Files in scope never start with a BOM, so equivalent.
const trimSpace = (value: string) => value.trim();

function candidatePaths(id: string, workDir: string, home: string): string[] {
  switch (id) {
    case "global_claude_md": return [join(home, ".claude", "CLAUDE.md")];
    case "global_swarm_md": return [join(home, ".swarm", "SWARM.md")];
    case "project_claude_md": return [join(workDir, ".claude", "CLAUDE.md"), join(workDir, "CLAUDE.md")];
    case "project_swarm_md": return [join(workDir, ".swarm", "SWARM.md"), join(workDir, "SWARM.md")];
    case "agents_md": return [join(workDir, ".swarm", "AGENTS.md"), join(workDir, ".claude", "AGENTS.md"), join(workDir, "AGENTS.md")];
    case "index_md": return [join(workDir, "INDEX.md")];
    default: return [];
  }
}

/** First existing candidate path for a file source, or undefined. */
export function candidateContextPath(id: string, workDir: string, home = process.env.HOME ?? ""): string | undefined {
  return candidatePaths(id, workDir, home).find(candidate => { try { statSync(candidate); return true; } catch { return false; } });
}

function readFileTrimmedLimited(path: string, maxBytes: number): string {
  const fd = openSync(path, "r");
  try {
    const buf = Buffer.alloc(maxBytes);
    const n = readSync(fd, buf, 0, maxBytes, 0);
    return trimSpace(buf.subarray(0, n).toString("utf8"));
  } finally { closeSync(fd); }
}

function git(workDir: string, args: string[]): string | undefined {
  try {
    return execFileSync("git", args, { cwd: workDir, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
  } catch { return undefined; }
}

/** orchestrator.go getGitStatus — byte-exact, including trailing newline. */
export function gitStatus(workDir: string): string {
  if (!existsSync(join(workDir, ".git"))) return "";
  let result = "";
  result += "Working Directory: " + resolve(workDir) + "\n\n";
  const branchOutput = git(workDir, ["branch", "--show-current"]);
  if (branchOutput === undefined) return "";
  result += "Current branch: " + trimSpace(branchOutput) + "\n";
  const commitsOutput = git(workDir, ["log", "--oneline", "-5", "--format=%h %s"]);
  if (commitsOutput !== undefined) {
    const commits = trimSpace(commitsOutput);
    if (commits !== "") result += "\nLast 5 commits:\n" + commits + "\n";
  }
  const statusOutput = git(workDir, ["status", "--short"]);
  if (statusOutput === undefined) return result;
  const status = trimSpace(statusOutput);
  if (status !== "") result += "\nStatus:\n" + status + "\n";
  const stagedOutput = git(workDir, ["diff", "--cached", "--name-status"]);
  if (stagedOutput !== undefined) {
    const staged = trimSpace(stagedOutput);
    if (staged !== "") result += "\nStaged files:\n" + staged + "\n";
  }
  return result;
}

/** orchestrator.go loadSourceContent for non-MCP sources. */
export function loadSourceContent(spec: SourceSpec, options: ContextOptions): string {
  const home = options.home ?? process.env.HOME ?? "";
  const now = options.now ?? (() => new Date());
  switch (spec.id) {
    case "global_claude_md": case "global_swarm_md": case "project_claude_md":
    case "project_swarm_md": case "agents_md": case "index_md": {
      const path = candidatePaths(spec.id, options.workDir, home).find(candidate => { try { statSync(candidate); return true; } catch { return false; } });
      if (!path) return "";
      try {
        return spec.id === "index_md" ? readFileTrimmedLimited(path, DEFAULT_MAX_SOURCE_CHARS) : trimSpace(readFileSync(path, "utf8"));
      } catch { return ""; }
    }
    case "git_status": return gitStatus(options.workDir);
    case "project_name": {
      const name = basename(options.workDir);
      return name === "" || name === "." ? "" : name;
    }
    case "current_date": {
      const d = now();
      return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
    }
    default: return "";
  }
}

/** orchestrator.go trimSourceContent. Byte-based like Go's len()/slicing. */
export function trimSourceContent(content: string, maxChars: number, maxLines: number): { text: string; truncated: boolean } {
  let truncated = false;
  let trimmed = content.replace(/\n+$/, "");
  if (maxLines > 0) {
    const lines = trimmed.split("\n");
    if (lines.length > maxLines) { trimmed = lines.slice(0, maxLines).join("\n"); truncated = true; }
  }
  if (maxChars > 0 && Buffer.byteLength(trimmed) > maxChars) {
    trimmed = Buffer.from(trimmed).subarray(0, maxChars).toString("utf8");
    truncated = true;
  }
  return { text: trimmed.replace(/\n+$/, ""), truncated };
}

/** orchestrator.go renderSources (no error note path). */
export function renderSources(sources: Array<{ name: string; content: string }>, maxSourceChars: number, maxSourceLines: number): string {
  if (sources.length === 0) return "";
  let b = "As you answer the user's questions, you can use the following context:\n";
  for (const src of sources) {
    if (trimSpace(src.content) === "") continue;
    const { text, truncated } = trimSourceContent(src.content, maxSourceChars, maxSourceLines);
    b += "<context name=\"" + src.name + "\">\n" + text;
    if (truncated) b += "\n... (truncated)";
    b += "\n</context>\n";
  }
  return trimSpace(b);
}

function truncateWithNote(content: string, maxChars: number, note: string): string {
  const len = Buffer.byteLength(content);
  if (maxChars <= 0 || len <= maxChars) return content;
  if (Buffer.byteLength(note) >= maxChars) return Buffer.from(note).subarray(0, maxChars).toString("utf8");
  return Buffer.from(content).subarray(0, maxChars - Buffer.byteLength(note)).toString("utf8") + note;
}

/** orchestrator.go applyBudget. */
export function applyBudget(content: string, remaining: number): [string, number] {
  if (remaining <= 0 || trimSpace(content) === "") return ["", remaining < 0 ? 0 : remaining];
  const len = Buffer.byteLength(content);
  if (len <= remaining) return [content, remaining - len];
  return [truncateWithNote(content, remaining, "\n... (context truncated)"), 0];
}

/** orchestrator.go Refresh → returns the joined cached+ephemeral block (may be ""). */
export function buildContextBlock(options: ContextOptions): { cached: string; ephemeral: string; block: string } {
  const maxSourceChars = options.maxSourceChars && options.maxSourceChars > 0 ? options.maxSourceChars : DEFAULT_MAX_SOURCE_CHARS;
  const maxSourceLines = options.maxSourceLines && options.maxSourceLines > 0 ? options.maxSourceLines : DEFAULT_MAX_SOURCE_LINES;
  const maxTotalChars = options.maxTotalChars && options.maxTotalChars > 0 ? options.maxTotalChars : DEFAULT_MAX_TOTAL_CHARS;
  const cachedSources: Array<{ name: string; content: string }> = [];
  const ephemeralSources: Array<{ name: string; content: string }> = [];
  for (const spec of BUILTIN_SOURCES) {
    const enabled = options.enabled?.[spec.id] ?? spec.enabled;
    if (!enabled) continue;
    const content = loadSourceContent(spec, options);
    if (trimSpace(content) === "") continue;
    (spec.cache === "ephemeral" ? ephemeralSources : cachedSources).push({ name: spec.name, content });
  }
  let cached = renderSources(cachedSources, maxSourceChars, maxSourceLines);
  let ephemeral = renderSources(ephemeralSources, maxSourceChars, maxSourceLines);
  let remaining = maxTotalChars;
  [cached, remaining] = applyBudget(cached, remaining);
  if (remaining > 0) [ephemeral, remaining] = applyBudget(ephemeral, remaining);
  else ephemeral = "";
  const blocks: string[] = [];
  if (trimSpace(cached) !== "") blocks.push(CACHED_START + "\n" + cached + "\n" + CACHED_END);
  if (trimSpace(ephemeral) !== "") blocks.push(EPHEMERAL_START + "\n" + ephemeral + "\n" + EPHEMERAL_END);
  return { cached, ephemeral, block: trimSpace(blocks.join("\n\n")) };
}

function stripTagBlock(input: string, startTag: string, endTag: string): string {
  let updated = input;
  for (;;) {
    const start = updated.indexOf(startTag);
    if (start === -1) break;
    const endOffset = updated.indexOf(endTag, start + startTag.length);
    if (endOffset === -1) break;
    updated = updated.slice(0, start) + updated.slice(endOffset + endTag.length);
  }
  return updated;
}

/** injection.go StripInjectedContext. */
export function stripInjectedContext(systemPrompt: string): string {
  if (systemPrompt === "") return "";
  return stripTagBlock(stripTagBlock(systemPrompt, CACHED_START, CACHED_END), EPHEMERAL_START, EPHEMERAL_END).replace(/\s+$/, "");
}

function normalizeContextBlock(block: string, startTag: string, endTag: string): string {
  const trimmed = trimSpace(block);
  if (trimmed === "") return "";
  if (trimmed.startsWith(startTag) && trimmed.endsWith(endTag)) return trimmed;
  if (trimmed.includes(startTag) && trimmed.includes(endTag)) return trimmed;
  return startTag + "\n" + trimmed + "\n" + endTag;
}

/** injection.go InjectContextBlocks: base, cached, ephemeral joined by "\n\n". */
export function injectContextBlocks(systemPrompt: string, cachedBlock: string, ephemeralBlock: string): string {
  const base = trimSpace(stripInjectedContext(systemPrompt));
  const cached = normalizeContextBlock(cachedBlock, CACHED_START, CACHED_END);
  const ephemeral = normalizeContextBlock(ephemeralBlock, EPHEMERAL_START, EPHEMERAL_END);
  const parts: string[] = [];
  if (base !== "") parts.push(base);
  if (cached !== "") parts.push(cached);
  if (ephemeral !== "") parts.push(ephemeral);
  return parts.join("\n\n");
}

/** Convenience: apply Swarm's default context injection to a base prompt. */
export function injectSwarmContext(systemPrompt: string, options: ContextOptions): string {
  const { cached, ephemeral } = buildContextBlock(options);
  return injectContextBlocks(systemPrompt, cached, ephemeral);
}
