/** Versioned, append-only session persistence primitives.
 *
 * Entries are deliberately plain JSON so they can be stored by Pi's session
 * journal or replayed by an offline worker. Unknown future versions fail
 * closed; older entries are migrated before they reach application state.
 */
export const CURRENT_SCHEMA_VERSION = 2;

export type VersionedSnapshot<T extends object> = T & {
  schemaVersion: number;
  revision: number;
  savedAt: string;
};

export interface PersistenceAudit {
  operation: "append" | "replay" | "migrate";
  schemaVersion: number;
  revision?: number;
  at: string;
}

export function snapshot<T extends object>(state: T, revision: number, now = new Date().toISOString()): VersionedSnapshot<T> {
  return { ...structuredClone(state), schemaVersion: CURRENT_SCHEMA_VERSION, revision, savedAt: now };
}

export function replayLatest<T>(entries: readonly unknown[], type: string, migrate: (value: unknown, version: number) => T): { state: T; revision: number; audit: PersistenceAudit[] } | undefined {
  const candidates: { state: T; revision: number }[] = [];
  const audit: PersistenceAudit[] = [];
  for (const entry of entries) {
    if (!entry || typeof entry !== "object" || (entry as any).type !== type) continue;
    const data = (entry as any).data;
    const version = typeof data?.schemaVersion === "number" ? data.schemaVersion : 1;
    if (version > CURRENT_SCHEMA_VERSION) continue;
    const raw = data?.state ?? (() => {
      if (!data || typeof data !== "object") return data;
      const { schemaVersion: _schemaVersion, revision: _revision, savedAt: _savedAt, ...state } = data;
      return state;
    })();
    const state = migrate(raw, version);
    const revision = typeof data?.revision === "number" ? data.revision : 0;
    candidates.push({ state, revision });
    if (version !== CURRENT_SCHEMA_VERSION) audit.push({ operation: "migrate", schemaVersion: version, revision, at: new Date().toISOString() });
  }
  if (!candidates.length) return undefined;
  const selected = candidates.reduce((a, b) => b.revision >= a.revision ? b : a);
  audit.push({ operation: "replay", schemaVersion: CURRENT_SCHEMA_VERSION, revision: selected.revision, at: new Date().toISOString() });
  return { state: structuredClone(selected.state), revision: selected.revision, audit };
}
