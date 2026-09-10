import { afterEach, describe, expect, it, vi } from "vitest";
import { registerSwarmMonitor } from "../../lib/tools/swarm-monitor.ts";

function harness() {
  vi.useFakeTimers();
  const tools = new Map<string, any>(); const handlers = new Map<string, any[]>();
  const pi = { registerTool: (tool: any) => tools.set(tool.name, tool), on: (name: string, handler: any) => handlers.set(name, [...handlers.get(name) ?? [], handler]), sendMessage: vi.fn(), appendEntry: vi.fn() };
  registerSwarmMonitor(pi, { now: () => Date.now() });
  return { pi, tool: (input: any) => tools.get("monitor_agent").execute("test", input), emit: async (name: string, event: any = {}) => { for (const handler of handlers.get(name) ?? []) await handler(event, { sessionManager: { getBranch: () => [] } }); } };
}
afterEach(() => vi.useRealTimers());

describe("generic monitor agent", () => {
  it("creates and schedules an arbitrary target", async () => {
    const h = harness(); const created = await h.tool({ action: "create", target: "production deployment", check: "Inspect the deployment status and logs", interval: "1m" });
    expect(created.details.target).toBe("production deployment");
    await vi.advanceTimersByTimeAsync(0);
    expect(h.pi.sendMessage).toHaveBeenCalledWith(expect.objectContaining({ customType: "swarm-monitor" }), expect.anything());
  });

  it("supports pause, resume, and cancellation without provider-specific assumptions", async () => {
    const h = harness(); const id = (await h.tool({ action: "create", target: "ticket ABC-1", check: "Check whether the ticket is resolved", interval: "5m" })).details.id;
    await h.tool({ action: "pause", id }); await vi.advanceTimersByTimeAsync(300_000); expect(h.pi.sendMessage).toHaveBeenCalledTimes(0);
    await h.tool({ action: "resume", id }); await vi.advanceTimersByTimeAsync(0); expect(h.pi.sendMessage).toHaveBeenCalledTimes(1);
    await h.tool({ action: "cancel", id }); expect((await h.tool({ action: "list" })).details).toHaveLength(0);
  });

  it("records structured evidence and stops after completion", async () => {
    const h = harness(); const created = await h.tool({ action: "create", target: "batch job 42", check: "Check job status", interval: "1m", max_attempts: 2 });
    await h.tool({ action: "check", id: created.details.id });
    await h.emit("agent_end", { monitorId: created.details.id, messages: [{ content: "status: complete\nevidence: job 42 finished" }] });
    const listed = (await h.tool({ action: "list" })).details[0];
    expect(listed.lastObservation).toMatchObject({ status: "complete", retryable: false, attempts: 1 });
    expect(listed.history).toHaveLength(1);
  });
});
