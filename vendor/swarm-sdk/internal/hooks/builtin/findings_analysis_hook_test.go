package builtin

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

func TestFindingsAnalysisHookFlushesAtSessionEnd(t *testing.T) {
	hook := NewFindingsAnalysisHook(nil, nil, nil, FindingsAnalysisConfig{
		TriggerMode: TriggerOnCompaction,
		BatchSize:   10,
	}, "")

	if !hook.Filter(hooks.Event{Type: hooks.EventAgentStopped}) {
		t.Fatal("session end should flush pending findings even in compaction mode")
	}

	hook.SetConfig(FindingsAnalysisConfig{TriggerMode: TriggerDisabled})
	if hook.Filter(hooks.Event{Type: hooks.EventAgentStopped}) {
		t.Fatal("disabled findings analysis should not flush at session end")
	}
}
