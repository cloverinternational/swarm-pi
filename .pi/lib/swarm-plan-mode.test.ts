import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { HeadlessPlanApprovalBroker, PlanModeController, MAX_SUBMITTED_PLAN_BYTES, planDigest, problemBreakdownPrompt, readSubmittedPlan } from "./swarm-plan-mode.ts";

describe("PlanModeController", () => {
  it("enters, injects once, records interaction, and exits only after approval", () => {
    const c = new PlanModeController();
    expect(c.snapshotOf().state).toBe("idle");
    const entered = c.enter();
    expect(entered.state).toBe("active");
    expect(entered.planId).toBeTruthy();

    const first = c.beforeTool("Read");
    expect(first.inject).toContain("PROBLEM BREAKDOWN REQUIRED");
    expect(c.beforeTool("Grep").inject).toBeUndefined();

    const ask = c.beforeTool("ask_user_question");
    expect(ask.snapshot.interactionOccurred).toBe(true);
    c.beginApproval();
    expect(c.snapshotOf().state).toBe("awaiting_approval");
    c.finishApproval({ approved: false });
    expect(c.snapshotOf().state).toBe("active");
    c.beginApproval();
    c.finishApproval({ approved: true });
    expect(c.snapshotOf().state).toBe("idle");
    expect(c.snapshotOf().planId).toBeUndefined();
    expect(c.snapshotOf().planIdHistory).toHaveLength(1);
  });

  it("hydrates without retriggering the first-tool prompt", () => {
    const c = new PlanModeController(undefined, { state: "active", firstToolUsed: true, everUsed: true, planIdHistory: ["p1"], interactionOccurred: false });
    expect(c.beforeTool("Read").inject).toBeUndefined();
  });
});

describe("plan file boundary", () => {
  it("reads contained files and rejects traversal, directories, and oversize content", () => {
    const root = mkdtempSync(join(tmpdir(), "pi-swarm-plan-"));
    writeFileSync(join(root, "PLAN.md"), "# Plan\n");
    mkdirSync(join(root, "dir"));
    expect(readSubmittedPlan({ workspace: root }, "PLAN.md")).toBe("# Plan");
    expect(() => readSubmittedPlan({ workspace: root }, "../outside.md")).toThrow("outside workspace");
    expect(() => readSubmittedPlan({ workspace: root }, "dir")).toThrow("regular file");
    writeFileSync(join(root, "huge.md"), "x".repeat(MAX_SUBMITTED_PLAN_BYTES + 1));
    expect(() => readSubmittedPlan({ workspace: root }, "huge.md")).toThrow("exceeds");
  });
});

describe("support contracts", () => {
  it("provides stable digest and headless approval", async () => {
    expect(planDigest("x")).toMatch(/^sha256:[a-f0-9]{64}$/);
    expect(problemBreakdownPrompt).toContain("ask_user_question");
    await expect(new HeadlessPlanApprovalBroker().requestApproval("p")).resolves.toEqual({ approved: true, editedPlan: "p", clearContext: false });
  });
});
