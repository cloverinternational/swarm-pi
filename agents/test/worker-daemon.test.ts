import { describe, expect, it, vi } from "vitest";
import { createWorkerDaemon } from "../src/worker-daemon.js";

describe("worker daemon", () => {
  it("starts one worker, reports health, and shuts down cleanly", async () => {
    const closeWorker = vi.fn(async () => undefined); const closeClient = vi.fn(async () => undefined);
    const client: any = { registerTask: vi.fn(), startWorker: vi.fn(async () => ({ close: closeWorker })), close: closeClient };
    const daemon = createWorkerDaemon({ db: "unused", workerId: "w1", createRuntime: (() => ({})) as any, absurdFactory: () => client });
    expect(daemon.health().state).toBe("stopped"); await daemon.start();
    expect(daemon.health()).toMatchObject({ state: "healthy", workerId: "w1", queueName: "pi-swarm" });
    expect(client.startWorker).toHaveBeenCalledWith(expect.objectContaining({ concurrency: 1, workerId: "w1" }));
    await daemon.stop(); expect(closeWorker).toHaveBeenCalled(); expect(closeClient).toHaveBeenCalled(); expect(daemon.state).toBe("stopped");
  });
});
