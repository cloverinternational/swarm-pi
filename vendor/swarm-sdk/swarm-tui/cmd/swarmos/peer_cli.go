package main

import "fmt"

// peerControlImplemented is the single source of truth for whether `swarm
// peer control` is wired to a working implementation. It gates two things
// that must never disagree (issue #231's exact bug was these two disagreeing):
//   - peerControl's own behavior below (real dispatch vs. "not yet supported").
//   - cli_aliases.go's warnLegacyAlias, which must not recommend switching
//     `swarm swarm attach` callers to `swarm peer control` while this is
//     false, or the deprecation warning sends users straight into the
//     "not yet supported" error peerControl returns.
//
// Flip this to true in the SAME change that wires peerControl to a working
// internal/presentationcontrol implementation, and update ADR-002's
// Compatibility table + cli_contract_test.go's TestSwarmAttachAliasDoesNot
// RecommendUnimplementedPeerControl alongside it — see that test's comment
// for the full checklist.
const peerControlImplemented = false

// peer_cli.go implements the canonical `swarm peer list|task|control` verb
// family (ADR-002 DOMAIN: "peer addresses discovered agent or presentation
// peers"; INTERFACE:
//
//	swarm peer list
//	swarm peer task <peer> <prompt> [task options]
//	swarm peer control <peer> <presentation-command> [arguments]
//
// `peer list` and `peer task` are thin dispatchers onto the EXISTING
// swarmList/swarmTask implementations already living in swarm_cli.go
// (P07 CONTRACT.md §3: "Worker A's new run_cli.go/peer_cli.go dispatch
// functions MUST reuse the existing runHeadless/swarmList/swarmTask/
// swarmAttach implementations by calling them ... do not duplicate their
// bodies"). This file intentionally contains ZERO copy-pasted logic from
// swarm_cli.go — every code path here is a direct call-through so the two
// spellings (`swarm swarm list`/`swarm peer list`,
// `swarm swarm task`/`swarm peer task`) can never drift out of sync (see
// cli_contract_test.go's TestPeerDispatchReachesSameFunctions).
//
// `peer control` is explicitly DEFERRED this phase — see
// .swarmflow/swarm-attach-architecture/p07-cli-sessions-thin-client/CONTRACT.md
// §2 "Deferred this phase": "swarm peer control full presentation-command
// parity: depends on Phase 06's internal/presentationcontrol reaching
// parity with the legacy binary protocol, already deferred by Phase 06's
// own CONTRACT.md to a later phase." Do NOT wire peerControl to
// internal/presentationcontrol, internal/inbox, or internal/lan/gossip —
// those packages are out of scope/off-limits for this file.

// runPeerCLI implements `swarmos peer <list|task|control>`.
func runPeerCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarm peer <list|task|control>")
	}
	switch args[0] {
	case "list":
		// Canonical spelling — no ADR-002 alias warning. Calls the exact
		// same swarmList() the legacy `swarm swarm list` alias (swarm_cli.go)
		// delegates to, after printing its own deprecation warning there.
		return swarmList()
	case "task":
		// Canonical spelling — no ADR-002 alias warning. Calls the exact
		// same swarmTask(args) the legacy `swarm swarm task` alias
		// (swarm_cli.go) delegates to.
		return swarmTask(args[1:])
	case "control":
		return peerControl(args[1:])
	default:
		return fmt.Errorf("unknown peer subcommand %q — valid: list, task, control", args[0])
	}
}

// peerControl implements `swarm peer control <peer> <presentation-command>
// [arguments]`. It is not yet wired to a working implementation: Phase 06's
// internal/presentationcontrol does not yet have peer-control parity with
// the legacy binary protocol swarmAttach (swarm_cli.go) already speaks over
// each peer's control socket. Rather than silently no-op, partially wire a
// subset of commands, or reach into Phase 06 packages out of this file's
// ownership, this returns a clear, stable error directing operators to the
// still-fully-functional legacy path. The exact error text is covered by
// cli_contract_test.go so it cannot silently drift.
func peerControl(args []string) error {
	_ = args
	return fmt.Errorf("not yet supported: use `swarm swarm attach` for now")
}
