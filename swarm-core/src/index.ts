/** Stable semantic identity and event contracts shared by Swarm adapters. */
export type EntityKind = "session" | "turn" | "tool_call" | "agent";

export interface SwarmIdentity {
  sessionId: string;
  workspace: string;
  createdAt: string;
}

export interface SwarmEvent<T = unknown> {
  id: string;
  type: string;
  sessionId: string;
  at: string;
  data: T;
}

export interface EventJournal {
  append<T>(event: SwarmEvent<T>): void;
  entries(): readonly SwarmEvent[];
}

/** Generates sortable IDs without relying on rendered UI state or a database. */
export function createEventId(now = Date.now(), random = Math.random()): string {
  const time = now.toString(36).padStart(10, "0");
  const entropy = Math.floor(random * 0x100000000).toString(36).padStart(7, "0");
  return `${time}-${entropy}`;
}

export function createIdentity(workspace: string, now = new Date(), sessionId = createEventId(now.getTime())): SwarmIdentity {
  if (!workspace.trim()) throw new Error("workspace must not be blank");
  if (!sessionId.trim()) throw new Error("sessionId must not be blank");
  return { sessionId, workspace, createdAt: now.toISOString() };
}

export class MemoryEventJournal implements EventJournal {
  private readonly log: SwarmEvent[] = [];
  append<T>(event: SwarmEvent<T>): void {
    if (!event.id || !event.sessionId || !event.type) throw new Error("event requires id, sessionId, and type");
    if (this.log.some(previous => previous.id === event.id)) throw new Error(`duplicate event id: ${event.id}`);
    this.log.push(structuredClone(event));
  }
  entries(): readonly SwarmEvent[] { return structuredClone(this.log); }
}

/** Runtime state invalidation follows Pi's session replacement boundary. */
export class SwarmRuntime {
  private _identity: SwarmIdentity | undefined;
  constructor(private readonly journal: EventJournal = new MemoryEventJournal()) {}
  get identity(): SwarmIdentity | undefined { return this._identity && structuredClone(this._identity); }
  start(identity: SwarmIdentity): void {
    this._identity = structuredClone(identity);
    this.emit("session_start", identity);
  }
  replace(identity: SwarmIdentity): void {
    if (this._identity) this.emit("session_shutdown", { sessionId: this._identity.sessionId });
    this.start(identity);
  }
  emit<T>(type: string, data: T): SwarmEvent<T> {
    if (!this._identity) throw new Error("runtime session has not started");
    const event = { id: createEventId(), type, sessionId: this._identity.sessionId, at: new Date().toISOString(), data };
    this.journal.append(event);
    return event;
  }
  entries(): readonly SwarmEvent[] { return this.journal.entries(); }
}
