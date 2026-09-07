/**
 * Swarm-compatible HistorySearch/HistoryGet over Pi session JSONL.
 *
 * Pi does not persist Swarm's conversation origin or separately generated
 * preview fields. Origin is therefore omitted; preview/title are derived from
 * the first substantive user message. Conversation IDs are Pi session IDs.
 */
import { readFile, readdir, stat } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, isAbsolute, parse, resolve } from "node:path";

export interface HistoryRuntime { root: string; cwd: string }
type AnyMap = Record<string, any>;
type Segment = { kind: "message" | "tool_call" | "tool_result"; text: string; messageId?: string; role?: string; toolName?: string; failed?: boolean; ordinal: number };
type Session = { id: string; cwd: string; title: string; titlePersisted: boolean; preview: string; updatedAt: string; entries: AnyMap[]; messages: AnyMap[]; body: string; segments: Segment[] };

const runtimeBlock = /<system-reminder[^>]*>[\s\S]*?<\/system-reminder>|<swarm_runtime_guidance[^>]*>[\s\S]*?<\/swarm_runtime_guidance>|<available_skills[^>]*>[\s\S]*?<\/available_skills>|<swarm_runtime_(?:skills|capabilities)[^>]*>[\s\S]*?<\/swarm_runtime_(?:skills|capabilities)>|<effective_capabilities[^>]*>[\s\S]*?<\/effective_capabilities>|<env[^>]*>[\s\S]*?<\/env>/gi;
const runtimePrefixes = ["[scheduled]", "## mcp context", "## current tasks", "**current mode**", "[task nudge]", "[skill reminder]", "this session is being continued from a previous conversation", "please continue the conversation from where we left off", "continue from where"];
const secretAssignment = /((?:api[_-]?key|access[_-]?token|password|secret)\s*[=:]\s*)[^\s"'&]+/gi;
const authorization = /(authorization\s*:\s*(?:bearer|basic)\s+)\S+/gi;

function textOf(value: any): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.map((x) => textOf(x)).filter(Boolean).join("\n");
  if (!value || typeof value !== "object") return "";
  return textOf(value.text ?? value.content ?? value.output ?? "");
}
export function cleanHistoryText(value: string): string {
  let out = value.replace(/\\u003c/gi, "<").replace(/\\u003e/gi, ">").replace(/\\"/g, "\"").replace(runtimeBlock, " ");
  const opening = /<(?:system-reminder|swarm_runtime_guidance|available_skills|swarm_runtime_(?:skills|capabilities)|effective_capabilities|env)\b/i.exec(out);
  if (opening) out = out.slice(0, opening.index);
  out = out.replace(/^\s*\[(?:SKILL REMINDER|Task Nudge)\].*$/gim, " ").replace(/\\n/g, " ").replace(/\s+/g, " ").trim();
  return out;
}
/** historytools.isSubstantiveUserText over CleanText'd input (cleaning is idempotent). */
export const isSubstantiveUserText = (cleaned: string): boolean => substantive(cleaned);
function substantive(value: string): boolean {
  const lower = cleanHistoryText(value).toLowerCase().trim();
  return lower !== "" && lower !== "continue" && !(lower.includes("continue") && lower.split(/\s+/).length <= 6) && !runtimePrefixes.some((p) => lower.startsWith(p));
}
function redact(value: string): string { return value.replace(authorization, "$1[REDACTED]").replace(secretAssignment, "$1[REDACTED]"); }
function deriveTitle(value: string): string {
  const clean = cleanHistoryText(value);
  if (!clean) return "(untitled conversation)";
  const stop = clean.search(/[.!?\n]/);
  const candidate = stop > 0 && stop < 80 ? clean.slice(0, stop) : clean;
  if ([...candidate].length <= 80) return candidate.trim();
  const first = [...candidate].slice(0, 80).join(""); const space = first.lastIndexOf(" ");
  return (space > 40 ? first.slice(0, space) : first).trim() + "…";
}
function canonicalWorkspace(value: unknown, label: string): string {
  if (typeof value !== "string" || value === "" || !isAbsolute(value) || parse(resolve(value)).root === resolve(value)) throw new Error(`${label} must be an absolute non-root path`);
  return resolve(value);
}
function integer(p: AnyMap, name: string, fallback: number, max: number, allowZero = false): number {
  const value = p[name]; if (value === undefined || value === null) return fallback;
  if (!Number.isInteger(value) || value < (allowZero ? 0 : 1) || value > max) {
    if (allowZero) throw new Error(`${name} must be a non-negative integer`);
    throw new Error(`${name} must be an integer between 1 and ${max}`);
  }
  return value;
}
function bool(p: AnyMap, name: string, fallback: boolean): boolean {
  if (p[name] === undefined || p[name] === null) return fallback;
  if (typeof p[name] !== "boolean") throw new Error(`${name} must be a boolean`);
  return p[name];
}
function iso(value: string): string { const d = new Date(value); return Number.isNaN(d.valueOf()) ? "" : d.toISOString().replace(".000Z", "Z"); }
function compactTitle(value: string): string { return deriveTitle(value); }

/**
 * Providers sometimes materialize optional JSON-Schema defaults and empty
 * strings. Remove only values that are semantically identical to omission so
 * they cannot accidentally select segment/stats mode or invalidate a normal
 * workspace search.
 */
export function normalizeHistorySearchParams(input: AnyMap): AnyMap {
  const params = { ...(input ?? {}) };
  for (const key of ["workspace_path", "regex", "tool_name", "origin"] as const) {
    if (typeof params[key] === "string" && params[key].trim() === "") delete params[key];
  }
  if (Array.isArray(params.fields) && params.fields.length === 0) delete params.fields;
  if (params.tool_outcome === "any" || params.tool_outcome === "") delete params.tool_outcome;
  if (params.stats !== true) {
    delete params.ngram;
    delete params.top_terms;
    if (params.stats === false) delete params.stats;
  }
  return params;
}

/**
 * Preserve explicit pagination while tolerating neutral values materialized by
 * schema adapters. offset:0 is only redundant when tail already selects the
 * window; on its own it still means "start at the first stored message".
 */
export function normalizeHistoryGetParams(input: AnyMap): AnyMap {
  const params = { ...(input ?? {}) };
  if (typeof params.workspace_path === "string" && params.workspace_path.trim() === "")
    delete params.workspace_path;
  if (params.tail !== undefined && params.offset === 0) delete params.offset;
  return params;
}

function convertMessage(entry: AnyMap): AnyMap | undefined {
  if (entry?.type !== "message" || !entry.message) return;
  const role = entry.message.role ?? "custom";
  const blocks = Array.isArray(entry.message.content) ? entry.message.content : [{ type: "text", text: textOf(entry.message.content) }];
  const content = blocks.filter((b: any) => b?.type === "text").map((b: any) => b.text ?? "").join("\n");
  const row: AnyMap = { id: entry.id ?? "", timestamp: entry.timestamp ?? "", role, content };
  const calls = blocks.filter((b: any) => b?.type === "toolCall" || b?.type === "tool_call").map((b: any) => ({ id: b.id ?? b.toolCallId ?? "", name: b.name ?? b.toolName ?? "", parameters: b.arguments ?? b.input ?? {} }));
  if (calls.length) row.tool_calls = calls;
  if (role === "toolResult" || role === "tool") row.tool_results = [{ call_id: entry.message.toolCallId ?? "", name: entry.message.toolName ?? "", output: textOf(entry.message.content), ...(entry.message.isError ? { error: { type: "tool_error", message: textOf(entry.message.content) } } : {}) }];
  return row;
}

async function loadSessions(runtime: HistoryRuntime): Promise<Session[]> {
  // Reuse Pi's existing engine when its UI peer dependency is available. The
  // small fallback keeps pure-logic/unit-test consumers independent of pi-tui.
  let listed: any[];
  // Swarm's session store simply has nothing to list when the directory does
  // not exist yet; never surface ENOENT from the Pi engine or the fallback.
  if (!existsSync(runtime.root)) return [];
  try {
    const { HistorySearchEngine } = await import("../../extensions/30-tools/history-search.ts");
    const engine = new HistorySearchEngine({ root: runtime.root, includeCurrent: true });
    listed = await engine.list({ root: runtime.root, includeCurrent: true });
  } catch {
    const found: string[] = [];
    const walk = async (dir: string): Promise<void> => {
      for (const item of await readdir(dir, { withFileTypes: true })) {
        const path = resolve(dir, item.name);
        if (item.isDirectory()) await walk(path);
        else if (item.isFile() && path.endsWith(".jsonl")) found.push(path);
      }
    };
    await walk(runtime.root);
    listed = await Promise.all(found.map(async (session) => ({ session, updatedAt: (await stat(session)).mtime.toISOString() })));
  }
  const sessions: Session[] = [];
  for (const item of listed) {
    let raw = ""; try { raw = await readFile(item.session, "utf8"); } catch { continue; }
    const entries = raw.split(/\r?\n/).filter(Boolean).flatMap((line) => { try { return [JSON.parse(line)]; } catch { return []; } });
    const header = entries.find((e) => e.type === "session") ?? {};
    const messages = entries.map(convertMessage).filter(Boolean) as AnyMap[];
    const firstUser = messages.find((m) => m.role === "user" && substantive(m.content))?.content ?? "";
    const preview = cleanHistoryText(firstUser);
    const persistedTitle = cleanHistoryText(header.name ?? "");
    const title = persistedTitle || deriveTitle(preview);
    const segments: Segment[] = []; let ordinal = 0;
    for (const m of messages) {
      if (m.content) segments.push({ kind: "message", text: m.content, messageId: m.id, role: m.role, ordinal: ordinal++ });
      for (const c of m.tool_calls ?? []) segments.push({ kind: "tool_call", text: JSON.stringify(c.parameters), messageId: m.id, role: m.role, toolName: c.name, ordinal: ordinal++ });
      for (const r of m.tool_results ?? []) segments.push({ kind: "tool_result", text: r.output, messageId: m.id, role: m.role, toolName: r.name, failed: Boolean(r.error), ordinal: ordinal++ });
    }
    const fs = await stat(item.session);
    const updated = [...entries].reverse().find((e) => e.timestamp)?.timestamp ?? item.updatedAt ?? fs.mtime.toISOString();
    sessions.push({ id: header.id ?? item.id ?? item.session, cwd: header.cwd ?? item.cwd ?? "", title, titlePersisted: Boolean(persistedTitle), preview, updatedAt: iso(updated), entries, messages, body: messages.map((m) => m.content).join("\n"), segments });
  }
  return sessions;
}

function regexFor(params: AnyMap): RegExp | undefined {
  if (params.regex === undefined || params.regex === null || params.regex === "") return;
  if (typeof params.regex !== "string") throw new Error("regex must be a string");
  if (/(\(\?[=!<]|\\[1-9])/.test(params.regex)) throw new Error(`invalid regex ${JSON.stringify(params.regex)}: invalid or unsupported Perl syntax (Go RE2 syntax; lookahead/backreferences are unsupported — use a plain query instead)`);
  try { return new RegExp(params.regex, params.case_sensitive ? "u" : "iu"); }
  catch (e) { throw new Error(`invalid regex ${JSON.stringify(params.regex)}: ${e instanceof Error ? e.message : String(e)} (Go RE2 syntax; lookahead/backreferences are unsupported — use a plain query instead)`); }
}
function snippet(text: string, matcher: RegExp | undefined, terms: string[], context: number): string {
  let at = -1, length = 0;
  if (matcher) { const m = matcher.exec(text); if (m) { at = m.index; length = m[0].length; } matcher.lastIndex = 0; }
  if (at < 0) for (const term of terms) { at = text.toLowerCase().indexOf(term.toLowerCase()); if (at >= 0) { length = term.length; break; } }
  if (at < 0) return [...text].slice(0, Math.min(320, context * 2)).join("");
  const chars = [...text], before = [...text.slice(0, at)].length, matchLen = [...text.slice(at, at + length)].length;
  const start = Math.max(0, before - context), end = Math.min(chars.length, before + matchLen + context);
  return (start ? "…" : "") + chars.slice(start, end).join("") + (end < chars.length ? "…" : "");
}

export async function historySearch(params: AnyMap, runtime: HistoryRuntime): Promise<AnyMap> {
  params = normalizeHistorySearchParams(params);
  const cwd = canonicalWorkspace(runtime.cwd, "workspace path");
  const scope = params.scope ?? "current";
  if (scope !== "current" && scope !== "all") throw new Error("HistorySearch: scope must be one of current or all");
  if (params.workspace_path != null && scope === "all") throw new Error("HistorySearch: workspace_path and scope=all are mutually exclusive");
  const workspace = scope === "all" ? "" : params.workspace_path != null ? canonicalWorkspace(params.workspace_path, "workspace_path") : cwd;
  const limit = integer(params, "limit", 10, 50);
  const caseSensitive = bool(params, "case_sensitive", false);
  const searchBody = bool(params, "search_body", String(params.query ?? "").trim() !== "" || Boolean(params.regex));
  const query = typeof params.query === "string" ? params.query.trim() : "";
  const terms = query.split(/\s+/).filter(Boolean);
  const rx = regexFor({ ...params, case_sensitive: caseSensitive });
  const fields: string[] = params.fields ?? ["title", "preview", "body"];
  if (!Array.isArray(fields)) throw new Error("HistorySearch: fields must be an array of title, preview or body");
  for (const f of fields) if (!["title", "preview", "body"].includes(String(f).toLowerCase().trim())) throw new Error(`HistorySearch: fields must contain only title, preview or body (got ${JSON.stringify(f)})`);
  const sortBy = params.sort ?? ((query || rx) ? "relevance" : "recency");
  if (!["relevance", "recency", "message_count"].includes(sortBy)) throw new Error("HistorySearch: sort must be one of relevance, recency, message_count");
  const order = params.order ?? "desc"; if (!["asc", "desc"].includes(order)) throw new Error("HistorySearch: order must be one of asc, desc");
  const minMessages = integer(params, "min_messages", 0, Number.MAX_SAFE_INTEGER, true);
  if (params.origin != null && !["interactive", "subagent", "headless"].includes(params.origin)) throw new Error("HistorySearch: origin must be one of interactive, subagent, headless");
  const sessions = (await loadSessions(runtime)).filter((s) => !workspace || resolve(s.cwd) === workspace).filter((s) => s.messages.length >= minMessages);
  const segmentRequested = ["tool_name", "tool_outcome", "segment_kind", "stats", "ngram", "top_terms"].some((k) => params[k] !== undefined);
  if (params.ngram !== undefined && !params.stats || params.top_terms !== undefined && !params.stats) throw new Error("HistorySearch: ngram and top_terms are only meaningful with stats=true");
  if (segmentRequested) return segmentSearch(params, sessions, scope, workspace);
  const ranked: { session: Session; score: number; snippets: AnyMap[] }[] = [];
  for (const s of sessions) {
    const values: AnyMap = { title: s.title, preview: s.preview, body: searchBody ? s.body : "" };
    if (params.exclude_runtime) values.body = cleanHistoryText(values.body);
    let score = 0, ok = true;
    for (const term of terms) {
      const needle = caseSensitive ? term : term.toLowerCase();
      const title = caseSensitive ? values.title : values.title.toLowerCase();
      const hay = fields.map((f) => caseSensitive ? values[f] : values[f].toLowerCase());
      if (!hay.some((v) => v.includes(needle))) { ok = false; break; }
      score += s.titlePersisted && title.includes(needle) ? 3 : 1;
    }
    if (ok && rx && !fields.some((f) => { rx.lastIndex = 0; return rx.test(values[f]); })) ok = false;
    if (!ok) continue;
    const snippets: AnyMap[] = [];
    if (params.snippet) for (const f of fields) {
      const v = values[f]; if (!v) continue;
      const matches = rx ? (rx.lastIndex = 0, rx.test(v)) : terms.some((t) => (caseSensitive ? v : v.toLowerCase()).includes(caseSensitive ? t : t.toLowerCase()));
      if (matches) snippets.push({ field: f, text: snippet(v, rx, terms, integer(params, "snippet_context", 60, 200)) });
      if (snippets.length >= integer(params, "max_snippets", 3, 10)) break;
    }
    ranked.push({ session: s, score, snippets });
  }
  ranked.sort((a, b) => {
    const n = sortBy === "message_count" ? a.session.messages.length - b.session.messages.length : sortBy === "recency" ? a.session.updatedAt.localeCompare(b.session.updatedAt) : a.score - b.score || a.session.updatedAt.localeCompare(b.session.updatedAt);
    return order === "desc" ? -n : n;
  });
  const truncated = ranked.length > limit;
  const results = ranked.slice(0, limit).map(({ session: s, snippets }) => ({
    id: s.id, title: s.title, ...(s.preview && s.preview !== s.title ? { preview: s.preview } : {}), ...(s.messages.length ? { message_count: s.messages.length } : {}),
    ...(s.updatedAt ? { updated_at: s.updatedAt } : {}), ...(scope === "all" ? { workspace_path: s.cwd } : {}), ...(snippets.length ? { snippets } : {}),
  }));
  return { scope, query: caseSensitive ? query : query.toLowerCase(), sort: sortBy, order, results, ...(workspace ? { workspace_path: workspace } : {}), ...(truncated ? { truncated: true } : {}), ...((rx || caseSensitive || params.fields || params.exclude_runtime) ? { post_filtered: true, candidates_examined: sessions.length } : {}) };
}

function segmentSearch(params: AnyMap, sessions: Session[], scope: string, workspace: string): AnyMap {
  const ngram = integer(params, "ngram", 1, 5), requestedTop = integer(params, "top_terms", 50, Number.MAX_SAFE_INTEGER), top = Math.min(500, requestedTop);
  let segments = sessions.flatMap((s) => s.segments.map((x) => ({ ...x, session: s })));
  if (params.tool_name) segments = segments.filter((s) => s.toolName === String(params.tool_name).trim());
  if (params.segment_kind) {
    if (!["message", "tool_call", "tool_result"].includes(params.segment_kind)) throw new Error(`HistorySearch: segment_kind must be one of message, tool_call, tool_result (got ${JSON.stringify(params.segment_kind)})`);
    segments = segments.filter((s) => s.kind === params.segment_kind);
  }
  if (params.tool_outcome && params.tool_outcome !== "any") {
    if (!["failed", "succeeded"].includes(params.tool_outcome)) throw new Error(`HistorySearch: tool_outcome must be one of any, failed, succeeded (got ${JSON.stringify(params.tool_outcome)})`);
    segments = segments.filter((s) => s.kind === "tool_result" && (params.tool_outcome === "failed") === Boolean(s.failed));
  }
  const filter: AnyMap = {}; if (workspace) filter.workspace_path = workspace; if (params.tool_name) filter.tool_name = String(params.tool_name).trim(); if (params.segment_kind) filter.kind = params.segment_kind; if (params.tool_outcome && params.tool_outcome !== "any") filter.outcome = params.tool_outcome; if (params.exclude_runtime) filter.exclude_runtime = true;
  if (params.stats) {
    const map = new Map<string, { occurrences: number; segments: Set<string>; conversations: Set<string> }>();
    segments.forEach((s, si) => { const words = cleanHistoryText(s.text).toLowerCase().match(/[\p{L}\p{N}_-]+/gu) ?? []; for (let i = 0; i + ngram <= words.length; i++) { const term = words.slice(i, i + ngram).join(" "); const v = map.get(term) ?? { occurrences: 0, segments: new Set(), conversations: new Set() }; v.occurrences++; v.segments.add(`${s.session.id}:${si}`); v.conversations.add(s.session.id); map.set(term, v); } });
    const terms = [...map].map(([term, v]) => ({ term, occurrences: v.occurrences, segments: v.segments.size, conversations: v.conversations.size })).sort((a, b) => b.occurrences - a.occurrences || a.term.localeCompare(b.term)).slice(0, top);
    return { stats: true, scope, filter, ngram, top_terms: top, segments_scanned: segments.length, terms, ...(requestedTop > 500 ? { top_terms_capped: true, requested_top_terms: requestedTop } : {}) };
  }
  const query = String(params.query ?? "").trim(), terms = query.toLowerCase().split(/\s+/).filter(Boolean), rx = regexFor(params);
  segments = segments.filter((s) => terms.every((t) => s.text.toLowerCase().includes(t)) && (!rx || (rx.lastIndex = 0, rx.test(s.text))));
  const seen = new Set<string>(), limit = integer(params, "limit", 10, 50), results: AnyMap[] = [];
  for (const hit of segments) { if (seen.has(hit.session.id)) continue; seen.add(hit.session.id); const s = hit.session; results.push({ id: s.id, title: s.title, ...(s.preview !== s.title ? { preview: s.preview } : {}), message_count: s.messages.length, updated_at: s.updatedAt, ...(scope === "all" ? { workspace_path: s.cwd } : {}), matched_segment: { kind: hit.kind, ordinal: hit.ordinal, text: compactTitle(hit.text).slice(0, 320), ...(hit.messageId ? { message_id: hit.messageId } : {}), ...(hit.role ? { role: hit.role } : {}), ...(hit.toolName ? { tool_name: hit.toolName } : {}), ...(hit.kind === "tool_result" ? { outcome: hit.failed ? "failed" : "succeeded" } : {}) } }); if (results.length === limit) break; }
  return { scope, query: params.case_sensitive ? query : query.toLowerCase(), sort: params.sort ?? (query || rx ? "relevance" : "recency"), order: params.order ?? "desc", segment_filter: filter, results, ...(workspace ? { workspace_path: workspace } : {}) };
}

function sanitize(value: any, key = ""): any {
  if (/(api.?key|authorization|token|password|secret|credential|private.?key|cookie|session.?id|client.?secret)/i.test(key)) return "[REDACTED]";
  if (typeof value === "string") return redact(value);
  if (Array.isArray(value)) return value.map((x) => sanitize(x, key));
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, sanitize(v, k)]));
  return value;
}
function fitRow(message: AnyMap, budget: number, humanOnly: boolean): { row?: AnyMap; size: number; truncated: boolean } {
  const base: AnyMap = { id: message.id, timestamp: message.timestamp, role: message.role, content: "" };
  if (Buffer.byteLength(JSON.stringify(base)) > budget) return { size: 0, truncated: true };
  const source = redact(message.content); const chars = [...source]; let lo = 0, hi = chars.length;
  while (lo < hi) { const mid = Math.ceil((lo + hi) / 2); base.content = chars.slice(0, mid).join(""); if (Buffer.byteLength(JSON.stringify(base)) <= budget) lo = mid; else hi = mid - 1; }
  base.content = chars.slice(0, lo).join(""); let truncated = lo < chars.length;
  if (!humanOnly) for (const key of ["tool_calls", "tool_results"]) for (const item of message[key] ?? []) { const candidate = [...(base[key] ?? []), sanitize(item)]; base[key] = candidate; if (Buffer.byteLength(JSON.stringify(base)) > budget) { base[key].pop(); if (!base[key].length) delete base[key]; truncated = true; break; } }
  return { row: base, size: Buffer.byteLength(JSON.stringify(base)), truncated };
}

export async function historyGet(params: AnyMap, runtime: HistoryRuntime): Promise<AnyMap> {
  params = normalizeHistoryGetParams(params);
  const cwd = canonicalWorkspace(runtime.cwd, "workspace path");
  if (typeof params.all_workspaces !== "undefined" && typeof params.all_workspaces !== "boolean") throw new Error("HistoryGet: all_workspaces must be a boolean");
  const all = Boolean(params.all_workspaces), selected = all ? "" : params.workspace_path != null ? canonicalWorkspace(params.workspace_path, "workspace_path") : cwd;
  const id = typeof params.conversation_id === "string" ? params.conversation_id.trim() : ""; if (!id) throw new Error("HistoryGet: conversation_id is required");
  const maxMessages = integer(params, "max_messages", 20, 100), maxChars = integer(params, "max_chars", 12000, 50000), humanOnly = bool(params, "human_only", false);
  if (params.tail !== undefined && params.offset !== undefined) throw new Error("HistoryGet: tail and offset are mutually exclusive");
  const sessions = await loadSessions(runtime);
  const matchingId = sessions.filter((session) => session.id === id);
  const matches = all
    ? matchingId
    : matchingId.filter((session) => resolve(session.cwd) === selected);
  if (!matches.length) {
    if (matchingId.length && !all)
      throw new Error("HistoryGet: conversation does not match the requested workspace");
    throw new Error("HistoryGet: conversation not found");
  }
  if (matches.length > 1)
    throw new Error("HistoryGet: conversation_id is ambiguous across stored sessions");
  const conv = matches[0];
  const total = conv.messages.length; let start = 0, end = total;
  if (params.offset !== undefined) { start = Math.min(total, integer(params, "offset", 0, Number.MAX_SAFE_INTEGER, true)); end = Math.min(total, start + maxMessages); }
  else if (params.tail !== undefined) { const tail = Math.min(integer(params, "tail", 20, 100), maxMessages); start = Math.max(0, total - tail); }
  else start = Math.max(0, total - maxMessages);
  let remaining = maxChars, omitted = start + total - end, filtered = 0, contentTruncated = false; const messages: AnyMap[] = [];
  for (let i = end - 1; i >= start; i--) {
    const original = conv.messages[i]; if (humanOnly && !["user", "assistant"].includes(original.role)) { filtered++; continue; }
    const content = humanOnly ? cleanHistoryText(original.content) : original.content;
    if (humanOnly && (!content || original.role === "user" && !substantive(content))) { filtered++; continue; }
    const fitted = fitRow({ ...original, content }, remaining, humanOnly);
    if (!fitted.row) { omitted += i - start + 1; contentTruncated = true; break; }
    remaining -= fitted.size; contentTruncated ||= fitted.truncated; messages.unshift(fitted.row);
  }
  return { conversation_id: conv.id, workspace_path: conv.cwd, messages, total_message_count: total, window_start: start, window_end: end, rendered_message_count: messages.length, ...(conv.title ? { title: conv.title } : {}), ...(omitted || contentTruncated ? { truncated: true } : {}), ...(contentTruncated ? { content_truncated: true } : {}), ...(omitted ? { omitted_message_count: omitted } : {}), ...(filtered ? { filtered_message_count: filtered } : {}), ...(!total || !messages.length ? { empty: true } : {}), metadata_truncated: false };
}

/** Apply HistoryGet's max_chars cap to the complete serialized JSON envelope. */
export function boundedHistoryJSON(input: AnyMap, maxChars: number): { value: AnyMap; text: string } {
  const value = structuredClone(input);
  let text = JSON.stringify(value);
  if (Buffer.byteLength(text) <= maxChars) return { value, text };
  while (value.messages?.length && Buffer.byteLength(text) > maxChars) {
    value.messages.shift(); value.rendered_message_count = value.messages.length; value.truncated = true; value.content_truncated = true; value.omitted_message_count = (value.omitted_message_count ?? 0) + 1; text = JSON.stringify(value);
  }
  if (Buffer.byteLength(text) <= maxChars) return { value, text };
  if (typeof value.title === "string" && value.title !== "") {
    const original = [...value.title]; let lo = 0, hi = original.length;
    while (lo < hi) {
      const mid = Math.ceil((lo + hi) / 2); value.title = original.slice(0, mid).join(""); value.metadata_truncated = true;
      if (Buffer.byteLength(JSON.stringify(value)) <= maxChars) lo = mid; else hi = mid - 1;
    }
    value.title = original.slice(0, lo).join(""); value.metadata_truncated = true; text = JSON.stringify(value);
  }
  if (Buffer.byteLength(text) <= maxChars) return { value, text };
  text = maxChars >= 18 ? `{"truncated":true}` : maxChars >= 2 ? "{}" : "0";
  return { value: JSON.parse(text), text };
}

export function historyRootFromContext(ctx: any): string {
  const dir = ctx?.sessionManager?.getSessionDir?.();
  if (dir) {
    const parent = dirname(dir);
    return dirname(parent).endsWith(".pi/agent") ? parent : dir;
  }
  return resolve(process.env.PI_CODING_AGENT_DIR ?? `${process.env.HOME ?? process.cwd()}/.pi/agent`, "sessions");
}
