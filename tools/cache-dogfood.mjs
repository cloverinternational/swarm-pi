#!/usr/bin/env node
import { readFileSync } from "node:fs";
const session = process.argv[2];
if (!session) { console.error("usage: node tools/cache-dogfood.mjs <session.jsonl>"); process.exit(2); }
const rows = readFileSync(session, "utf8").split("\n").filter(Boolean).map(JSON.parse);
const telemetry = rows.filter(x => x.type === "custom" && ["pi-swarm-cache-telemetry", "pi-swarm-cache-response"].includes(x.customType));
const hooks = rows.filter(x => x.type === "custom" && x.customType === "pi-swarm-hook-state").at(-1);
console.log(JSON.stringify({ session, telemetryEntries: telemetry.length, cacheRequests: telemetry.filter(x => x.customType === "pi-swarm-cache-telemetry").map(x => x.data), hookCounts: hooks?.data?.counts ?? null, note: "Request hash changes indicate payload mutation; provider usage.cacheRead/cacheWrite is authoritative for actual cache hits." }, null, 2));
