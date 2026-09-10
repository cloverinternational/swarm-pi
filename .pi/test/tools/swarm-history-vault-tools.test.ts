import { PERMISSIVE_PARAMETERS, overlaySwarmToolSchemas } from "../../lib/runtime/swarm-tool-surface.ts";
import { afterEach, describe, expect, it } from "vitest";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { registerSwarmHistoryVaultTools } from "../../extensions/30-tools/swarm-history-vault-tools.ts";
import { historyGet, historySearch, normalizeHistoryGetParams } from "../../lib/tools/swarm-history-tools.ts";
import { parseVaultDuration, vaultAdd, vaultList } from "../../lib/tools/swarm-vault-tools.ts";

const roots: string[] = [];
afterEach(async () => { await Promise.all(roots.splice(0).map((p) => rm(p, { recursive: true, force: true }))); });
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "pi-history-")); roots.push(root);
  const cwd = join(root, "workspace");
  const session = (id: string, timestamp: string, texts: string[]) => [
    { type: "session", version: 3, id, timestamp, cwd },
    ...texts.map((text, i) => ({ type: "message", id: `${id}-m${i}`, parentId: i ? `${id}-m${i - 1}` : null, timestamp: new Date(new Date(timestamp).valueOf() + i * 1000).toISOString(), message: { role: i % 2 ? "assistant" : "user", content: [{ type: "text", text }] } })),
  ].map((x) => JSON.stringify(x)).join("\n");
  await writeFile(join(root, "one.jsonl"), session("session-one", "2026-01-01T00:00:00Z", ["Alpha banana request", "Gamma response", "final note"]));
  await writeFile(join(root, "two.jsonl"), session("session-two", "2026-01-02T00:00:00Z", ["Beta request", "error PR #123 happened", "banana banana"]));
  return { root, cwd, session };
}

describe("Swarm history and vault surfaces", () => {
  it("parses documented agent durations and rejects ambiguous input", () => {
    const valid: [string, number][] = [["1s", 1_000], ["15m", 900_000], ["24h", 86_400_000], ["90d", 90 * 86_400_000], ["1y", 365 * 86_400_000], ["1y30d", 395 * 86_400_000]];
    for (const [input, expected] of valid) expect(parseVaultDuration(input)).toBe(expected);
    for (const input of ["", "0s", "-1d", "+1d", "1.5d", " 1d", "1 d", "1w", "1d!", "9".repeat(65) + "d"]) expect(parseVaultDuration(input)).toBeUndefined();
  });

  it("advertises fixture-byte-equivalent descriptions and schemas for all seven tools", () => {
    const registered: any[] = [];
    registerSwarmHistoryVaultTools({ registerTool: (tool: any) => registered.push(tool), on() {}, getCwd: () => "/tmp/work" }, { historyRoot: "/tmp", cwd: "/tmp/work" });
    const fixture = JSON.parse(require("node:fs").readFileSync(new URL("../../../tools/parity/fixtures/swarm-tools.json", import.meta.url), "utf8"));
    for (const name of ["HistorySearch", "HistoryGet", "vault_add", "vault_list"]) {
      const actual = registered.find((x) => x.name === name), wanted = fixture.find((x: any) => x.function.name === name).function;
      expect(JSON.stringify(actual.description)).toBe(JSON.stringify(wanted.description));
      expect(actual.parameters).toEqual(PERMISSIVE_PARAMETERS);
      expect(JSON.stringify(overlaySwarmToolSchemas({ tools: [{ type: "function", function: { name, description: actual.description, parameters: actual.parameters } }] })!.tools[0].function.parameters)).toBe(JSON.stringify(wanted.parameters));
    }
  });

  it("searches query and regex, emits snippets, and computes stats", async () => {
    const runtime = await fixture();
    const query = await historySearch({ query: "banana", snippet: true, scope: "current" }, runtime);
    expect(query.results.map((x: any) => x.id)).toEqual(["session-two", "session-one"]);
    expect(query.results[0].snippets[0]).toMatchObject({ field: "body" });
    const regex = await historySearch({ regex: "PR #\\d{3}", scope: "current" }, runtime);
    expect(regex.results.map((x: any) => x.id)).toEqual(["session-two"]);
    const stats = await historySearch({ stats: true, ngram: 1, top_terms: 20 }, runtime);
    expect(stats).toMatchObject({ stats: true, ngram: 1, segments_scanned: 6 });
    expect(stats.terms.find((x: any) => x.term === "banana")).toMatchObject({ occurrences: 3, conversations: 2 });
  });

  it("honors normalized fields and segment search case, runtime, and ordering filters", async () => {
    const runtime = await fixture();
    const mixed = await historySearch({ query: "BANANA", fields: [" BODY "], case_sensitive: true, scope: "current" }, runtime);
    expect(mixed.results).toEqual([]);
    const insensitive = await historySearch({ query: "BANANA", segment_kind: "message", case_sensitive: false, sort: "recency", order: "asc" }, runtime);
    expect(insensitive.results.map((x: any) => x.id)).toEqual(["session-one", "session-two"]);
    const sensitive = await historySearch({ query: "BANANA", segment_kind: "message", case_sensitive: true }, runtime);
    expect(sensitive.results).toEqual([]);
  });

  it("gets tail and offset windows and reports max_chars truncation", async () => {
    const runtime = await fixture();
    const tail = await historyGet({ conversation_id: "session-one", tail: 1 }, runtime);
    expect(tail).toMatchObject({ window_start: 2, window_end: 3, omitted_message_count: 2, truncated: true });
    expect(tail.messages.map((x: any) => x.id)).toEqual(["session-one-m2"]);
    const materialized = {
      conversation_id: "session-one",
      tail: 1,
      offset: 0,
      workspace_path: "",
    };
    expect(normalizeHistoryGetParams(materialized)).toEqual({
      conversation_id: "session-one",
      tail: 1,
    });
    const neutral = await historyGet(materialized, runtime);
    expect(neutral.messages.map((x: any) => x.id)).toEqual(["session-one-m2"]);
    const page = await historyGet({ conversation_id: "session-one", offset: 1, max_messages: 1 }, runtime);
    expect(page.messages.map((x: any) => x.id)).toEqual(["session-one-m1"]);
    const first = await historyGet({ conversation_id: "session-one", offset: 0, max_messages: 1 }, runtime);
    expect(first.messages.map((x: any) => x.id)).toEqual(["session-one-m0"]);
    await expect(historyGet({
      conversation_id: "session-one",
      tail: 1,
      offset: 1,
    }, runtime)).rejects.toThrow(/tail and offset are mutually exclusive/);
    const bounded = await historyGet({ conversation_id: "session-one", max_chars: 120 }, runtime);
    expect(bounded.content_truncated).toBe(true);
    expect(bounded.omitted_message_count).toBeGreaterThan(0);
  });

  it("caps tail by max_messages and preserves identity through the registered tool", async () => {
    const runtime = await fixture();
    await writeFile(join(runtime.root, "many.jsonl"), runtime.session(
      "session-many",
      "2026-01-03T00:00:00Z",
      Array.from({ length: 6 }, (_, index) => `message-${index} ${"x".repeat(600)}`),
    ));
    const capped = await historyGet({
      conversation_id: "session-many",
      tail: 10,
      max_messages: 3,
      max_chars: 50000,
      offset: 0,
    }, runtime);
    expect(capped).toMatchObject({
      conversation_id: "session-many",
      workspace_path: runtime.cwd,
      window_start: 3,
      window_end: 6,
      rendered_message_count: 3,
    });
    expect(capped.messages.map((message: any) => message.id)).toEqual([
      "session-many-m3",
      "session-many-m4",
      "session-many-m5",
    ]);
    const registered: any[] = [];
    registerSwarmHistoryVaultTools({
      registerTool: (tool: any) => registered.push(tool),
      on() {},
      getCwd: () => runtime.cwd,
    }, { historyRoot: runtime.root, cwd: runtime.cwd });
    const get = registered.find(tool => tool.name === "HistoryGet");
    const result = await get.execute("bounded-tail", {
      conversation_id: "session-many",
      tail: 10,
      max_messages: 3,
      max_chars: 1000,
      offset: 0,
      workspace_path: "",
    });
    const value = JSON.parse(result.content[0].text);
    expect(Buffer.byteLength(result.content[0].text)).toBeLessThanOrEqual(1000);
    expect(value).toMatchObject({
      conversation_id: "session-many",
      workspace_path: runtime.cwd,
      total_message_count: 6,
      window_start: 3,
      window_end: 6,
    });
    expect(value.rendered_message_count).toBeLessThanOrEqual(3);

    const independent = await get.execute("independent", {
      conversation_id: "session-two",
      tail: 1,
      max_messages: 1,
      human_only: true,
    });
    const second = JSON.parse(independent.content[0].text);
    expect(second.conversation_id).toBe("session-two");
    expect(independent.content[0].text).not.toContain("session-many");
  });

  it("resolves duplicate IDs within the requested workspace and rejects global ambiguity", async () => {
    const runtime = await fixture();
    const otherCwd = join(runtime.root, "other-workspace");
    const duplicate = (cwd: string, text: string) => [
      { type: "session", version: 3, id: "duplicate-id", timestamp: "2026-01-04T00:00:00Z", cwd },
      { type: "message", id: `${text}-m0`, parentId: null, timestamp: "2026-01-04T00:00:01Z", message: { role: "user", content: [{ type: "text", text }] } },
    ].map(value => JSON.stringify(value)).join("\n");
    await writeFile(join(runtime.root, "duplicate-current.jsonl"), duplicate(runtime.cwd, "current"));
    await writeFile(join(runtime.root, "duplicate-other.jsonl"), duplicate(otherCwd, "other"));

    const current = await historyGet({ conversation_id: "duplicate-id" }, runtime);
    expect(current).toMatchObject({
      conversation_id: "duplicate-id",
      workspace_path: runtime.cwd,
      messages: [{ content: "current" }],
    });
    await expect(historyGet({
      conversation_id: "duplicate-id",
      all_workspaces: true,
    }, runtime)).rejects.toThrow(/conversation_id is ambiguous/);
  });

  it("adds/lists without exposing secrets, updates metadata only, and injects env", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-vault-")); roots.push(root);
    const rt = { path: join(root, "pi-vault.json") };
    expect(await vaultAdd({ id: "token", kind: "env_var", secret: "super-secret", target: "TEST_PI_SECRET" }, rt)).toMatchObject({ success: true, credentialId: "token" });
    const listed = await vaultList({}, rt);
    expect(listed.credentials).toEqual([{ id: "token", kind: "env_var", scope: "global" }]);
    expect(listed).toMatchObject({ count: 1, has_more: false });
    expect(JSON.stringify(listed)).not.toContain("super-secret");
    expect(await vaultAdd({ id: "token", kind: "env_var", allowedCommands: ["printenv *"] }, rt)).toMatchObject({ success: true, metadataOnly: true });
    expect((await vaultList({ details: true }, rt)).credentials[0]).toMatchObject({ allowedCommands: ["printenv *"] });
    const stored = await readFile(rt.path, "utf8");
    // vault/transparent.go disk format: version "2" is cleartext by design
    // (the "transparent" store), keyed by credential id with injectTarget.
    expect(JSON.parse(stored)).toMatchObject({ version: "2", credentials: { token: { kind: "env_var", value: "super-secret", injectMethod: "env", injectTarget: "TEST_PI_SECRET", allowedCommands: ["printenv *"] } } });
  });

  it("uses the plain vault without initialization and supports explicit disablement", async () => {
    const warning = "plain vault is unavailable because it was explicitly disabled; credentials are stored in ~/.swarm/vault/credentials.json";
    expect(await vaultList({}, { locked: true })).toEqual({ credentials: [], warning });
    expect(await vaultList({}, { configured: false })).toEqual({ credentials: [], warning });
    expect(await vaultAdd({ id: "x", kind: "env_var", secret: "x" }, { locked: true })).toMatchObject({ success: false, error: expect.stringContaining("vault is unavailable") });
    const root = await mkdtemp(join(tmpdir(), "pi-vault-no-init-")); roots.push(root);
    const rt = { path: join(root, "credentials.json") };
    expect(await vaultList({}, rt)).toEqual({ credentials: [], count: 0, has_more: false });
    expect(await vaultAdd({ id: "token", kind: "env_var", secret: "value" }, rt)).toMatchObject({ success: true });
  });

  it("accepts day/year expiry and serializes a future timestamp", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-vault-expiry-")); roots.push(root);
    const rt = { path: join(root, "credentials.json") };
    const before = Date.now();
    expect(await vaultAdd({ id: "long-lived", kind: "api_key", secret: "value", expire: "1y" }, rt)).toMatchObject({ success: true });
    const stored = JSON.parse(await readFile(rt.path, "utf8"));
    const expiry = Date.parse(stored.credentials["long-lived"].expiresAt);
    expect(expiry).toBeGreaterThan(before + 364 * 86_400_000);
    expect(expiry).toBeLessThanOrEqual(Date.now() + 366 * 86_400_000);
  });

  it("serializes concurrent additions without losing credentials", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-vault-concurrent-")); roots.push(root);
    const rt = { path: join(root, "credentials.json") };
    const results = await Promise.all(Array.from({ length: 24 }, (_, i) => vaultAdd({ id: `credential-${i}`, kind: "api_key", secret: `value-${i}`, expire: i % 2 ? "90d" : "1y" }, rt)));
    expect(results.every((result) => result.success)).toBe(true);
    expect((await vaultList({ limit: 100 }, rt)).credentials).toHaveLength(24);
  });

  it("keeps malformed expiration input side-effect free", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-vault-expiry-fuzz-")); roots.push(root);
    const rt = { path: join(root, "credentials.json") };
    const malformed = ["NaN", "Infinity", "1e3d", "1\n day", "1\u0000d", "999999999999999999999999999999999999999999999999d", "d1", "1dd", "1y-1d", "1/1d"];
    for (const expire of malformed) expect((await vaultAdd({ id: "x", kind: "api_key", secret: "secret", expire }, rt)).success).toBe(false);
    expect(await vaultList({}, rt)).toMatchObject({ count: 0, credentials: [] });
  });
});
