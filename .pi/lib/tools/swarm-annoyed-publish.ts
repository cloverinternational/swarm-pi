/**
 * Model-boundary port of Swarm's `annoyed` tool (internal/tools/builtin/
 * annoyed.go, annoyed_issue.go, annoyed_publish.go): the model sees either
 *   <result status="ok" [severity="…"]>
 *     <issue><![CDATA[…]]></issue>
 *     <publication_url><![CDATA[…]]></publication_url>
 *     <repository><![CDATA[…]]></repository>
 *   </result>
 * or a tool error such as
 *   annoyed: GitHub API POST repos/Swarm-Code/mono/issues: exit status 4: <stderr>
 * Publication goes through `gh api --method POST repos/<repo>/issues --input -`
 * with GH_PROMPT_DISABLED=1, exactly like annoyedGHAPI. The issue BODY is
 * host-owned (Swarm renders a redacted transcript; Pi renders its board
 * record) and never reaches the model.
 */
import { spawn } from "node:child_process";
import { goQuote } from "./swarm-bash.ts";

export const DEFAULT_ANNOYED_REPOSITORY = "Swarm-Code/mono";
const MAX_TITLE_CHARS = 120;
const repositoryPattern = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

export function annoyedRepository(configured = "", environment = process.env.SWARM_ANNOYED_REPOSITORY ?? ""): string {
  let repository = configured.trim() || environment.trim() || DEFAULT_ANNOYED_REPOSITORY;
  if (!repositoryPattern.test(repository)) throw new Error(`annoyed: invalid repository ${JSON.stringify(repository)}; expected owner/name`);
  return repository;
}

export function normalizeAnnoyedSeverity(value: unknown): string {
  const v = String(value ?? "").trim().toLowerCase();
  return v === "low" || v === "medium" || v === "high" ? v : "";
}

export function annoyedIssueTitle(issue: string): string {
  const title = issue.split(/\s+/).filter(Boolean).join(" ");
  if (title === "") return "Agent feedback";
  const runes = [...title];
  return runes.length > MAX_TITLE_CHARS ? runes.slice(0, MAX_TITLE_CHARS - 1).join("") + "…" : title;
}

export function annoyedPublicTitle(issue: string, severity: string): string {
  return annoyedIssueTitle(severity ? `[${severity.toUpperCase()}] ${issue}` : issue);
}

/** Go exec error text for a non-zero exit / signal (os/exec ExitError.Error()). */
const exitText = (code: number | null, signal: NodeJS.Signals | null) => code !== null ? `exit status ${code}` : `signal: ${String(signal ?? "").toLowerCase()}`;

export interface GHRunner { (args: string[], stdin: string): Promise<{ code: number | null; signal: NodeJS.Signals | null; stdout: string; stderr: string; spawnError?: Error }> }
const defaultGH: GHRunner = (args, stdin) => new Promise((resolveP) => {
  const child = spawn("gh", args, { env: { ...process.env, GH_PROMPT_DISABLED: "1" }, stdio: ["pipe", "pipe", "pipe"] });
  const out: Buffer[] = [], err: Buffer[] = [];
  child.stdout.on("data", (d: Buffer) => out.push(d)); child.stderr.on("data", (d: Buffer) => err.push(d));
  child.on("error", (e) => resolveP({ code: null, signal: null, stdout: "", stderr: "", spawnError: e }));
  child.on("close", (code, signal) => resolveP({ code, signal, stdout: Buffer.concat(out).toString(), stderr: Buffer.concat(err).toString() }));
  child.stdin.end(stdin);
});

export async function publishAnnoyedIssue(repository: string, title: string, body: string, gh: GHRunner = defaultGH): Promise<string> {
  const endpoint = `repos/${repository}/issues`;
  const result = await gh(["api", "--method", "POST", endpoint, "--input", "-"], JSON.stringify({ title, body }));
  if (result.spawnError) throw new Error(`annoyed: GitHub API POST ${endpoint}: ${result.spawnError.message}`);
  if (result.code !== 0) {
    const detail = result.stderr.trim();
    const exit = exitText(result.code, result.signal);
    throw new Error(detail ? `annoyed: GitHub API POST ${endpoint}: ${exit}: ${detail}` : `annoyed: GitHub API POST ${endpoint}: ${exit}`);
  }
  let created: any;
  try { created = JSON.parse(result.stdout); } catch (e) { throw new Error(`annoyed: decode GitHub API POST ${endpoint} response: ${(e as Error).message}`); }
  const url = String(created?.html_url ?? "");
  let parsed: URL | undefined; try { parsed = new URL(url); } catch { /* invalid */ }
  if (!parsed || parsed.protocol !== "https:" || !parsed.host || !parsed.pathname.includes("/issues/")) throw new Error("annoyed: gh returned invalid issue URL");
  return url;
}

/** tools.NewXML("result").Attr("status","ok").Field(…)[.Attr("severity", …)] — attrs in call order. */
export function annoyedResultXML(issue: string, publicationURL: string, repository: string, severity: string): string {
  const attrs = [`status="ok"`, ...(severity ? [`severity=${goQuote(severity)}`] : [])];
  const field = (tag: string, value: string) => `  <${tag}><![CDATA[${value}]]></${tag}>\n`;
  return `<result ${attrs.join(" ")}>\n${field("issue", issue)}${field("publication_url", publicationURL)}${field("repository", repository)}</result>`;
}
