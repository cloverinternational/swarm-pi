import { mkdtempSync, writeFileSync, rmSync, utimesSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { readSubagentConversationCached } from "../../lib/ui/running-work-view.ts";

const dirs: string[] = [];
afterEach(() => { for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true }); });

const line = (text: string) => JSON.stringify({ type: "message", id: text, message: { role: "user", content: text } });

describe("cached subagent transcript", () => {
  it("returns the same parse until the file's size or mtime changes", () => {
    const dir = mkdtempSync(join(tmpdir(), "rwv-")); dirs.push(dir);
    const path = join(dir, "t.jsonl");
    writeFileSync(path, line("first") + "\n");
    const a = readSubagentConversationCached(path);
    expect(a?.[0]?.body).toBe("first");
    expect(readSubagentConversationCached(path)).toBe(a);

    writeFileSync(path, line("first") + "\n" + line("second") + "\n");
    utimesSync(path, new Date(), new Date(Date.now() + 5000));
    const b = readSubagentConversationCached(path);
    expect(b?.map(step => step.body)).toEqual(["first", "second"]);
  });

  it("returns undefined for a missing transcript without throwing", () => {
    expect(readSubagentConversationCached(join(tmpdir(), "missing-rwv.jsonl"))).toBeUndefined();
  });
});
