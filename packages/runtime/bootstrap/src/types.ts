export type BootstrapMode = "parallel" | "combined" | "off";

export interface BootstrapSettings {
  mode: BootstrapMode;
  model?: string;
  version: 1;
  updatedAt: string;
}

export interface BootstrapMemory { id?: string; text: string; tags?: string[]; source?: string; }
export interface BootstrapSkill { name: string; description?: string; source?: string; body: string; }
export interface BootstrapSelection { memories: BootstrapMemory[]; skills: BootstrapSkill[]; evidence: string[]; }
export interface BootstrapTask { subject: string; description: string; category?: string; priority?: string; }

export interface BootstrapResult {
  mode: BootstrapMode;
  status: "ready" | "disabled" | "degraded" | "cancelled" | "error";
  selection?: BootstrapSelection;
  task?: BootstrapTask;
  model: string;
  usage: { selectorCalls: number; draftCalls: number; inputChars: number; outputChars: number };
  error?: string;
}

export interface BootstrapSelector {
  (task: string, signal: AbortSignal): Promise<BootstrapSelection>;
}
export interface ParallelSelectors { memory: BootstrapSelector; skills: BootstrapSelector; }
export interface BootstrapDraft {
  (task: string, selection: BootstrapSelection, signal: AbortSignal): Promise<BootstrapTask>;
}
