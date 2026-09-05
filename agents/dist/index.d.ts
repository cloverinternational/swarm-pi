export type AgentStatus = "queued" | "running" | "completed" | "failed" | "cancelled";
export interface Profile {
    name: string;
    systemPrompt?: string;
    capabilities?: string[];
    tools?: string[];
}
export interface Preset extends Profile {
    provider?: string;
    model?: string;
    concurrency?: number;
    worktree?: boolean;
}
export interface AgentSpec {
    id?: string;
    parentId?: string;
    sessionId?: string;
    task: string;
    provider?: string;
    model?: string;
    profile?: string;
    preset?: string;
    capabilities?: string[];
    worktree?: string | boolean;
}
export interface AgentResult {
    id: string;
    status: AgentStatus;
    output?: string;
    error?: string;
    startedAt?: string;
    completedAt: string;
    durationMs: number;
}
export interface BackgroundHandle {
    readonly id: string;
    readonly parentId?: string;
    readonly sessionId: string;
    wait(timeoutMs?: number): Promise<AgentResult>;
    cancel(): boolean;
    steer(instruction: string): boolean;
}
export interface RunnerContext {
    signal: AbortSignal;
    spec: Required<Pick<AgentSpec, "id" | "task">> & AgentSpec;
    task: string;
    profile?: Profile;
    provider?: string;
    model?: string;
    cwd: string;
    instructions: readonly string[];
    steering: readonly string[];
}
export type Runner = (ctx: RunnerContext) => Promise<string>;
/** Build a real Pi child-session runner. The child is deliberately prevented
 * from recursively spawning this control surface; the parent owns orchestration. */
export declare function createPiRunner(pi: any): Runner;
export declare class AgentManager {
    private readonly options;
    private readonly agents;
    private active;
    private readonly queue;
    private readonly profiles;
    private readonly presets;
    constructor(options?: {
        runner?: Runner;
        cwd?: string;
        concurrency?: number;
        profiles?: Profile[];
        presets?: Record<string, Preset>;
    });
    addProfile(profile: Profile): void;
    addPreset(name: string, preset: Preset): void;
    profile(name: string): Profile | undefined;
    private resolve;
    spawn(spec: AgentSpec): BackgroundHandle;
    private drain;
    private execute;
    private createWorktree;
    get(id: string): AgentResult | undefined;
    control(id: string, action: "wait" | "cancel" | "steer", value?: string, timeoutMs?: number): Promise<AgentResult | boolean> | boolean;
    list(): AgentResult[];
    onComplete(id: string, listener: (result: AgentResult) => void): () => void;
}
export declare function registerAgents(pi: any, manager?: AgentManager): AgentManager;
