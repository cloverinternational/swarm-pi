import { describe, expect, it } from "vitest";
import { readdir } from "node:fs/promises";
import { registerControlPanel } from "../../../../.pi/extensions/50-ui/control-panel.ts";
import { InProcessControlPlane } from "../src/control-plane.ts";

describe("control panel adapter", () => {
  it("registers a bounded read-only snapshot without prompt text", async () => {
    const plane = new InProcessControlPlane();
    await plane.createJob(
      {
        request: { prompt: "secret prompt", target: {} },
        idempotencyKey: "one",
      },
      "test",
    );
    const tools: any[] = [];
    registerControlPanel({ registerTool: (tool) => tools.push(tool) }, plane);

    const output = await tools[0].execute("id", {});

    expect(output.details.jobs[0].state).toBe("queued");
    expect(JSON.stringify(output)).not.toContain("secret prompt");
  });

  it("keeps test modules out of Pi's extension discovery directory", async () => {
    const entries = await readdir(new URL("../../../../.pi/extensions/", import.meta.url));
    expect(entries.filter((entry) => entry.endsWith(".test.ts"))).toEqual([]);
  });
});
