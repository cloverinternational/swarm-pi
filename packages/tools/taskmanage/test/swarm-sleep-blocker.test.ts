import { describe, expect, it } from "vitest";
import { blockedByHookText, deriveUntilRewrite, sleepBlockReason, stripHeredocs } from "../../../../.pi/lib/swarm-sleep-blocker.ts";

describe("swarm sleep blocker port", () => {
  it("blocks bare foreground sleeps with Swarm's generic message", () => {
    const reason = sleepBlockReason("sleep 0.2; printf six", {})!;
    expect(reason.startsWith("Blocked: a bare sleep can't wait for a condition. To wait until something is ready, poll with an until-loop instead. Replace this command with:\n\n  until <check>; do sleep 5; done && printf six\n\n(Adjust")).toBe(true);
    expect(sleepBlockReason("sleep 5", {})).toContain("Use one of these instead:");
    expect(sleepBlockReason("make build && sleep 5", {})).toContain("Use one of these instead:");
    expect(blockedByHookText("bash", "x")).toBe("Tool 'bash' blocked by hook: x");
  });
  it("derives a file-polling rewrite from a sleep && <cmd> chain", () => {
    expect(deriveUntilRewrite("sleep 60 && tail -20 /tmp/x.log")).toBe('until grep -q "SUCCESS\\|TIMEOUT\\|done" /tmp/x.log; do sleep 5; done && tail -20 /tmp/x.log');
    expect(deriveUntilRewrite("printf hi")).toBe("");
  });
  it("never blocks polling loops, backgrounded sleeps, allow-listed commands, heredoc bodies, or identifiers", () => {
    expect(sleepBlockReason("until grep -q ok f; do sleep 5; done && cat f", {})).toBeUndefined();
    expect(sleepBlockReason("sleep 1800 >/dev/null 2>&1 &", {})).toBeUndefined();
    expect(sleepBlockReason("sleep 5 && cmd &", {})).toBeDefined(); // && chain is foreground
    expect(sleepBlockReason("npm install && sleep 5", {})).toBeUndefined();
    expect(sleepBlockReason("sleep 5", { SWARM_ALLOW_SLEEP: "1" })).toBeUndefined();
    expect(sleepBlockReason("cat <<'EOF' > x.go\nsleep 5\nEOF\ngo build", {})).toBeUndefined();
    expect(sleepBlockReason("go test -run TestSleepTimeout ./...", {})).toBeUndefined();
    expect(sleepBlockReason("timeout 30 make", {})).toBeUndefined();
    expect(sleepBlockReason("", {})).toBeUndefined();
  });
  it("strips heredoc bodies but keeps opener lines", () => {
    expect(stripHeredocs("a <<EOF\nbody\nEOF\nb")).toBe("a <<EOF\nb");
    expect(stripHeredocs("a <<-X\n\tbody\n\tX\nb")).toBe("a <<-X\nb");
    expect(stripHeredocs("cat <<< here; sleep 1")).toBe("cat <<< here; sleep 1");
  });
});
