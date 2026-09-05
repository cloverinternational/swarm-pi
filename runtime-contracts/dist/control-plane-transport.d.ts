import type { ControlPlane } from "./control-plane.js";
export interface JsonRpcRequest {
    readonly request_id: string;
    readonly method: string;
    readonly params?: unknown;
    readonly auth?: string;
}
export interface JsonRpcResponse {
    readonly request_id: string;
    readonly ok: boolean;
    readonly result?: unknown;
    readonly error?: {
        code: string;
        message: string;
        retryable: boolean;
    };
}
export interface LocalTransportOptions {
    readonly socketPath: string;
    readonly token: string;
    readonly maxRequestBytes?: number;
}
/** Line-delimited JSON-RPC over a permissioned Unix socket. One request per line. */
export declare class LocalControlPlaneServer {
    private readonly plane;
    private readonly options;
    private server?;
    constructor(plane: ControlPlane, options: LocalTransportOptions);
    listen(): Promise<void>;
    close(): Promise<void>;
    private handle;
    private dispatch;
    private call;
}
