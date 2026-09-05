/** Generates sortable IDs without relying on rendered UI state or a database. */
export function createEventId(now = Date.now(), random = Math.random()) {
    const time = now.toString(36).padStart(10, "0");
    const entropy = Math.floor(random * 0x100000000).toString(36).padStart(7, "0");
    return `${time}-${entropy}`;
}
export function createIdentity(workspace, now = new Date(), sessionId = createEventId(now.getTime())) {
    if (!workspace.trim())
        throw new Error("workspace must not be blank");
    if (!sessionId.trim())
        throw new Error("sessionId must not be blank");
    return { sessionId, workspace, createdAt: now.toISOString() };
}
export class MemoryEventJournal {
    log = [];
    append(event) {
        if (!event.id || !event.sessionId || !event.type)
            throw new Error("event requires id, sessionId, and type");
        if (this.log.some(previous => previous.id === event.id))
            throw new Error(`duplicate event id: ${event.id}`);
        this.log.push(structuredClone(event));
    }
    entries() { return structuredClone(this.log); }
}
/** Runtime state invalidation follows Pi's session replacement boundary. */
export class SwarmRuntime {
    journal;
    _identity;
    constructor(journal = new MemoryEventJournal()) {
        this.journal = journal;
    }
    get identity() { return this._identity && structuredClone(this._identity); }
    start(identity) {
        this._identity = structuredClone(identity);
        this.emit("session_start", identity);
    }
    replace(identity) {
        if (this._identity)
            this.emit("session_shutdown", { sessionId: this._identity.sessionId });
        this.start(identity);
    }
    emit(type, data) {
        if (!this._identity)
            throw new Error("runtime session has not started");
        const event = { id: createEventId(), type, sessionId: this._identity.sessionId, at: new Date().toISOString(), data };
        this.journal.append(event);
        return event;
    }
    entries() { return this.journal.entries(); }
}
