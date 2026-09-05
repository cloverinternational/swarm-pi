export type Transport = "stdio" | "http" | "sse";
export type FailureKind = "config" | "auth" | "timeout" | "transport" | "protocol" | "server" | "denied";
export declare class MCPError extends Error {
    readonly kind: FailureKind;
    readonly cause?: unknown | undefined;
    constructor(kind: FailureKind, message: string, cause?: unknown | undefined);
}
export interface MCPManifest {
    id: string;
    type: Transport;
    command?: string;
    args?: string[];
    url?: string;
    cwd?: string;
    environment?: string[];
    headers?: Record<string, string>;
    tools?: string[];
    excludeTools?: string[];
    enabled?: boolean;
    lazy?: boolean;
    timeoutMs?: number;
    oauth?: {
        tokenEnv: string;
        scopes?: string[];
    };
}
export interface MCPTool {
    name: string;
    description?: string;
    inputSchema?: unknown;
    serverId: string;
}
export interface MCPClient {
    initialize(): Promise<unknown>;
    listTools(): Promise<MCPTool[]>;
    callTool(name: string, args: unknown, signal?: AbortSignal): Promise<unknown>;
    close(): Promise<void>;
}
export declare function validateManifest(m: MCPManifest, closed?: boolean): string[];
export declare function createClient(m: MCPManifest, closed?: boolean): MCPClient;
export declare class MCPManager {
    private manifests;
    private opts;
    private clients;
    private discovered;
    constructor(manifests: MCPManifest[], opts?: {
        closed?: boolean;
        registerTool?: (tool: unknown) => void;
    });
    manifestsList(): {
        headers: undefined;
        oauth: {
            tokenEnv: string;
            scopes: string[] | undefined;
        } | undefined;
        id: string;
        type: Transport;
        command?: string;
        args?: string[];
        url?: string;
        cwd?: string;
        environment?: string[];
        tools?: string[];
        excludeTools?: string[];
        enabled?: boolean;
        lazy?: boolean;
        timeoutMs?: number;
    }[];
    discover(id: string): Promise<MCPTool[]>;
    call(server: string, tool: string, args: unknown, signal?: AbortSignal): Promise<unknown>;
    close(): Promise<void>;
}
export declare const manifestSchema: {
    type: string;
    additionalProperties: boolean;
    required: string[];
    properties: {
        id: {
            type: string;
        };
        type: {
            type: string;
            enum: string[];
        };
        command: {
            type: string;
        };
        args: {
            type: string;
            items: {
                type: string;
            };
        };
        url: {
            type: string;
        };
        environment: {
            type: string;
            items: {
                type: string;
            };
        };
        headers: {
            type: string;
        };
        tools: {
            type: string;
            items: {
                type: string;
            };
        };
        excludeTools: {
            type: string;
            items: {
                type: string;
            };
        };
        lazy: {
            type: string;
        };
        timeoutMs: {
            type: string;
        };
        oauth: {
            type: string;
        };
    };
};
export default function mcpExtension(pi: any): MCPManager | undefined;
