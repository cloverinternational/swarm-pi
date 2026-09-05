const objectSchema = { type: "object", additionalProperties: false };
const result = (value) => ({ content: [{ type: "text", text: JSON.stringify(value, null, 2) }], details: value });
const failure = (error) => result({ error: error instanceof Error ? error.message : String(error) });
const id = { type: "string", minLength: 1 };
const continuation = { type: "object", additionalProperties: false, properties: {
        maxIterations: { type: "integer", minimum: 1 }, maxTokens: { type: "integer", minimum: 1 },
        maxCost: { type: "number", exclusiveMinimum: 0 }, maxNoProgress: { type: "integer", minimum: 1 },
    } };
export const goalLoopTools = {
    goal_create: { required: ["description", "doneWhen", "doneWhenReviewed"], properties: { description: { type: "string", minLength: 1 }, doneWhen: { type: "string", minLength: 1 }, doneWhenReviewed: { type: "boolean", const: true }, idempotencyKey: id } },
    goal_status: { required: ["id"], properties: { id } }, goal_pause: { required: ["id"], properties: { id } }, goal_resume: { required: ["id"], properties: { id } }, goal_complete: { required: ["id"], properties: { id } },
    loop_create: { required: ["prompt", "cadence", "continuation"], properties: { prompt: { type: "string", minLength: 1 }, cadence: { type: "string", minLength: 1 }, goalId: id, continuation }, },
    loop_status: { required: ["id"], properties: { id } }, loop_pause: { required: ["id"], properties: { id } }, loop_resume: { required: ["id"], properties: { id } }, loop_stop: { required: ["id"], properties: { id } },
};
/** Register model-callable workflow controls. Creation is always queued; resume is the explicit start. */
export function registerGoalLoopTools(pi, control) {
    const tool = (name, description, spec, execute) => pi.registerTool({ name, label: name.replace(/_/g, " "), description, parameters: { ...objectSchema, required: spec.required, properties: spec.properties }, execute: async (_callId, p) => { try {
            return result(await execute(p));
        }
        catch (e) {
            return failure(e);
        } } });
    tool("goal_create", "Create a reviewed Done-when goal in durable queued state. Does not start work.", goalLoopTools.goal_create, p => control.goalCreate(p));
    for (const [name, method, description] of [["goal_status", "goalStatus", "Read durable goal state"], ["goal_pause", "goalPause", "Pause a running goal"], ["goal_resume", "goalResume", "Explicitly start or resume a goal"], ["goal_complete", "goalComplete", "Complete a goal after its Done-when criteria are met"]])
        tool(name, description, goalLoopTools[name], p => control[method](p.id));
    tool("loop_create", "Create a bounded durable loop in queued state. Does not activate it.", goalLoopTools.loop_create, p => control.loopCreate(p));
    for (const [name, method, description] of [["loop_status", "loopStatus", "Read durable loop state"], ["loop_pause", "loopPause", "Pause a loop"], ["loop_resume", "loopResume", "Explicitly start or resume a loop"], ["loop_stop", "loopStop", "Stop a loop"]])
        tool(name, description, goalLoopTools[name], p => control[method](p.id));
}
export function registerGoalLoopCommands(pi, control) {
    const parse = (args) => {
        const trimmed = args.trim();
        const match = /^(\\S+)(?:\\s+([\\s\\S]+))?$/.exec(trimmed);
        if (!match)
            throw new Error("operation is required");
        if (match[1] === "create") {
            if (!match[2])
                throw new Error("create requires a JSON object");
            try {
                return { op: "create", input: JSON.parse(match[2]) };
            }
            catch {
                throw new Error("create requires valid JSON");
            }
        }
        if (!match[2] || /\\s/.test(match[2]))
            throw new Error("operation requires one id");
        return { op: match[1], id: match[2] };
    };
    const command = (name, operations) => pi.registerCommand?.(name, { description: `${name} workflow controls (create accepts JSON; resume explicitly starts)`, handler: async (args) => { try {
            const parsed = parse(args);
            const fn = operations[parsed.op];
            if (!fn)
                throw new Error(`unknown ${name} operation '${parsed.op}'`);
            return await fn(parsed.id, parsed.input);
        }
        catch (e) {
            return failure(e);
        } } });
    command("goal", { create: (_id, input) => control.goalCreate(input), status: id => control.goalStatus(id), pause: id => control.goalPause(id), resume: id => control.goalResume(id), complete: id => control.goalComplete(id) });
    command("loop", { create: (_id, input) => control.loopCreate(input), status: id => control.loopStatus(id), pause: id => control.loopPause(id), resume: id => control.loopResume(id), stop: id => control.loopStop(id) });
}
export function registerGoalLoop(pi, control) {
    registerGoalLoopTools(pi, control);
    registerGoalLoopCommands(pi, control);
}
