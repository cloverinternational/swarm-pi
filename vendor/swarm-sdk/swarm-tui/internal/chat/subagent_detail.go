package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

// collectDockEntries returns the unified dock entry list: background bash
// commands plus background sub-agents, deterministically ordered (running first,
// newest first, stable ID tiebreak) so the list does not reshuffle between
// refreshes. Finished entries age out after dockFinishedLinger.
func (a *App) collectDockEntries() []bashDockEntry {
	if a.sdk == nil {
		return nil
	}
	entries := collectBashDockEntries(a.sdk.GetBackgroundProcesses(), a.sdk.GetCurrentForegroundProcessID())

	now := time.Now()
	for _, info := range a.sdk.GetBackgroundAgents() {
		running := info.Result == nil
		cmd := info.Task
		if i := strings.IndexByte(cmd, '\n'); i >= 0 {
			cmd = cmd[:i]
		}
		var completedAt time.Time
		if info.Result != nil {
			completedAt = info.Result.EndTime
		}
		// Age out finished agents after the same linger window as bash.
		if !running && !completedAt.IsZero() && now.Sub(completedAt) > dockFinishedLinger {
			continue
		}
		e := bashDockEntry{
			Command:     "agent: " + cmd,
			Running:     running,
			Elapsed:     info.Duration,
			StartedAt:   info.StartTime,
			CompletedAt: completedAt,
			IsAgent:     true,
			AgentID:     info.AgentID,
		}
		if running {
			e.State = bgprocess.StateRunning
			if !info.StartTime.IsZero() {
				e.Elapsed = now.Sub(info.StartTime)
			}
		} else {
			e.State = bgprocess.StateCompleted
		}
		entries = append(entries, e)
	}

	sortDockEntries(entries)
	return entries
}

// openDockEntryDetail opens the takeover for whichever entry kind is selected.
func (a *App) openDockEntryDetail(e bashDockEntry) {
	if e.IsAgent {
		a.openSubAgentDetail(e.AgentID)
		return
	}
	a.openBashDetail(e.TaskID)
}

// openSubAgentDetail opens the full-screen takeover viewer for a background
// sub-agent, showing a live header (task/status/last-tool/progress) plus the
// agent's latest summary and final result. This reuses the exact same
// DetailViewer takeover as background bash commands, so sub-agents are viewed
// with the identical Enter-to-view / Esc-to-return interaction.
func (a *App) openSubAgentDetail(agentID string) {
	if a.sdk == nil || agentID == "" {
		return
	}

	// find looks up the current info for this agent on each refresh so the
	// takeover updates live while the agent runs.
	find := func() (task, status, summary, lastTool, result string, ok bool) {
		for _, info := range a.sdk.GetBackgroundAgents() {
			if info.AgentID != agentID {
				continue
			}
			task = info.Task
			status = string(info.Status)
			summary = info.Summary
			lastTool = info.LastTool
			if info.Result != nil {
				result = info.Result.Result
			}
			return task, status, summary, lastTool, result, true
		}
		return "", "", "", "", "", false
	}

	refresh := func() string {
		task, status, summary, lastTool, result, ok := find()
		if !ok {
			return "This sub-agent is no longer available."
		}
		header := []string{
			fmtDetailHeader("agent", agentID),
			fmtDetailHeader("status", status),
		}
		if lastTool != "" {
			header = append(header, fmtDetailHeader("last tool", lastTool))
		}
		var body []string
		if task != "" {
			body = append(body, "── task ──", task, "")
		}
		if summary != "" {
			body = append(body, "── progress ──", summary, "")
		}
		if result != "" {
			body = append(body, "── result ──", result)
		}
		if len(body) == 0 {
			body = append(body, "(no output yet)")
		}
		return detailBodyFromLines(header, body)
	}

	status := func() string {
		_, st, _, lastTool, _, ok := find()
		if !ok {
			return "gone"
		}
		if lastTool != "" {
			return fmt.Sprintf("%s · %s", st, lastTool)
		}
		return st
	}

	title := "sub-agent"
	if task, _, _, _, _, ok := find(); ok && task != "" {
		title = strings.SplitN(task, "\n", 2)[0]
	}

	v := NewDetailViewer(title, status, refresh)
	v.SetSize(a.width, a.height)
	a.detailViewer = v
}
