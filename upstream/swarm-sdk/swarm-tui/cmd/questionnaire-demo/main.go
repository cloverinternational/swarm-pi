// Command questionnaire-demo renders the production questionnaire modal with
// deterministic content for visual regression capture.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat"
)

type model struct {
	modal     *chat.QuestionModal
	width     int
	height    int
	submitted bool
}

func newModel() *model {
	request := interaction.QuestionRequest{
		Question: "Configure the service",
		Type:     interaction.QuestionTypeQuestionnaire,
		Questions: []interaction.QuestionnaireQuestion{
			{
				ID:     "runtime",
				Label:  "Runtime",
				Prompt: "Which runtime should the service use?",
				Options: []interaction.QuestionOption{
					{Value: "node", Label: "Node.js", Description: "Best for JavaScript services."},
					{Value: "go", Label: "Go", Description: "Small binaries and simple deployment."},
				},
				AllowOther: true,
			},
			{
				ID:     "region",
				Label:  "Region",
				Prompt: "Where should the service run?",
				Options: []interaction.QuestionOption{
					{Value: "us-east", Label: "US East", Description: "Lowest latency for North America."},
					{Value: "eu-west", Label: "Europe", Description: "Keeps workloads in the EU."},
				},
				AllowOther: true,
			},
		},
	}
	result := &model{width: 92, height: 24}
	result.modal = chat.NewQuestionModal(request, 1, 1, func(interaction.QuestionResponse) {
		result.submitted = true
	}, nil)
	return result
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
	case tea.KeyMsg:
		key := typed.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		// Plain-key aliases keep VHS/tmux captures deterministic even when the
		// enclosing terminal does not negotiate modified/extended key events.
		switch key {
		case "j":
			key = "down"
		case "k":
			key = "up"
		case "b":
			key = "left"
		case "f":
			key = "right"
		case "ctrl+j":
			key = "enter"
		}
		m.modal.Update(key)
	case tea.PasteMsg:
		m.modal.HandlePaste(typed.Content)
	}
	return m, nil
}

func (m *model) View() tea.View {
	status := "Production QuestionModal"
	if m.submitted {
		status += "  •  submitted"
	}
	content := status + "\n" + m.modal.RenderBar(m.width, m.height-1, chat.DefaultTheme)
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "questionnaire demo: %v\n", err)
		os.Exit(1)
	}
}
