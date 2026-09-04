import { describe, expect, it } from "vitest";
import { ContractError, createId, newId, normalizeEvent, redact, redactRecord, redactUnknown, type AuditRecord } from "../src/index.js";

const id = (value: string) => createId(value);
describe("runtime contracts", () => {
  it("validates stable IDs and generates prefixed IDs", () => {
    expect(createId("tool.read")).toBe("tool.read");
    expect(newId("session")).toMatch(/^session:/);
    expect(() => createId("bad id")).toThrow(ContractError);
  });
  it("normalizes events and rejects invalid ordering values", () => {
    const event = normalizeEvent({ id: id("e:1"), sequence: 2, kind: "content", sessionId: id("s:1"), payload: "hi" });
    expect(event.timestamp).toMatch(/^\d{4}-/);
    expect(() => normalizeEvent({ id: id("e:2"), sequence: -1, kind: "started", sessionId: id("s:1") })).toThrow("sequence");
  });
  it("redacts secret-shaped keys deeply and preserves input", () => {
    const input = { apiKey: "x", nested: { password: "y", visible: "ok" } } as const;
    expect(redact(input)).toEqual({ apiKey: "[REDACTED]", nested: { password: "[REDACTED]", visible: "ok" } });
    expect(input.apiKey).toBe("x");
    expect(redactUnknown({ authorization: "Bearer x" })).toEqual({ authorization: "[REDACTED]" });
  });
  it("keeps audit records serializable and redacts details", () => {
    const record: AuditRecord = { id: id("audit:1"), timestamp: new Date().toISOString(), sessionId: id("s:1"), actorId: id("a:1"), eventType: "tool.denied", action: "read", outcome: "denied", details: { credential: "hidden" } };
    expect(redactRecord(record).details).toEqual({ credential: "[REDACTED]" });
  });
});
