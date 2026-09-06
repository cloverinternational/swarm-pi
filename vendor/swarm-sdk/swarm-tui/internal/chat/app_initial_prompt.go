package chat

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type initialPromptMsg struct {
	prompt string
}

func (a *App) takeInitialPrompt() string {
	prompt := strings.TrimSpace(a.appOptions.InitialPrompt)
	a.appOptions.InitialPrompt = ""
	return prompt
}

func (a *App) initialPromptCmd() tea.Cmd {
	prompt := a.takeInitialPrompt()
	if prompt == "" {
		return nil
	}
	return func() tea.Msg {
		return initialPromptMsg{prompt: prompt}
	}
}

func (a *App) handleInitialPrompt(msg initialPromptMsg) tea.Cmd {
	prompt, accepted := a.consumeInitialPrompt(msg)
	if !accepted || a.textInput == nil {
		return nil
	}
	a.startNewChatDirect()
	a.textInput.SetValue(prompt)
	return a.handleSendMessage()
}

func (a *App) consumeInitialPrompt(msg initialPromptMsg) (string, bool) {
	if a.initialPromptConsumed {
		return "", false
	}
	a.initialPromptConsumed = true

	prompt := strings.TrimSpace(msg.prompt)
	if prompt == "" {
		return "", false
	}
	return prompt, true
}
