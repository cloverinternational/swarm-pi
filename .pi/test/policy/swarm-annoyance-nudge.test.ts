import { describe, expect, it } from "vitest";
import {
  AnnoyanceNudgeState,
  annoyanceMessage,
  failureFingerprint,
  failureFrictionCategory,
  frictionFingerprint,
  resetReminderSequences,
  sanitizeAnnoyanceLabel,
  wrapReminder,
} from "../../lib/policy/swarm-annoyance-nudge.ts";

describe("swarm annoyance nudge port", () => {
  it("sanitizes labels like sanitizeAnnoyanceLabel", () => {
    expect(sanitizeAnnoyanceLabel("  Bash ")).toBe("bash");
    expect(sanitizeAnnoyanceLabel("tool--failure__x")).toBe("tool-failure_x");
    expect(sanitizeAnnoyanceLabel("a b  c")).toBe("a-b-c");
    expect(sanitizeAnnoyanceLabel("!!!")).toBe("unknown");
    expect(sanitizeAnnoyanceLabel("x".repeat(100))).toHaveLength(64);
  });
  it("classifies and fingerprints like hooks/failure.go", () => {
    expect(failureFrictionCategory("error: command timed out after 60s")).toBe("timeout");
    expect(failureFrictionCategory("error: Output limit exceeded")).toBe("output-limit");
    expect(failureFrictionCategory("error: exit status 3")).toBe("tool-failure");
    // sha256("error: boom")[:16 bytes] and sha256("tool-failure:" + that)[:16 bytes]
    const inner = failureFingerprint("Error:   BOOM");
    expect(inner).toBe(failureFingerprint("error: boom"));
    expect(inner).toMatch(/^[0-9a-f]{32}$/);
    expect(frictionFingerprint("tool-failure", inner)).toMatch(/^[0-9a-f]{32}$/);
    expect(frictionFingerprint("tool-failure", inner)).not.toBe(inner);
  });
  it("renders the exact Swarm reminder and dedupes per (conversation, tool)", () => {
    resetReminderSequences();
    const state = new AnnoyanceNudgeState();
    const failed = { toolName: "bash", isError: true, content: [{ type: "text", text: "Error executing bash: Command exited with code 3: exit status 3 (error_id=err_aa)" }] };
    const first = state.onToolResult("c1", failed)!;
    expect(first.startsWith('<system-reminder source="annoyance-nudge" kind="nudge" seq="1">[ANNOYANCE REVIEW]\nTool: "bash"\nFriction class: "tool-failure"\nFingerprint: "')).toBe(true);
    expect(first.endsWith("Do not publish vague frustration or duplicate this fingerprint.</system-reminder>")).toBe(true);
    expect(state.onToolResult("c1", failed)).toBeUndefined(); // same fingerprint
    expect(state.onToolResult("c2", failed)).toMatch(/seq="2"/); // other conversation
    expect(state.onToolResult("c1", { toolName: "bash", isError: false, content: "ok" })).toBeUndefined(); // clears key
    expect(state.onToolResult("c1", failed)).toMatch(/seq="3"/);
    expect(state.onToolResult("c1", { toolName: "annoyed", isError: true, content: "x" })).toBeUndefined();
    expect(state.onToolResult("c1", { toolName: "bash", isError: true, content: "x", details: { error_type: "tool.blocked_by_hook" } })).toBeUndefined();
  });
  it("wraps like reminder.Wrap", () => {
    expect(wrapReminder("a&b", "bogus", 0, "  body ")).toBe('<system-reminder source="a&amp;b" kind="context" seq="1">body</system-reminder>');
    expect(annoyanceMessage("Bash", "tool-failure", "abc")).toContain('Tool: "bash"\nFriction class: "tool-failure"\nFingerprint: "abc"');
  });
});
