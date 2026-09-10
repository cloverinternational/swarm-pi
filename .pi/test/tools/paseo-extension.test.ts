import { beforeEach, describe, expect, it, vi } from "vitest";
const mocked = vi.hoisted(() => ({
  start: vi.fn(), setup: vi.fn(), applySetup: vi.fn(), stop: vi.fn(),
}));
vi.mock("../../lib/tools/paseo-setup.ts", () => ({
  defaultListen: () => "127.0.0.1:6767",
  paseo: { applySetup: mocked.applySetup },
  paseoStart: mocked.start, paseoSetup: mocked.setup, paseoStop: mocked.stop,
  paseoBuild: vi.fn(), paseoPair: vi.fn(), paseoStatus: vi.fn(), paseoUpdate: vi.fn(),
}));
import extension from "../../extensions/30-tools/paseo.ts";

function register() {
  const handlers = new Map<string, any>();
  let tool: any, command: any;
  extension({ on: (name, handler) => handlers.set(name, handler),
    registerTool: value => { tool = value; },
    registerCommand: (_name, spec) => { command = spec; } });
  return { handlers, tool, command };
}
beforeEach(() => {
  vi.clearAllMocks();
  mocked.start.mockResolvedValue({ success: true, listen: "127.0.0.1:6767" });
  mocked.setup.mockResolvedValue({ success: true });
  mocked.applySetup.mockResolvedValue({ success: true });
});
describe("Paseo network consent", () => {
  it("never applies network setup on session start", async () => {
    const { handlers } = register();
    const old = process.env.PASEO_HOST;
    try {
      await handlers.get("session_start")({}, { ui: { notify: vi.fn() } });
      await Promise.resolve();
      expect(mocked.start).toHaveBeenCalledOnce();
      expect(mocked.setup).not.toHaveBeenCalled();
      expect(mocked.applySetup).not.toHaveBeenCalled();
    } finally { if (old === undefined) delete process.env.PASEO_HOST; else process.env.PASEO_HOST = old; }
  });
  it("tool defaults to inspection and requires an explicit apply flag", async () => {
    const { tool } = register();
    await tool.execute("1", { action: "setup" });
    expect(mocked.setup.mock.calls.at(-1)?.[2]).toBe(false);
    await tool.execute("2", { action: "setup", apply: true });
    expect(mocked.setup.mock.calls.at(-1)?.[2]).toBe(true);
    await tool.execute("3", { action: "serve", apply: true, inspect: true });
    expect(mocked.setup.mock.calls.at(-1)?.[2]).toBe(false);
  });
  it("explicit setup command applies while inspect does not", async () => {
    const { command } = register();
    await command.handler("setup", { ui: { notify: vi.fn() } });
    expect(mocked.applySetup).toHaveBeenCalledOnce();
    await command.handler("setup inspect", { ui: { notify: vi.fn() } });
    expect(mocked.applySetup).toHaveBeenCalledOnce();
    expect(mocked.setup.mock.calls.at(-1)?.[2]).toBe(false);
  });
});
