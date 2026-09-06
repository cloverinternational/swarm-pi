package chat

// attach_wiring.go wires the concrete SDK-backed implementations (the
// attach-target registry, live peer sessions, and sub-agent interjection)
// into the Ctrl+Q "Attach & Monitor" screen defined in attach_screen.go.
//
// Before this file existed, AppOptions.AttachScreenFactory was never set by
// any real caller, so Ctrl+Q always fell back to the empty
// NewAttachScreen(nil, nil, nil) in app_keyboard.go. newRealAttachScreenFactory
// is the missing piece that main.go/app_init.go hand to AppOptions once an
// *SDKIntegration exists.

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/attach"
)

// backgroundAgentInterjector adapts *SDKBackgroundAgentManager to the
// subagentInterjector interface expected by AttachScreen, resolving the
// agent by ID on each call rather than caching a reference.
type backgroundAgentInterjector struct {
	mgr *SDKBackgroundAgentManager
}

func (b backgroundAgentInterjector) Interject(id, text string) error {
	if b.mgr == nil {
		return fmt.Errorf("attach: no background agent manager available")
	}
	ag, err := b.mgr.Get(id)
	if err != nil {
		return fmt.Errorf("attach: resolve background agent %q: %w", id, err)
	}
	return ag.Interject(text)
}

// newRealAttachScreenFactory returns the AppOptions.AttachScreenFactory used
// by the running app: it lists real attach targets (local background agents
// plus discovered swarm peers), dials real peers over their control socket,
// and routes sub-agent interjection through the SDK's background agent
// manager.
//
// swarmName selects the peer-discovery namespace passed to attach.ListTargets
// (and, transitively, a2a.ListPeers) — callers without a more specific swarm
// name should pass a2a.DefaultSwarmName, matching every other production
// a2a.ListPeers call site in swarm-tui (e.g. app_a2a_debug.go).
func newRealAttachScreenFactory(sdk *SDKIntegration, swarmName string) func() *AttachScreen {
	return func() *AttachScreen {
		targetsFn := func() ([]attach.Target, error) {
			return attach.ListTargets(sdk, swarmName)
		}
		attachPeerFn := func(controlSocket string) (liveSession, error) {
			return attach.NewLivePeerSession(controlSocket)
		}
		var interjector subagentInterjector
		if sdk != nil {
			interjector = backgroundAgentInterjector{mgr: sdk.GetSDKBackgroundAgentManager()}
		}
		return NewAttachScreen(targetsFn, attachPeerFn, interjector)
	}
}
