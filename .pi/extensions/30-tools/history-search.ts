import { createReadStream } from "node:fs";
import { readdir, realpath, stat } from "node:fs/promises";
import { createInterface } from "node:readline";
import { resolve, relative, sep, basename } from "node:path";
import { Text } from "@earendil-works/pi-tui";

export const HISTORY_TOOL = "history_search";
const DEFAULT_MAX_CHARS = 12000;
const DEFAULT_MAX_BYTES = 50 * 1024 * 1024;
const DEFAULT_SCAN_LIMIT = 1000;
const DEFAULT_RESULT_LIMIT = 20;

type Role = "user" | "assistant" | "toolResult" | "bashExecution" | "custom" | "summary" | "any";
export interface HistoryOptions { root?: string; maxBytes?: number; scanLimit?: number; includeCurrent?: boolean; currentSession?: string; }
export interface SearchOptions extends HistoryOptions { query: string; limit?: number; role?: Role; cwd?: string; since?: string; until?: string; includeToolResults?: boolean; }
export interface ReadOptions extends HistoryOptions { session: string; offset?: number; recordLimit?: number; maxCharacters?: number; }
export interface JsonSearchOptions { root?: string; query?: string; path?: string; limit?: number; maxBytes?: number; scanLimit?: number; includeValues?: boolean; }

function textOf(value: any): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.map(textOf).join(" ");
  if (!value || typeof value !== "object") return "";
  return [value.text, value.content, value.display, value.summary, value.compactionSummary, value.branchSummary]
    .filter(x => x !== undefined).map(textOf).join(" ");
}
function roleOf(entry: any): Role {
  const type = entry?.type;
  if (type === "message") return entry.message?.role ?? "custom";
  if (type === "compaction" || type === "branch_summary") return "summary";
  if (type === "bash_execution") return "bashExecution";
  return "custom";
}
function redaction(s: string): string {
  return s.replace(/(sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{20,}|xai-[A-Za-z0-9_-]{12,})/g, "[REDACTED]")
    .replace(/((?:api[_-]?key|token|secret|password)\s*[:=]\s*)[^\s,;]+/gi, "$1[REDACTED]")
    .replace(/(Bearer\s+)[A-Za-z0-9._~+\/-]{12,}/gi, "$1[REDACTED]");
}
function cap(s: string, n: number) { return redaction(s).slice(0, n); }
function inside(root: string, target: string): boolean { const r = relative(root, target); return r === "" || (r !== ".." && !r.startsWith(`..${sep}`) && !r.startsWith("/")); }
async function safeRoot(input?: string): Promise<string> {
  const base = input ?? process.env.PI_CODING_AGENT_DIR ?? `${process.env.HOME ?? process.cwd()}/.pi/agent`;
  return realpath(input ? resolve(base) : resolve(base, "sessions"));
}
async function files(root: string, limit: number): Promise<string[]> {
  const out: string[] = [];
  async function walk(dir: string) { if (out.length >= limit) return; let names: any[] = []; try { names = await readdir(dir, { withFileTypes: true }); } catch { return; }
    // Do not sort: sorting every directory adds latency and is not needed for bounded search.
    for (const item of names) { if (out.length >= limit) break; const p = resolve(dir, item.name); if (item.isDirectory()) await walk(p); else if (item.isFile() && p.endsWith(".jsonl")) out.push(p); }
  }
  await walk(root); return out;
}
async function lines(file: string, maxBytes: number | undefined, cb: (value: any, line: number) => void | Promise<void>) {
  if (maxBytes !== undefined) { const info = await stat(file); if (info.size > maxBytes) throw new Error(`file exceeds maxBytes (${maxBytes})`); }
  const rl = createInterface({ input: createReadStream(file, { encoding: "utf8" }), crlfDelay: Infinity }); let n = 0;
  for await (const line of rl) { n++; try { await cb(JSON.parse(line), n); } catch (e) { if (e instanceof SyntaxError) continue; throw e; } }
}
function match(text: string, query: string) { return query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean).every(q => text.toLocaleLowerCase().includes(q)); }

export class HistorySearchEngine {
  constructor(private readonly opts: HistoryOptions = {}) {}
  async list(options: HistoryOptions = {}) {
    const root = await safeRoot(options.root ?? this.opts.root); const result: any[] = [];
    for (const file of await files(root, options.scanLimit ?? this.opts.scanLimit ?? 1000)) { try { const s = await stat(file); let header: any;
      await lines(file, options.maxBytes ?? this.opts.maxBytes ?? DEFAULT_MAX_BYTES, (x) => { if (!header && x?.type === "session") header = x; });
      if (!options.includeCurrent && options.currentSession && resolve(options.currentSession) === file) continue;
      result.push({ session: file, id: header?.id, cwd: header?.cwd, name: header?.name, updatedAt: s.mtime.toISOString(), bytes: s.size });
    } catch { /* unreadable/oversized files are intentionally skipped */ } }
    return result.sort((a,b) => b.updatedAt.localeCompare(a.updatedAt));
  }
  async search(options: SearchOptions) {
    const root = await safeRoot(options.root ?? this.opts.root); const hits: any[] = [];
    for (const file of await files(root, options.scanLimit ?? this.opts.scanLimit ?? 1000)) { if (!options.includeCurrent && options.currentSession && resolve(options.currentSession) === file) continue; let header: any;
      try { await lines(file, options.maxBytes ?? this.opts.maxBytes ?? DEFAULT_MAX_BYTES, async (entry) => { if (entry?.type === "session") { header = entry; return; } const role = roleOf(entry); if (options.role && options.role !== "any" && role !== options.role) return; if (!options.includeToolResults && ["toolResult", "bashExecution"].includes(role)) return; const when = entry?.timestamp ? new Date(entry.timestamp).getTime() : 0; if (options.since && when < new Date(options.since).getTime() || options.until && when > new Date(options.until).getTime()) return; const text = textOf(entry?.message ?? entry); if (options.cwd && header?.cwd !== options.cwd) return; if (match(text, options.query)) hits.push({ session: file, id: entry.id, parentId: entry.parentId, cwd: header?.cwd, timestamp: entry.timestamp, role, snippet: cap(text, 500) }); }); } catch { /* continue scanning healthy files */ } }
    return hits.sort((a,b) => String(b.timestamp ?? "").localeCompare(String(a.timestamp ?? ""))).slice(0, Math.min(options.limit ?? DEFAULT_RESULT_LIMIT, 100));
  }
  async read(options: ReadOptions) {
    const root = await safeRoot(options.root ?? this.opts.root); let file = resolve(options.session);
    if (!inside(root, file)) { const candidates = (await files(root, options.scanLimit ?? 1000)).filter(x => basename(x) === options.session || x.includes(options.session)); if (candidates.length !== 1) throw new Error("session must be a unique path or id inside the sessions root"); file = candidates[0]; }
    try { file = await realpath(file); } catch { throw new Error("session file does not exist"); }
    if (!inside(root, file)) throw new Error("session path resolves outside the sessions root");
    const records: any[] = []; await lines(file, options.maxBytes ?? this.opts.maxBytes ?? DEFAULT_MAX_BYTES, (x) => { if (x?.type !== "session") records.push(x); });
    const offset = Math.max(0, options.offset ?? 0), selected = records.slice(offset, offset + Math.min(options.recordLimit ?? 80, 200)); let text = "";
    for (const r of selected) { const row = `${r.timestamp ?? ""} ${roleOf(r)} ${r.id ?? ""}\n${textOf(r.message ?? r)}\n\n`; if (text.length + row.length > (options.maxCharacters ?? DEFAULT_MAX_CHARS)) break; text += row; }
    return { session: file, offset, recordLimit: selected.length, nextOffset: offset + selected.length < records.length ? offset + selected.length : null, transcript: cap(text, options.maxCharacters ?? DEFAULT_MAX_CHARS) };
  }
  async jsonSearch(options: JsonSearchOptions) {
    const root = await safeRoot(options.root ?? process.cwd()); const q = options.query?.toLocaleLowerCase(); const wanted = options.path?.split(".").filter(Boolean); const result: any[] = [];
    const candidates = (await files(root, options.scanLimit ?? 10000)).concat(root.endsWith(".jsonl") ? [root] : []);
    for (const file of [...new Set(candidates)]) { if (!inside(root, file) && file !== root) continue; try { await lines(file, options.maxBytes ?? DEFAULT_MAX_BYTES, (value, line) => { let v = value; for (const key of wanted ?? []) v = v?.[key]; const hay = JSON.stringify(v ?? value); if (!q || hay.toLocaleLowerCase().includes(q)) result.push({ file, line, path: options.path ?? "$", value: options.includeValues ? cap(JSON.stringify(v), 4000) : undefined }); }); } catch { /* malformed and oversized files do not stop the corpus scan */ } if (result.length >= Math.min(options.limit ?? 50, 500)) break; }
    return { matches: result.slice(0, Math.min(options.limit ?? 50, 500)), scanned: candidates.length };
  }
}

const schema = { type: "object", required: ["operation"], additionalProperties: false, properties: { operation: { type: "string", enum: ["list", "search", "read", "json_search"] }, query: { type: "string" }, session: { type: "string" }, role: { type: "string" }, cwd: { type: "string" }, since: { type: "string" }, until: { type: "string" }, root: { type: "string" }, path: { type: "string" }, limit: { type: "number" }, offset: { type: "number" }, recordLimit: { type: "number" }, maxCharacters: { type: "number" }, maxBytes: { type: "number" }, includeValues: { type: "boolean" }, includeToolResults: { type: "boolean" }, includeCurrent: { type: "boolean" }, scanLimit: { type: "number" } } };

function pretty(details: any, theme: any, expanded = false): string {
  const fg = (name: string, value: string) => theme?.fg ? theme.fg(name, value) : value;
  const op = details?.matches ? "JSON DATA" : details?.transcript !== undefined ? "TRANSCRIPT" : Array.isArray(details) && details[0]?.session ? "HISTORY" : "HISTORY";
  const rows: string[] = [fg("accent", "+----------------------------------------------------------+"), fg("accent", `| ${op.padEnd(56).slice(0, 56)} |`), fg("muted", "+----------------------------------------------------------+")];
  if (details?.transcript !== undefined) {
    rows.push(fg("dim", `| page ${details.offset ?? 0} | ${details.recordLimit ?? 0} records${details.nextOffset !== null ? " | more available" : ""}`));
    if (expanded || details.transcript.length < 1800) rows.push(...String(details.transcript).split("\\n").slice(0, expanded ? 80 : 24).map((x: string) => `  ${x}`));
    else rows.push(fg("dim", "| ... transcript hidden (expand to inspect)"));
  } else if (Array.isArray(details)) {
    rows.push(fg("dim", `| ${details.length} result${details.length === 1 ? "" : "s"}`));
    for (const item of details.slice(0, expanded ? 30 : 8)) {
      const title = item.name || item.id || item.session?.split(/[\\/]/).pop() || "session";
      rows.push(`| ${fg("success", "+")} ${fg("text", String(title).slice(0, 70))}`);
      if (item.snippet) rows.push(fg("dim", `|   ${String(item.snippet).replace(/\\s+/g, " ").slice(0, expanded ? 176 : 106)}`));
      else if (item.cwd) rows.push(fg("dim", `|   ${item.cwd}`));
    }
    if (details.length > (expanded ? 30 : 8)) rows.push(fg("dim", `| ... ${details.length - (expanded ? 30 : 8)} more`));
  } else if (details?.matches) {
    rows.push(fg("dim", `| ${details.matches.length} match${details.matches.length === 1 ? "" : "es"} | ${details.scanned ?? 0} files scanned`));
    for (const item of details.matches.slice(0, expanded ? 30 : 10)) rows.push(`| ${fg("success", "+")} ${item.file}:${item.line}  ${fg("dim", item.path ?? "$")}${item.value ? `\\n|   ${item.value}` : ""}`);
  }
  rows.push(fg("muted", "+----------------------------------------------------------+"));
  return rows.join("\\n");
}

export default function historySearchExtension(pi: any) { let engine = new HistorySearchEngine(); pi.on?.("session_start", (_e: any, ctx: any) => { const root = ctx?.sessionManager?.getSessionDir?.(); engine = new HistorySearchEngine({ root, currentSession: ctx?.sessionManager?.getSessionFile?.() }); }); pi.registerTool?.({ name: HISTORY_TOOL, label: "History Search", description: "Search prior Pi conversations and stream-search huge JSONL datasets. Read-only, bounded, and redacted. Search first, then read a matching session.", parameters: schema, renderCall(args: any, theme: any) { return new Text(fgCall(args, theme), 0, 0); }, renderResult(result: any, options: any, theme: any) { const value = result?.details; const output = pretty(value, theme, Boolean(options?.expanded)); return new Text(output, 0, 0); }, execute: async (_id: string, p: any) => { try { const result = p.operation === "list" ? await engine.list(p) : p.operation === "read" ? await engine.read(p) : p.operation === "json_search" ? await engine.jsonSearch(p) : await engine.search(p); return { content: [{ type: "text", text: JSON.stringify(result) }], details: result }; } catch (e) { return { content: [{ type: "text", text: e instanceof Error ? e.message : String(e) }], isError: true, details: {} }; } } }); }
function fgCall(args: any, theme: any) { const fg = (name: string, value: string) => theme?.fg ? theme.fg(name, value) : value; return fg("accent", "+-- HISTORY SEARCH ----------------------------------------+") + "\\n" + fg("dim", `| op: ${(args?.operation ?? "search").padEnd(12)} | ${args?.query ? `query: ${String(args.query).slice(0, 35)}` : "read-only / local / bounded"}`) + "\\n" + fg("accent", "+----------------------------------------------------------+"); }
