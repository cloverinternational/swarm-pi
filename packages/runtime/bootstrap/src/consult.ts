import { spawn } from "node:child_process";

/** Isolated leaf consultation: no tools, extensions, context discovery or durable session. */
export function consultModel(model: string, prompt: string, cwd: string, signal: AbortSignal): Promise<string> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) return reject(new Error("Bootstrap cancelled"));
    const child = spawn("pi", ["-p", "--model", model, "--no-session", "--no-extensions", "--no-tools", "--no-skills", "--no-context-files", "--no-prompt-templates", "--", prompt], { cwd, stdio: ["ignore", "pipe", "pipe"], detached: process.platform === "linux" });
    let output = "", error = "", failure = "";
    let escalation: ReturnType<typeof setTimeout> | undefined;
    const kill = (sig: NodeJS.Signals) => { try { if (process.platform === "linux" && child.pid) process.kill(-child.pid, sig); else child.kill(sig); } catch {} };
    const stop = (reason: string) => { failure ||= reason; kill("SIGTERM"); escalation ??= setTimeout(() => kill("SIGKILL"), 500); };
    const abort = () => stop("Bootstrap cancelled");
    const timer = setTimeout(() => stop("Bootstrap consultation timed out"), 120_000);
    const cleanup = () => { clearTimeout(timer); if (escalation) clearTimeout(escalation); signal.removeEventListener("abort", abort); };
    signal.addEventListener("abort", abort, { once: true });
    child.stdout.on("data", chunk => { output += chunk.toString(); if (output.length > 64_000) { output = output.slice(0, 64_000); stop("Bootstrap output limit exceeded"); } });
    child.stderr.on("data", chunk => { error = (error + chunk.toString()).slice(-2000); });
    child.on("error", err => { cleanup(); reject(err); });
    child.on("close", code => { cleanup(); if (failure || code !== 0) reject(new Error(failure || `Bootstrap model failed (${code}): ${error}`)); else resolve(output.trim()); });
  });
}
