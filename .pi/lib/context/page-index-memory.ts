import { createHash } from "node:crypto";

export const CONTEXT_SOURCE_ENTRY = "pi-swarm-context-source";
export const CONTEXT_TOMBSTONE_ENTRY = "pi-swarm-context-tombstone";
export const CONTEXT_VERSION = 1;

export interface ContextScope { namespace: string; workspace: string; session: string; }
export interface ContextNode { title: string; nodeId: string; line: number; text: string; summary?: string; children: ContextNode[]; }
export interface ContextCitation { sourceId: string; title: string; line: number; nodeId: string; contentHash: string; }
export interface ContextSource { id: string; version: number; scope: ContextScope; name: string; path?: string; contentHash: string; indexedAt: string; status: "indexed" | "deleted"; lineCount: number; summarizedAt?: string; description?: string; raw: string; tree: ContextNode[]; }
export interface ContextEntry { type: typeof CONTEXT_SOURCE_ENTRY; data: ContextSource; }
export interface ContextTombstone { type: typeof CONTEXT_TOMBSTONE_ENTRY; data: { id: string; version: number; deletedAt: string; scope: ContextScope; }; }

const secretPatterns = [/(sk-[A-Za-z0-9_-]{12,})/g, /(gh[pousr]_[A-Za-z0-9_]{20,})/g, /(xai-[A-Za-z0-9_-]{12,})/g, /((?:api[_-]?key|token|secret|password)\s*[:=]\s*)([^\s,;]+)/gi, /(Bearer\s+)[A-Za-z0-9._~+\/-]{12,}/gi];
export function redactContext(text: string): string { let value = String(text); for (const [i, pattern] of secretPatterns.entries()) value = value.replace(pattern, i === 3 || i === 4 ? "$1[REDACTED]" : "[REDACTED]"); return value; }
export function scopeOf(input: Partial<ContextScope> = {}, cwd = process.cwd()): ContextScope { const clean = (v: string | undefined, fallback: string) => (v ?? fallback).trim().replace(/[\\/]+/g, "/") || fallback; return { namespace: clean(input.namespace, "default"), workspace: clean(input.workspace, cwd), session: clean(input.session, "current") }; }
function sameScope(a: ContextScope, b: ContextScope): boolean { return a.namespace === b.namespace && a.workspace === b.workspace && a.session === b.session; }
export function hashContent(text: string): string { return createHash("sha256").update(text.replace(/\r\n?/g, "\n")).digest("hex"); }
export const EXCERPT_LIMIT = 1200;
/** Page-Index reuses a short leaf's own text as its summary rather than paying for
 * a model call (vendor/page-index/pageindex/utils.py:761). Same rule, chars not tokens. */
export const SUMMARY_RAW_TEXT_CHARS = 800;
/** Ingest caps. A context source is attacker-influenced input (a file in the
 * workspace, or text an agent was told to remember), so its cost is bounded
 * before it reaches the index, the summary model, or the session log. */
export const MAX_SOURCE_CHARS = 1_000_000;
export const MAX_SOURCE_NODES = 2_000;
/** Summaries are model output over untrusted text; they are bounded so a source
 * cannot make its own outline entry arbitrarily large. */
export const SUMMARY_LIMIT = 600;
function boundExcerpt(text: string, limit: number): string { return text.length <= limit ? text : text.slice(0, limit) + "\n… [truncated]"; }

export function indexMarkdown(markdown: string): { lineCount: number; contentHash: string; tree: ContextNode[] } {
  const normalized = markdown.replace(/\r\n?/g, "\n"); const lines = normalized.split("\n");
  const headings: Array<{ title: string; level: number; line: number }> = []; let fenced = false;
  lines.forEach((raw, i) => { const value = raw.trim(); if (value.slice(0, 3) === String.fromCharCode(96, 96, 96)) { fenced = !fenced; return; } if (fenced || !value) return; const match = /^(#{1,6})\s+(.+)$/.exec(value); if (match) { headings.push({ title: match[2].trim(), level: match[1].length, line: i + 1 }); return; } const bold = /^\*\*(.+?)\*\*\s*$/.exec(value); if (bold && bold[1].trim()) headings.push({ title: bold[1].trim(), level: 1, line: i + 1 }); });
  const flat = headings.map((heading, i) => ({ ...heading, text: lines.slice(heading.line - 1, i + 1 < headings.length ? headings[i + 1].line - 1 : lines.length).join("\n").trim() }));
  const roots: ContextNode[] = []; const stack: Array<{ level: number; node: ContextNode }> = [];
  flat.forEach((item, i) => { const node: ContextNode = { title: item.title, nodeId: String(i + 1).padStart(4, "0"), line: item.line, text: item.text, children: [] }; while (stack.length && stack[stack.length - 1].level >= item.level) stack.pop(); if (stack.length) stack[stack.length - 1].node.children.push(node); else roots.push(node); stack.push({ level: item.level, node }); });
  return { lineCount: lines.length, contentHash: hashContent(normalized), tree: roots };
}
function flatten(nodes: ContextNode[]): ContextNode[] { return nodes.flatMap(node => [node, ...flatten(node.children)]); }
/** Parses and bounds one source, rejecting inputs that would blow up the index. */
function parseBounded(markdown: string): { lineCount: number; contentHash: string; tree: ContextNode[] } {
  if (markdown.length > MAX_SOURCE_CHARS) throw new Error("Context source is too large (" + markdown.length + " chars, limit " + MAX_SOURCE_CHARS + "). Index a smaller file or split it.");
  const parsed = indexMarkdown(markdown);
  const nodes = flatten(parsed.tree).length;
  if (nodes > MAX_SOURCE_NODES) throw new Error("Context source has too many sections (" + nodes + ", limit " + MAX_SOURCE_NODES + "). Split it into smaller documents.");
  return parsed;
}
function sourceId(scope: ContextScope, name: string, contentHash: string): string { return createHash("sha256").update(JSON.stringify([scope, name, contentHash])).digest("hex").slice(0, 24); }

export class ContextIndex {
  private sources = new Map<string, ContextSource>(); private deleted = new Set<string>();
  constructor(private readonly now: () => Date = () => new Date()) {}
  load(entries: unknown[]): void { this.sources.clear(); this.deleted.clear(); for (const entry of entries as any[]) { if (entry?.type === CONTEXT_SOURCE_ENTRY && entry.data?.version === CONTEXT_VERSION) this.sources.set(entry.data.id, entry.data); if (entry?.type === CONTEXT_TOMBSTONE_ENTRY && entry.data?.version === CONTEXT_VERSION) { this.deleted.add(entry.data.id); this.sources.delete(entry.data.id); } } }
  index(name: string, markdown: string, scopeInput: Partial<ContextScope>, path?: string): ContextEntry { const scope = scopeOf(scopeInput); const clean = redactContext(markdown); if (!clean.trim()) throw new Error("Context source must not be empty"); const parsed = parseBounded(clean); const data: ContextSource = { id: sourceId(scope, name, parsed.contentHash), version: CONTEXT_VERSION, scope, name: redactContext(name).trim() || "untitled", path, contentHash: parsed.contentHash, indexedAt: this.now().toISOString(), status: "indexed", lineCount: parsed.lineCount, raw: clean, tree: parsed.tree }; this.sources.set(data.id, data); this.deleted.delete(data.id); return { type: CONTEXT_SOURCE_ENTRY, data }; }
  retrieve(query: string, scopeInput: Partial<ContextScope>, limit = 5): Array<{ excerpt: string; citation: ContextCitation }> { const scope = scopeOf(scopeInput); const terms = query.toLocaleLowerCase().split(/\s+/).filter(Boolean); return [...this.sources.values()].filter(source => source.status === "indexed" && !this.deleted.has(source.id) && sameScope(source.scope, scope)).flatMap(source => flatten(source.tree).map(node => ({ source, node, score: terms.filter(term => (node.title + " " + node.text).toLocaleLowerCase().includes(term)).length }))).filter(item => !terms.length || item.score > 0).sort((a, b) => b.score - a.score || a.node.line - b.node.line).slice(0, Math.max(0, limit)).map(item => ({ excerpt: boundExcerpt(item.node.text, EXCERPT_LIMIT), citation: { sourceId: item.source.id, title: item.source.name + " · " + item.node.title, line: item.node.line, nodeId: item.node.nodeId, contentHash: item.source.contentHash } })); }
  /** Replaces a source's content in place, keeping its id so notes can accumulate. */
  update(id: string, markdown: string): ContextEntry {
    const source = this.sources.get(id);
    if (!source) throw new Error("Context source not found: " + id);
    const clean = redactContext(markdown);
    if (!clean.trim()) throw new Error("Context source must not be empty");
    const parsed = parseBounded(clean);
    const data: ContextSource = { ...source, contentHash: parsed.contentHash, indexedAt: this.now().toISOString(), lineCount: parsed.lineCount, raw: clean, tree: parsed.tree, summarizedAt: undefined, description: undefined };
    this.sources.set(id, data);
    return { type: CONTEXT_SOURCE_ENTRY, data };
  }
  /** Attaches bottom-up summaries. Nodes keep any summary they already carry. */
  applySummaries(id: string, summaries: Map<string, string>, description?: string): ContextEntry {
    const source = this.sources.get(id);
    if (!source) throw new Error("Context source not found: " + id);
    const clean = (text: string | undefined): string | undefined => { if (text === undefined) return undefined; const value = redactContext(text).trim(); return value ? value.slice(0, SUMMARY_LIMIT) : undefined; };
    const attach = (nodes: ContextNode[]): ContextNode[] => nodes.map(node => ({ ...node, summary: node.summary ?? clean(summaries.get(node.nodeId)), children: attach(node.children) }));
    const data: ContextSource = { ...source, tree: attach(source.tree), description: source.description ?? clean(description), summarizedAt: this.now().toISOString() };
    this.sources.set(id, data);
    return { type: CONTEXT_SOURCE_ENTRY, data };
  }
  source(id: string): ContextSource | undefined { return this.sources.get(id); }
  /** Reports sources whose backing file content no longer matches the indexed hash. */
  staleness(readFile: (path: string) => string | undefined): Array<{ id: string; path: string; state: "current" | "stale" | "missing" }> { return [...this.sources.values()].filter(source => typeof source.path === "string" && !this.deleted.has(source.id)).map(source => { const current = readFile(source.path as string); return { id: source.id, path: source.path as string, state: current === undefined ? "missing" as const : hashContent(redactContext(current)) === source.contentHash ? "current" as const : "stale" as const }; }); }
  inspect(scopeInput: Partial<ContextScope>): ContextSource[] { const scope = scopeOf(scopeInput); return [...this.sources.values()].filter(source => sameScope(source.scope, scope) && !this.deleted.has(source.id)); }
  delete(id: string): ContextTombstone { const source = this.sources.get(id); if (!source) throw new Error("Context source not found: " + id); this.sources.delete(id); this.deleted.add(id); return { type: CONTEXT_TOMBSTONE_ENTRY, data: { id, version: CONTEXT_VERSION, deletedAt: this.now().toISOString(), scope: source.scope } }; }
}
