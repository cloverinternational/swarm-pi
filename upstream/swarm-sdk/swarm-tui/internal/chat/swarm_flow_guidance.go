package chat

import (
	"os/exec"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// swarm-flow is an on-disk CLI (tools/swarm-flow) that runs parallel `swarm -p`
// workers in tmux over DISJOINT file sets. This guidance advertises that
// capability to the INTERACTIVE TUI agent so it reaches for the workflow when a
// task cleanly splits into independent, file-disjoint subtasks.
//
// It is deliberately gated OUT of headless mode: a `swarm -p` worker IS itself a
// swarm-flow worker, and telling it to spawn more workers would recurse. The
// caller (ExecuteMessage) checks agent.IsHeadless() before injecting this.

var (
	swarmFlowOnce      sync.Once
	swarmFlowFoundPath string
)

// swarmFlowAvailable reports whether the `swarm-flow` CLI is installed on PATH.
// We only advertise the capability when it can actually be invoked, so the
// system prompt never promises a tool the environment lacks. Resolved once.
func swarmFlowAvailable() bool {
	swarmFlowOnce.Do(func() {
		if p, err := exec.LookPath("swarm-flow"); err == nil {
			swarmFlowFoundPath = p
		}
	})
	return swarmFlowFoundPath != ""
}

// buildSwarmFlowGuidance returns the system-prompt section describing the
// swarm-flow parallel-workflow capability. Kept concise and imperative so it
// costs little context while still being actionable.
func buildSwarmFlowGuidance() string {
	return `<swarm_flow_capability>
You have ` + "`swarm-flow`" + `, a CLI that runs parallel ` + "`swarm -p`" + ` workers in tmux over DISJOINT file sets.
Reach for it when a task cleanly splits into 2+ INDEPENDENT subtasks that touch NON-overlapping files
(e.g. package A vs package B): it parallelizes the work AND keeps each worker's verbose output out of
your own context. Do NOT use it when subtasks share files (they would corrupt each other) — do those
yourself; and never nest swarm-flow inside a swarm-flow worker.

Workflow:
  swarm-flow init <name>                      # scaffolds .swarmflow/<name>/ (CONTRACT.md + w1.txt w2.txt)
  # edit each wN.txt: line 1 is the ownership header, the rest is a self-contained task prompt
  #   # FILES: exact/path/one.go, exact/path/one_test.go
  # workers do NOT see this conversation — brief each one fully (repo path, files, task, a verify cmd)
  swarm-flow run <name> --workspace "$PWD" --integrate "<build/test cmd>"
  swarm-flow status <name> | logs <name> <wN> | attach <name>

swarm-flow refuses to launch if two workers claim the same file (disjointness is enforced). After it
returns, YOU run the authoritative integration build/test and reconcile any cross-package seams.
</swarm_flow_capability>`
}

// conversationHasAssistantTurn reports whether the conversation already contains
// an assistant reply. Used to inject static Codex guidance ONCE (first turn only)
// rather than on every turn, since Codex's system prompt is backend-locked.
func conversationHasAssistantTurn(messages []*conversation.Message) bool {
	for _, m := range messages {
		if m != nil && m.Role == conversation.RoleAssistant {
			return true
		}
	}
	return false
}
