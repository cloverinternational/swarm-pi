#!/usr/bin/env node
// Regenerates .pi/lib/swarm-plan-mode-texts.ts from the Go raw-string
// literals in vendor/swarm-sdk so the plan-mode hook texts Pi injects are
// byte-identical to Swarm's (box-drawing art included).
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const extract = (file, fn) => {
  const src = readFileSync(resolve(root, "vendor/swarm-sdk", file), "utf8");
  const at = src.indexOf(`func ${fn}(`);
  if (at < 0) throw new Error(`${fn} not found in ${file}`);
  const start = src.indexOf("`", at) + 1;
  return src.slice(start, src.indexOf("`", start));
};
const esc = s => s.replace(/\\/g, "\\\\").replace(/`/g, "\\`").replace(/\$\{/g, "\\${");
const breakdown = extract("internal/hooks/builtin/plan_mode_first_tool.go", "ProblemBreakdownPrompt");
const simulation = extract("internal/hooks/builtin/simulation.go", "SimulationReminderMessage");
if (simulation.includes("%")) throw new Error("SimulationReminderMessage is a Sprintf format; extend the generator");
writeFileSync(resolve(root, ".pi/lib/swarm-plan-mode-texts.ts"), `/**
 * GENERATED from vendor/swarm-sdk — do not edit by hand.
 *   internal/hooks/builtin/plan_mode_first_tool.go  ProblemBreakdownPrompt
 *   internal/hooks/builtin/simulation.go            SimulationReminderMessage
 * Regenerate: node tools/parity/gen-plan-texts.mjs
 */
export const PROBLEM_BREAKDOWN_PROMPT = \`${esc(breakdown)}\`;

export const SIMULATION_REMINDER_MESSAGE = \`${esc(simulation)}\`;
`);
console.log(`breakdown=${breakdown.length}B simulation=${simulation.length}B`);
