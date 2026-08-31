// Extension-side fixture. Keep these values byte-for-byte aligned with Go protocol constants.
export const RPC_CONTRACT_V1 = "swarm.chrome.rpc/1.0";
export const EVENTS_CONTRACT_V1 = "swarm.chrome.events/1.0";
export const MESSAGE_KINDS_V1 = Object.freeze(["request", "response", "event"]);
export const TERMINAL_STATES_V1 = Object.freeze(["completed", "rejected", "failed"]);
export const EXECUTION_STATES_V1 = Object.freeze(["not_started", "failed", "indeterminate"]);
