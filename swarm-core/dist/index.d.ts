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
export declare function createEventId(now?: number, random?: number): string;
export declare function createIdentity(workspace: string, now?: Date, sessionId?: string): SwarmIdentity;
export declare class MemoryEventJournal implements EventJournal {
    private readonly log;
    append<T>(event: SwarmEvent<T>): void;
    entries(): readonly SwarmEvent[];
}
/** Runtime state invalidation follows Pi's session replacement boundary. */
export declare class SwarmRuntime {
    private readonly journal;
    private _identity;
    constructor(journal?: EventJournal);
    get identity(): SwarmIdentity | undefined;
    start(identity: SwarmIdentity): void;
    replace(identity: SwarmIdentity): void;
    emit<T>(type: string, data: T): SwarmEvent<T>;
    entries(): readonly SwarmEvent[];
}
