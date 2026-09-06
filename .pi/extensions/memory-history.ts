import { createHash } from "node:crypto";
import { withDefaultToolRenderer } from "../lib/swarm-tool-renderer.ts";

export const MEMORY_ENTRY_TYPE = "pi-swarm-memory";
export const MEMORY_VERSION = 1;

export interface MemoryScope {
  namespace: string;
  workspace: string;
  session: string;
}

export interface MemoryRecord {
  id: string;
  version: number;
  namespace: string;
  workspace: string;
  session: string;
  text: string;
  tags: string[];
  createdAt: string;
  source?: string;
}

export interface MemoryEntry {
  type: typeof MEMORY_ENTRY_TYPE;
  data: MemoryRecord;
}

const secretPatterns = [
  /(sk-[A-Za-z0-9_-]{12,})/g,
  /(gh[pousr]_[A-Za-z0-9_]{20,})/g,
  /(xai-[A-Za-z0-9_-]{12,})/g,
  /((?:api[_-]?key|token|secret|password)\s*[:=]\s*)([^\s,;]+)/gi,
  /(Bearer\s+)[A-Za-z0-9._~+\/-]{12,}/gi,
];

/** Redacts common credentials before memory is persisted or returned. */
export function redact(text: string): string {
  let result = String(text);
  result = result.replace(secretPatterns[0], "[REDACTED]");
  result = result.replace(secretPatterns[1], "[REDACTED]");
  result = result.replace(secretPatterns[2], "[REDACTED]");
  result = result.replace(secretPatterns[3], "$1[REDACTED]");
  return result.replace(secretPatterns[4], "$1[REDACTED]");
}

function normalize(value: string, fallback: string): string {
  const result = value.trim().replace(/[\\/]+/g, "/");
  return result || fallback;
}

export function scopeOf(input: Partial<MemoryScope> = {}, cwd = process.cwd()): MemoryScope {
  return {
    namespace: normalize(input.namespace ?? "default", "default"),
    workspace: normalize(input.workspace ?? cwd, cwd),
    session: normalize(input.session ?? "current", "current"),
  };
}

function sameScope(a: MemoryScope, b: MemoryScope): boolean {
  return a.namespace === b.namespace && a.workspace === b.workspace && a.session === b.session;
}

function stableId(scope: MemoryScope, text: string, createdAt: string): string {
  return createHash("sha256").update(JSON.stringify([scope, text, createdAt])).digest("hex").slice(0, 24);
}

export class MemoryHistory {
  private records: MemoryRecord[] = [];
  constructor(private readonly now: () => Date = () => new Date()) {}

  /** Imports durable Pi entries. Unknown versions are ignored rather than guessed. */
  load(entries: unknown[]): void {
    this.records = entries.flatMap((entry: any) => {
      const data = entry?.type === MEMORY_ENTRY_TYPE ? entry.data : undefined;
      if (!data || data.version !== MEMORY_VERSION || typeof data.text !== "string") return [];
      return [{ ...data, text: redact(data.text), tags: Array.isArray(data.tags) ? data.tags.map(String) : [] }];
    });
  }

  remember(text: string, scopeInput: Partial<MemoryScope>, tags: string[] = [], source?: string): MemoryEntry {
    const scope = scopeOf(scopeInput);
    const clean = redact(text).trim();
    if (!clean) throw new Error("Memory text must not be empty");
    const createdAt = this.now().toISOString();
    const data: MemoryRecord = { id: stableId(scope, clean, createdAt), version: MEMORY_VERSION, ...scope, text: clean, tags: [...new Set(tags.map(String).filter(Boolean))], createdAt, ...(source ? { source: redact(source) } : {}) };
    this.records.push(data);
    return { type: MEMORY_ENTRY_TYPE, data };
  }

  search(query: string, scopeInput: Partial<MemoryScope>, limit = 20): MemoryRecord[] {
    const scope = scopeOf(scopeInput);
    const q = query.trim().toLocaleLowerCase();
    return this.records.filter(record => sameScope(record, scope) && (!q || `${record.text} ${record.tags.join(" ")}`.toLocaleLowerCase().includes(q))).slice(-Math.max(0, limit)).reverse();
  }

  replay(scopeInput: Partial<MemoryScope>): MemoryRecord[] { return this.search("", scopeInput, Number.MAX_SAFE_INTEGER).reverse(); }
  all(): MemoryRecord[] { return this.records.map(record => ({ ...record, tags: [...record.tags] })); }

  /** Converts older unversioned memory records into the current schema. */
  migrate(entries: unknown[], scopeInput: Partial<MemoryScope>): MemoryEntry[] {
    const scope = scopeOf(scopeInput);
    return entries.flatMap((entry: any) => {
      const old = entry?.type === "memory" ? entry.data ?? entry : entry?.memory;
      if (!old || typeof old.text !== "string") return [];
      const clean = redact(old.text).trim();
      if (!clean) return [];
      const createdAt = typeof old.createdAt === "string" ? old.createdAt : this.now().toISOString();
      const data: MemoryRecord = { id: stableId(scope, clean, createdAt), version: MEMORY_VERSION, ...scope, text: clean, tags: Array.isArray(old.tags) ? old.tags.map(String) : [], createdAt, source: "migration" };
      return [{ type: MEMORY_ENTRY_TYPE as typeof MEMORY_ENTRY_TYPE, data }];
    });
  }
}

const schema = { type: "object", required: ["operation"], additionalProperties: false, properties: { operation: { type: "string", enum: ["remember", "search", "replay", "migrate"] }, text: { type: "string" }, query: { type: "string" }, tags: { type: "array", items: { type: "string" } }, namespace: { type: "string" }, limit: { type: "number" } } } as const;

export default function memoryHistoryExtension(pi: any): void {
  const history = new MemoryHistory();
  let scope = scopeOf();
  let sessionEntries: unknown[] = [];
  pi.on?.("session_start", (_event: any, ctx: any) => {
    const cwd = ctx?.cwd ?? process.cwd();
    const session = ctx?.sessionManager?.getSessionFile?.() ?? ctx?.sessionManager?.sessionFile ?? "current";
    scope = scopeOf({ workspace: cwd, session: String(session) }, cwd);
    sessionEntries = ctx?.sessionManager?.getEntries?.() ?? [];
    history.load(sessionEntries);
  });
  pi.registerTool?.(withDefaultToolRenderer({ name: "memory_history", label: "Memory History", description: "Durable, redacted, workspace/session-scoped memory and history retrieval.", parameters: schema, async execute(_id: string, params: any) {
    try {
      if (params.operation === "remember") {
        const entry = history.remember(params.text ?? "", { ...scope, namespace: params.namespace ?? scope.namespace }, params.tags ?? [], "memory_history");
        pi.appendEntry?.(MEMORY_ENTRY_TYPE, entry.data);
        return { content: [{ type: "text", text: JSON.stringify(entry.data) }], details: {} };
      }
      const requested = { ...scope, namespace: params.namespace ?? scope.namespace };
      if (params.operation === "migrate") {
        const migrated = history.migrate(sessionEntries, requested);
        for (const entry of migrated) pi.appendEntry?.(MEMORY_ENTRY_TYPE, entry.data);
        return { content: [{ type: "text", text: JSON.stringify(migrated) }], details: {} };
      }
      const result = params.operation === "replay" ? history.replay(requested) : history.search(params.query ?? "", requested, params.limit ?? 20);
      return { content: [{ type: "text", text: JSON.stringify(result) }], details: {} };
    } catch (error) { return { content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }], isError: true, details: {} }; }
  } }));
}
