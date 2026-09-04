export declare function validateCron(expression: string): string;
export declare function nextCronTime(expression: string, after: Date): Date;
export declare function humanReadableCron(expression: string): string;
/** Parse the positive subset of Go's time.ParseDuration syntax used by Swarm. */
export declare function parseDelay(value: string, maxMs?: number): number;
