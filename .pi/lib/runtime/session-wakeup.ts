/** Shared delivery boundary for asynchronous work owned by one Pi session. */
export interface WakeMessage {
  customType: string;
  content: string;
  display: boolean;
  details?: unknown;
}

export function createSessionWakeup(pi: any) {
  let generation = 0;
  let stopped = false;
  let context: any;
  pi.on?.("session_start", (_event: unknown, ctx: any) => {
    generation++;
    stopped = false;
    context = ctx;
  });
  pi.on?.("session_shutdown", () => { stopped = true; generation++; context = undefined; });
  const report = (error: unknown) => {
    // Never silently swallow delivery failures or turn completion into failure.
    const text = error instanceof Error ? error.message : String(error);
    console.error(`[session-wakeup] delivery failed: ${text}`);
  };
  return {
    capture(): () => boolean {
      const owner = generation;
      return () => !stopped && owner === generation;
    },
    async send(message: WakeMessage, valid: () => boolean = () => !stopped): Promise<boolean> {
      if (stopped || !valid()) return false;
      try {
        const host = typeof context?.sendMessage === "function" ? context : pi;
        if (typeof host.sendMessage !== "function") throw new Error("Pi sendMessage is unavailable");
        // A session-bound sender can resolve only after the new agent turn.
        // Dispatch without awaiting that turn: the caller may be its prerequisite.
        void Promise.resolve(host.sendMessage(message, { deliverAs: "steer", triggerTurn: true })).catch(report);
        return true;
      } catch (error) { report(error); return false; }
    },
  };
}
