import { registerSwarmMonitor } from "../../lib/tools/swarm-monitor.ts";

export default function swarmMonitorExtension(pi: any): void {
  registerSwarmMonitor(pi);
}
