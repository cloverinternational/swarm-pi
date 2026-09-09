import { describe, expect, it, vi } from "vitest";
import { createSessionWakeup } from "../../lib/runtime/session-wakeup.ts";

describe("session wake-up delivery", () => {
  const setup = () => {
    const handlers: Record<string, Function> = {};
    const sendMessage = vi.fn();
    const wake = createSessionWakeup({ on: (name: string, cb: Function) => { handlers[name] = cb; }, sendMessage });
    return { wake, sendMessage, emit: (name: string, ctx = {}) => handlers[name]({}, ctx) };
  };
  const message = { customType: "test-wake", content: "Continue", display: true };
  it("dispatches a triggering custom message without waiting for the resulting turn", async () => {
    const h = setup();
    h.sendMessage.mockReturnValue(new Promise(() => {}));
    expect(await h.wake.send(message, h.wake.capture())).toBe(true);
    expect(h.sendMessage).toHaveBeenCalledWith(message, { triggerTurn: true, deliverAs: "steer" });
  });
  it("invalidates callbacks across shutdown, reload and session replacement", async () => {
    const h = setup();
    h.emit("session_start");
    const old = h.wake.capture();
    h.emit("session_shutdown");
    expect(await h.wake.send(message, old)).toBe(false);
    h.emit("session_start");
    expect(await h.wake.send(message, old)).toBe(false);
    expect(await h.wake.send(message, h.wake.capture())).toBe(true);
    expect(h.sendMessage).toHaveBeenCalledTimes(1);
  });
  it("reports errors instead of pretending delivery succeeded", async () => {
    const h = setup();
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      h.sendMessage.mockImplementation(() => { throw new Error("stale sender"); });
      expect(await h.wake.send(message)).toBe(false);
      expect(log).toHaveBeenCalledWith(expect.stringContaining("stale sender"));
    } finally { log.mockRestore(); }
  });
});
