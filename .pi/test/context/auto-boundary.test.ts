import { expect, it, vi } from "vitest";
import extension from "../../../extensions/swarm-auto/extension.ts";
function harness() {
  const hooks = new Map<string, Function[]>(), commands = new Map<string, any>();
  const pi = { on: (name: string, fn: Function) => hooks.set(name, [...hooks.get(name) ?? [], fn]), registerCommand: (name: string, command: any) => commands.set(name, command), sendMessage: vi.fn() };
  const ctx = { sessionManager: { getSessionId: () => "auto-boundary-test" }, ui: { notify: vi.fn() }, hasPendingMessages: () => false };
  extension(pi);
  const emit = async (name: string, event: any = {}) => { let result; for (const fn of hooks.get(name) ?? []) result = await fn(event, ctx); return result; };
  const boundary = { outcome: "completed", continue: false, entries: [], context: { canContinue: false, contextMessages: [{ role: "user", content: "work" }, { role: "toolResult", content: "progress" }, { role: "assistant", stopReason: "stop", content: [{ type: "text", text: "Step one verified. <!-- pi-swarm:auto-continue -->" }] }] } };
  return { pi, ctx, hooks, emit, boundary, command: (args: string) => commands.get("auto").handler(args, ctx) };
}
it("requests one native continuation without scheduling a post-settlement wake", async () => {
  const h = harness(); await h.emit("session_start"); await h.command("on"); await h.emit("input", { source: "interactive", text: "work" });
  try {
    const result = await h.emit("agent_before_settle", h.boundary);
    expect(result.continue).toBe(true); expect(result.entries[0].type).toBe("custom_message");
    expect(await h.emit("agent_before_settle", h.boundary)).toBeUndefined();
    expect(h.hooks.has("agent_settled")).toBe(false); expect(h.pi.sendMessage).not.toHaveBeenCalled();
  } finally { await h.emit("session_shutdown"); }
});
it("does not continue aborted runs, pending continuations, or after off", async () => {
  const h = harness(); await h.emit("session_start"); await h.command("on"); await h.emit("input", { source: "rpc", text: "work" });
  try {
    expect(await h.emit("agent_before_settle", { ...h.boundary, outcome: "aborted" })).toBeUndefined();
    expect(await h.emit("agent_before_settle", { ...h.boundary, continue: true })).toBeUndefined();
    await h.command("off"); expect(await h.emit("agent_before_settle", h.boundary)).toBeUndefined();
  } finally { await h.emit("session_shutdown"); }
});
it("contributes auto status only while enabled", async () => {
  const h = harness();
  const segment = () => (globalThis as any)[Symbol.for("pi-swarm-footer-segments")].get("auto-mode")?.();
  try {
    await h.emit("session_start"); expect(segment()).toBeUndefined();
    await h.command("on"); expect(segment()).toBe("auto:on");
    await h.command("off"); expect(segment()).toBeUndefined();
  } finally { await h.emit("session_shutdown"); }
});
