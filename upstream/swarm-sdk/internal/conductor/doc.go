// Package conductor implements a Symphony-spec-compatible orchestrator for
// dispatching tracked issues to Swarm agent peers.
//
// Architecture overview:
//
//	┌─────────────────────────────────────────────────────┐
//	│  Conductor                                          │
//	│   ├─ IssueTracker  (Forgejo / GitHub / Linear)     │
//	│   ├─ WorkflowDef   (WORKFLOW.md front-matter)       │
//	│   ├─ PeerPool      (a2a discovery + spawn)          │
//	│   └─ Orchestrator  (poll loop, claim state)         │
//	└──────────────────────────────────────┬──────────────┘
//	                                       │ client.InjectEvent
//	                              client.Event bus
//	                         ┌──────┬──────┬───────┐
//	                        TUI  Hooks  Analytics  ...
//
// Every conductor lifecycle event (task dispatched, completed, failed,
// peer joined/left) flows through the unified client.Event bus so any
// Subscribe listener receives it without extra wiring.
package conductor
