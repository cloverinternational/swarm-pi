/**
 * Regenerate the vendored prompt assets from the upstream Swarm TUI source.
 * Run with `npm --prefix swarm-prompt run sync` after refreshing upstream/.
 */
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { parseGoStringConst } from "./go-const.mjs";

const goSource = new URL(
  "../../upstream/swarm-sdk/swarm-tui/internal/chat/settings/system_prompt.go",
  import.meta.url,
);
const source = readFileSync(goSource, "utf8");

const targets = [
  ["forgeSwarmSystemPrompt", "forge-swarm.txt"],
  ["swarmForgeDelegationAddendum", "delegation-addendum.txt"],
];

for (const [constant, file] of targets) {
  const body = parseGoStringConst(source, constant);
  const target = new URL(`../assets/${file}`, import.meta.url);
  writeFileSync(target, body);
  console.log(`${file.padEnd(26)} ${String(Buffer.byteLength(body)).padStart(6)} bytes  <- ${constant}`);
}

const composed = parseGoStringConst(source, "swarmForgeSystemPrompt");
const rebuilt = targets
  .map(([, file]) => readFileSync(fileURLToPath(new URL(`../assets/${file}`, import.meta.url)), "utf8"))
  .join("");
if (composed !== rebuilt) {
  console.error("FAIL: swarmForgeSystemPrompt != forge-swarm.txt + delegation-addendum.txt");
  process.exit(1);
}
console.log("composition verified against swarmForgeSystemPrompt");
