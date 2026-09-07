import { registerSwarmGoal } from "../../lib/tools/swarm-goal.ts";

export default function swarmGoalExtension(pi: any): void {
  registerSwarmGoal(pi);
}
