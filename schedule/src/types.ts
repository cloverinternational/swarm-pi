export interface ScheduledTask {
  id: string;
  prompt: string;
  cron: string;
  recurring: boolean;
  durable: boolean;
  createdAt: Date;
  lastFiredAt?: Date;
  nextFireAt: Date;
  agentId?: string;
}

export interface CreateScheduleInput {
  prompt: string;
  cron: string;
  recurring?: boolean;
  durable?: boolean;
  agentId?: string;
}

export interface WakeupInput {
  prompt: string;
  delay: string;
}

export interface ScheduleClock {
  now(): Date;
  setTimeout(callback: () => void, delayMs: number): ReturnType<typeof setTimeout>;
  clearTimeout(timer: ReturnType<typeof setTimeout>): void;
}

export type PromptSink = (prompt: string, task: Readonly<ScheduledTask>) => void | Promise<void>;
