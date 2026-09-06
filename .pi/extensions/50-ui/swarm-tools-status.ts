export default function swarmToolsStatus(pi: any) {
  pi.registerCommand?.("swarm-tools", { description: "List registered Swarm/Pi tools", handler: async (_args: string, ctx: any) => {
    const tools = pi.getAllTools?.() ?? [];
    const names = tools.map((t: any) => t.name).filter((n: any) => typeof n === "string" && (/search|task|skill|hook|cache|swarm/i.test(n)));
    ctx.ui?.notify?.(`Swarm tools (${names.length}): ${names.join(", ") || "none"}`, "info");
  } });
}
