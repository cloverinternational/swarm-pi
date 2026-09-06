import { mkdir, chmod, rename, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { homedir } from "node:os";

export type AnnoyedStatus = "backlog" | "triage" | "accepted" | "in_progress" | "verified" | "wont_fix" | "duplicate";
export type AnnoyedSeverity = "low" | "medium" | "high" | "critical";

export interface AnnoyedIssue {
  id: string;
  fingerprint: string;
  title: string;
  issue: string;
  category: string;
  severity: AnnoyedSeverity;
  status: AnnoyedStatus;
  observed: string;
  expected: string;
  evidence: string[];
  acceptanceTests: string[];
  toolName?: string;
  conversationId?: string;
  projectCwd?: string;
  source: string;
  occurrences: number;
  firstSeenAt: string;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
  resolution?: string;
  tags: string[];
  transcript?: unknown;
  metadata: Record<string, unknown>;
}

export interface AnnoyedStoreOptions { home?: string; now?: () => Date; }

// node:sqlite is available in the Node runtime used by Pi. Keeping the import
// dynamic lets the extension load far enough to give a useful error on older
// Node versions, instead of failing discovery.
type SqliteDatabase = { exec(sql: string): void; prepare(sql: string): any; close(): void };

const VALID_STATUSES = new Set<AnnoyedStatus>(["backlog", "triage", "accepted", "in_progress", "verified", "wont_fix", "duplicate"]);
const VALID_SEVERITIES = new Set<AnnoyedSeverity>(["low", "medium", "high", "critical"]);
const json = (value: unknown) => JSON.stringify(value ?? null);
const parse = <T>(value: unknown, fallback: T): T => { try { return value == null ? fallback : JSON.parse(String(value)); } catch { return fallback; } };
const id = () => `ann-${new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14)}-${crypto.randomUUID().slice(0, 8)}`;
const clean = (value: unknown, max = 12000) => String(value ?? "").trim().slice(0, max);
const list = (value: unknown, maxItems = 32) => (Array.isArray(value) ? value : []).map(x => clean(x, 4000)).filter(Boolean).slice(0, maxItems);

export class AnnoyedStore {
  readonly directory: string;
  readonly databasePath: string;
  readonly exportPath: string;
  private db?: SqliteDatabase;
  private now: () => Date;

  constructor(options: AnnoyedStoreOptions = {}) {
    const home = options.home ?? homedir();
    this.directory = join(home, ".pi", "annoyed");
    this.databasePath = join(this.directory, "annoyed.sqlite");
    this.exportPath = join(this.directory, "issues.json");
    this.now = options.now ?? (() => new Date());
  }

  async open() {
    if (this.db) return this;
    await mkdir(this.directory, { recursive: true, mode: 0o700 });
    await chmod(this.directory, 0o700).catch(() => undefined);
    const sqlite = await import("node:sqlite") as any;
    this.db = new sqlite.DatabaseSync(this.databasePath) as SqliteDatabase;
    this.db!.exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;");
    this.db!.exec(`
      CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS issues (
        id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, title TEXT NOT NULL, issue TEXT NOT NULL,
        category TEXT NOT NULL, severity TEXT NOT NULL, status TEXT NOT NULL,
        observed TEXT NOT NULL, expected TEXT NOT NULL, evidence_json TEXT NOT NULL,
        acceptance_tests_json TEXT NOT NULL, tool_name TEXT, conversation_id TEXT,
        project_cwd TEXT, source TEXT NOT NULL, occurrences INTEGER NOT NULL DEFAULT 1,
        first_seen_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL, resolution TEXT, tags_json TEXT NOT NULL,
        transcript_json TEXT, metadata_json TEXT NOT NULL
      );
      CREATE UNIQUE INDEX IF NOT EXISTS issues_fingerprint_idx ON issues(fingerprint);
      CREATE INDEX IF NOT EXISTS issues_board_idx ON issues(status, severity, updated_at DESC);
      CREATE TABLE IF NOT EXISTS issue_events (
        id INTEGER PRIMARY KEY AUTOINCREMENT, issue_id TEXT NOT NULL, event_type TEXT NOT NULL,
        at TEXT NOT NULL, payload_json TEXT NOT NULL,
        FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
      );
    `);
    await chmod(this.databasePath, 0o600).catch(() => undefined);
    return this;
  }

  private requireDb() { if (!this.db) throw new Error("AnnoyedStore is not open"); return this.db; }
  private rowToIssue(row: any): AnnoyedIssue { return {
    id: row.id, fingerprint: row.fingerprint, title: row.title, issue: row.issue,
    category: row.category, severity: row.severity, status: row.status,
    observed: row.observed, expected: row.expected, evidence: parse(row.evidence_json, []),
    acceptanceTests: parse(row.acceptance_tests_json, []), toolName: row.tool_name ?? undefined,
    conversationId: row.conversation_id ?? undefined, projectCwd: row.project_cwd ?? undefined,
    source: row.source, occurrences: row.occurrences, firstSeenAt: row.first_seen_at,
    lastSeenAt: row.last_seen_at, createdAt: row.created_at, updatedAt: row.updated_at,
    resolution: row.resolution ?? undefined, tags: parse(row.tags_json, []),
    transcript: parse(row.transcript_json, undefined), metadata: parse(row.metadata_json, {}),
  }; }

  async upsert(input: Partial<AnnoyedIssue> & { issue: string; fingerprint?: string }): Promise<{ issue: AnnoyedIssue; duplicate: boolean }> {
    await this.open(); const db = this.requireDb(); const now = this.now().toISOString();
    const text = clean(input.issue); if (!text) throw new Error("issue is required");
    const fingerprint = clean(input.fingerprint, 200) || await fingerprintFor(input);
    const existing = db.prepare("SELECT * FROM issues WHERE fingerprint = ?").get(fingerprint);
    if (existing) {
      const updated = db.prepare("UPDATE issues SET occurrences=occurrences+1,last_seen_at=?,updated_at=?,metadata_json=? WHERE id=?").run(now, now, json(input.metadata ?? parse(existing.metadata_json, {})), existing.id);
      db.prepare("INSERT INTO issue_events(issue_id,event_type,at,payload_json) VALUES(?,?,?,?)").run(existing.id, "observed_again", now, json(input));
      await this.exportJson();
      return { issue: this.rowToIssue(db.prepare("SELECT * FROM issues WHERE id=?").get(existing.id)), duplicate: true };
    }
    const issue: AnnoyedIssue = {
      id: input.id ?? id(), fingerprint, title: clean(input.title, 240) || text.split(/\s+/).slice(0, 12).join(" "), issue: text,
      category: clean(input.category, 80) || "other", severity: VALID_SEVERITIES.has(input.severity as AnnoyedSeverity) ? input.severity as AnnoyedSeverity : "medium",
      status: VALID_STATUSES.has(input.status as AnnoyedStatus) ? input.status as AnnoyedStatus : "backlog",
      observed: clean(input.observed), expected: clean(input.expected), evidence: list(input.evidence), acceptanceTests: list(input.acceptanceTests),
      toolName: clean(input.toolName, 120) || undefined, conversationId: clean(input.conversationId, 200) || undefined,
      projectCwd: clean(input.projectCwd, 1000) || undefined, source: clean(input.source, 80) || "annoyed-tool", occurrences: 1,
      firstSeenAt: now, lastSeenAt: now, createdAt: now, updatedAt: now, resolution: clean(input.resolution) || undefined,
      tags: list(input.tags, 24), transcript: input.transcript, metadata: input.metadata ?? {},
    };
    db.prepare(`INSERT INTO issues VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`).run(
      issue.id, issue.fingerprint, issue.title, issue.issue, issue.category, issue.severity, issue.status,
      issue.observed, issue.expected, json(issue.evidence), json(issue.acceptanceTests), issue.toolName ?? null,
      issue.conversationId ?? null, issue.projectCwd ?? null, issue.source, issue.occurrences, issue.firstSeenAt,
      issue.lastSeenAt, issue.createdAt, issue.updatedAt, issue.resolution ?? null, json(issue.tags), json(issue.transcript), json(issue.metadata));
    db.prepare("INSERT INTO issue_events(issue_id,event_type,at,payload_json) VALUES(?,?,?,?)").run(issue.id, "created", now, json(issue));
    await this.exportJson(); return { issue, duplicate: false };
  }

  async list(status?: AnnoyedStatus): Promise<AnnoyedIssue[]> { await this.open(); const db = this.requireDb();
    const rows = status && VALID_STATUSES.has(status) ? db.prepare("SELECT * FROM issues WHERE status=? ORDER BY updated_at DESC").all(status) : db.prepare("SELECT * FROM issues ORDER BY updated_at DESC").all();
    return rows.map((r: any) => this.rowToIssue(r));
  }

  async update(id: string, patch: Partial<Pick<AnnoyedIssue, "status" | "severity" | "title" | "resolution" | "tags">>) {
    await this.open(); const db = this.requireDb(); const current = db.prepare("SELECT * FROM issues WHERE id=?").get(id); if (!current) throw new Error(`issue not found: ${id}`);
    if (patch.status && !VALID_STATUSES.has(patch.status)) throw new Error(`invalid status: ${patch.status}`);
    if (patch.severity && !VALID_SEVERITIES.has(patch.severity)) throw new Error(`invalid severity: ${patch.severity}`);
    const at = this.now().toISOString(); db.prepare("UPDATE issues SET status=COALESCE(?,status),severity=COALESCE(?,severity),title=COALESCE(?,title),resolution=COALESCE(?,resolution),tags_json=COALESCE(?,tags_json),updated_at=? WHERE id=?").run(patch.status ?? null, patch.severity ?? null, patch.title ? clean(patch.title, 240) : null, patch.resolution ? clean(patch.resolution) : null, patch.tags ? json(list(patch.tags, 24)) : null, at, id);
    db.prepare("INSERT INTO issue_events(issue_id,event_type,at,payload_json) VALUES(?,?,?,?)").run(id, "updated", at, json(patch)); await this.exportJson();
    return this.rowToIssue(db.prepare("SELECT * FROM issues WHERE id=?").get(id));
  }

  async exportJson() { await this.open(); const issues = await this.list(); const payload = { schemaVersion: 1, exportedAt: this.now().toISOString(), database: this.databasePath, issues };
    const temp = `${this.exportPath}.tmp-${process.pid}`; await writeFile(temp, JSON.stringify(payload, null, 2) + "\n", { mode: 0o600 }); await rename(temp, this.exportPath); await chmod(this.exportPath, 0o600).catch(() => undefined); return payload;
  }

  close() { this.db?.close(); this.db = undefined; }
}

async function fingerprintFor(input: Partial<AnnoyedIssue>) { const material = [input.category, input.issue, input.observed, input.toolName].map(x => clean(x).toLowerCase()).join("\0"); return Buffer.from(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(material))).toString("hex").slice(0, 32); }
