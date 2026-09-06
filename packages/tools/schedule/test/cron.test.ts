import { describe, expect, it } from "vitest";
import { humanReadableCron, nextCronTime, parseDelay, validateCron } from "../src/index.js";

describe("cron validation", () => {
  it.each([
    "* * * * *",
    "*/5 * * * *",
    "0 9 * * 1-5",
    "15 14 1 JAN MON",
  ])("accepts standard five-field expression %s", (expression) => {
    expect(validateCron(expression)).toBe(expression);
  });

  it.each([
    "",
    "0 9 * *",
    "0 9 * * * *",
    "60 * * * *",
    "0 24 * * *",
    "0 0 32 * *",
    "0 0 1 13 *",
    "0 0 * * 8",
    "@daily",
  ])("rejects invalid expression %j", (expression) => {
    expect(() => validateCron(expression)).toThrow();
  });

  it("calculates the next local-time occurrence", () => {
    const after = new Date(2026, 0, 5, 8, 30, 10);
    expect(nextCronTime("0 9 * * 1-5", after)).toEqual(new Date(2026, 0, 5, 9, 0, 0));
  });

  it("describes common schedules and preserves unknown expressions", () => {
    expect(humanReadableCron("*/5 * * * *")).toBe("every 5 minutes");
    expect(humanReadableCron("15 14 * * *")).toBe("15 14 * * *");
  });
});

describe("wakeup delay parsing", () => {
  it.each([
    ["500ms", 500],
    ["5m", 300_000],
    ["1h30m", 5_400_000],
    ["1.5s", 1_500],
    ["1000us", 1],
  ])("parses %s", (input, expected) => {
    expect(parseDelay(input)).toBe(expected);
  });

  it.each(["", "0s", "-1s", "1d", "5 minutes", "1m junk"])("rejects %j", (input) => {
    expect(() => parseDelay(input)).toThrow();
  });

  it("enforces a configurable upper bound", () => {
    expect(() => parseDelay("2s", 1_000)).toThrow("exceeds maximum");
  });
});
