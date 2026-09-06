import { describe, expect, it } from "vitest";
import { createId, InProcessControlPlane, ControlPlaneError } from "../src/index.js";

describe("InProcessControlPlane", () => {
  it("is idempotent, orders events, and returns defensive copies", async () => {
    const plane = new InProcessControlPlane();
    const agent = await plane.registerAgent({ id: createId("agent:test"), kind: "pi-session", workspace: "/tmp/work" }, "client");
    const first = await plane.createJob({ request: { prompt: "hello", target: { agentId: agent.id } }, idempotencyKey: "once" }, "client");
    const duplicate = await plane.createJob({ request: { prompt: "different", target: {} }, idempotencyKey: "once" }, "client");
    expect(duplicate).toEqual(first);
    const events = await plane.readEvents();
    expect(events.events.map(event => event.type)).toEqual(["agent.registered", "job.created"]);
    expect(events.events.map(event => event.sequence)).toEqual([1, 2]);
    const changed = await plane.getJob(first.id); (changed.request as { prompt: string }).prompt = "mutated";
    expect((await plane.getJob(first.id)).request.prompt).toBe("hello");
  });
  it("enforces lifecycle and cursor validation", async () => {
    const plane = new InProcessControlPlane();
    await expect(plane.heartbeatAgent(createId("missing"), "client")).rejects.toMatchObject<ControlPlaneError>({ code: "not_found" });
    await expect(plane.readEvents(-1)).rejects.toMatchObject<ControlPlaneError>({ code: "invalid_request" });
    const job = await plane.createJob({ request: { prompt: "run", target: {} }, idempotencyKey: "retry" }, "client");
    await expect(plane.retryJob(job.id, "client")).rejects.toMatchObject({ code: "conflict" });
    expect((await plane.cancelJob(job.id, "client")).state).toBe("cancelled");
    expect((await plane.retryJob(job.id, "client")).attempt).toBe(1);
  });
});
