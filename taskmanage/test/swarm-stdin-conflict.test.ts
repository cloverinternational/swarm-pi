import { describe, expect, it } from "vitest";
import { StdinConflictHook, hasPipeHeredocStdinConflict, stdinConflictAdvisoryMessage } from "../../.pi/lib/swarm-stdin-conflict.ts";
import { formatHookContext, resetReminderSequences } from "../../.pi/lib/swarm-annoyance-nudge.ts";

describe("swarm stdin-conflict port", () => {
  it("detects a pipe feeding a heredoc'd command and nothing else", () => {
    expect(hasPipeHeredocStdinConflict("printf hi | cat <<'EOF'\nx\nEOF")).toBe(true);
    expect(hasPipeHeredocStdinConflict("cat <<'EOF' | grep x\nx\nEOF")).toBe(false); // heredoc on the left
    expect(hasPipeHeredocStdinConflict("a | b; c <<EOF\nx\nEOF")).toBe(false); // separator resets
    expect(hasPipeHeredocStdinConflict("a || b <<EOF\nx\nEOF")).toBe(false); // logical OR is not a pipe
    expect(hasPipeHeredocStdinConflict("a | b <<< here")).toBe(false); // here-string
    expect(hasPipeHeredocStdinConflict("a | echo '<<EOF'")).toBe(false); // quoted
    expect(hasPipeHeredocStdinConflict("a | $(cat <<EOF\nx\nEOF\n)")).toBe(false); // inside substitution
    expect(hasPipeHeredocStdinConflict("a | b <<")).toBe(false); // no delimiter → silent
    expect(hasPipeHeredocStdinConflict("")).toBe(false);
  });
  it("advises up to three times per instance, blocks in block mode", () => {
    const hook = new StdinConflictHook("advise");
    const cmd = "printf hi | cat <<'EOF'\nx\nEOF";
    for (let i = 0; i < 3; i++) expect(hook.onBashCommand(cmd)).toEqual({ advise: stdinConflictAdvisoryMessage() });
    expect(hook.onBashCommand(cmd)).toBeUndefined();
    expect(new StdinConflictHook("block").onBashCommand(cmd)).toHaveProperty("block");
    expect(hook.onBashCommand("printf hi")).toBeUndefined();
  });
  it("wraps the advisory like FormatHookContext (kind inferred as block)", () => {
    resetReminderSequences();
    const wrapped = formatHookContext("stdin-conflict-advisory", stdinConflictAdvisoryMessage());
    expect(wrapped.startsWith('<system-reminder source="stdin-conflict-advisory" kind="block" seq="1">[stdin conflict]')).toBe(true);
    expect(wrapped.endsWith("Running as-is — this is advice, not a block.</system-reminder>")).toBe(true);
  });
});
