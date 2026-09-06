package chat

import (
	"context"
	"fmt"
	"sync"

	hooksbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
)

// tuiCronPromptSink implements builtin.PromptSink for the interactive TUI.
//
// Without a PromptSink the cron scheduler falls back to spawning a background
// agent for every fired task — invisible to the chat session, so /loop, /goal
// and ScheduleWakeup appeared to do nothing in the TUI. With this sink, fired
// prompts are delivered into the main chat session as if the user typed them.
//
// The scheduler starts before the Bubble Tea runtime is ready to receive
// messages, so prompts fired before SetHandler are buffered and flushed once
// the app attaches its delivery callback.
type tuiCronPromptSink struct {
	mu      sync.Mutex
	handler func(prompt string)
	pending []string
}

// EnqueuePrompt implements builtin.PromptSink. Called from the scheduler's
// goroutine; the handler is invoked outside the lock since it may block on
// the app's runtime queue.
func (s *tuiCronPromptSink) EnqueuePrompt(_ context.Context, prompt string) error {
	s.mu.Lock()
	h := s.handler
	if h == nil {
		s.pending = append(s.pending, prompt)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	h(prompt)
	return nil
}

// SetHandler attaches the delivery callback and flushes any prompts that
// fired before the app was ready.
func (s *tuiCronPromptSink) SetHandler(h func(prompt string)) {
	s.mu.Lock()
	s.handler = h
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()

	if h == nil {
		return
	}
	for _, p := range pending {
		h(p)
	}
}

// cronPromptInjectMsg delivers a fired cron/wakeup prompt into the Update
// loop, where it is sent as a chat prompt to the main agent.
type cronPromptInjectMsg struct {
	prompt string
}

// maybeNotifyGoalEvaluation surfaces /goal evaluator verdicts in the TUI as
// notifications — without this the loop's decisions were invisible. Called at
// end of turn (agentResponseMsg), after the stop-hook evaluation has run.
// Deduped on (state, iteration, reason) so re-renders don't repeat it.
func (a *App) maybeNotifyGoalEvaluation() {
	if a.sdk == nil {
		return
	}
	hm := a.sdk.GetHooksManager()
	if hm == nil {
		return
	}
	gh := hm.GetGoalHook()
	if gh == nil {
		return
	}
	g := gh.GetGoal()
	if g == nil || g.State == hooksbuiltin.GoalStateCleared || g.State == hooksbuiltin.GoalStateNotYetEvaluated {
		return
	}

	key := fmt.Sprintf("%s|%d|%s", g.State, g.Iterations, g.LastReason)
	if key == a.lastGoalEvalKey {
		return
	}
	a.lastGoalEvalKey = key

	reason := g.LastReason
	if len(reason) > 100 {
		reason = reason[:97] + "..."
	}
	switch g.State {
	case hooksbuiltin.GoalStateMet:
		a.addNotification("success", fmt.Sprintf("Goal achieved — %s", reason))
	case hooksbuiltin.GoalStateImpossible:
		a.addNotification("error", fmt.Sprintf("Goal marked impossible — %s", reason))
	case hooksbuiltin.GoalStateActive:
		a.addNotification("info", fmt.Sprintf("Goal check #%d: not met — %s", g.Iterations, reason))
	}
}
