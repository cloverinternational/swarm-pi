/** Adds only the missing renderer; tool-specific renderers remain authoritative. */
export declare function withDefaultToolRenderer<T extends Record<string, any>>(tool: T): T;
