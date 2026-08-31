package chat

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
	sdkvisual "github.com/Swarm-Code/mono/swarm-sdk/internal/interaction/visual"
)

func TestTUIBroker_VisualChoice_RoutesThroughTUI(t *testing.T) {
	qBroker := NewQuestionBroker()
	broker := NewTUIInteractionBroker(qBroker, NewPermissionsBroker(nil))
	requests := make(chan interaction.QuestionRequest, 1)
	qBroker.SetDispatcher(func(msg tea.Msg) {
		if question, ok := msg.(questionRequestMsg); ok {
			requests <- question.request
		}
	})

	req := interaction.QuestionRequest{
		ID: "q-1", Type: interaction.QuestionTypeVisualChoice,
		Visual: sdkvisual.Options{Items: []sdkvisual.OptionItem{{
			Key: "ok", Label: "OK", Description: "Keep everything in the terminal",
		}}},
	}
	done := make(chan interaction.QuestionResponse, 1)
	go func() {
		response, err := broker.AskQuestion(context.Background(), req)
		if err != nil {
			t.Error(err)
			return
		}
		done <- response
	}()

	select {
	case tuiReq := <-requests:
		const displayedChoice = "OK\nKeep everything in the terminal"
		if tuiReq.Type != interaction.QuestionTypeChoice {
			t.Fatalf("type = %q", tuiReq.Type)
		}
		if len(tuiReq.Choices) != 1 || tuiReq.Choices[0] != displayedChoice {
			t.Fatalf("choices = %#v", tuiReq.Choices)
		}
		if tuiReq.Context != nil {
			if _, exists := tuiReq.Context["visual_url"]; exists {
				t.Fatal("visual question must not expose a browser URL")
			}
		}
		if err := qBroker.Respond("q-1", interaction.QuestionResponse{Answer: displayedChoice}); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("visual question was not routed to TUI")
	}

	select {
	case response := <-done:
		if response.Answer != "ok" {
			t.Errorf("answer = %q", response.Answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("broker call did not complete")
	}
}

func TestVisualTUIChoices_CardsAndSplit(t *testing.T) {
	tests := []struct {
		name      string
		primitive sdkvisual.VisualPrimitive
		want      []string
	}{
		{
			"cards",
			sdkvisual.Cards{Items: []sdkvisual.CardItem{{
				Key: "a", Title: "Alpha", Description: "First option",
				Body: sdkvisual.Markdown{Content: "**terminal body**"},
			}}},
			[]string{"Alpha\nFirst option\n**terminal body**"},
		},
		{
			"split",
			sdkvisual.SplitCompare{Items: []sdkvisual.SplitItem{
				{Key: "a", Label: "Alpha", Body: sdkvisual.Mermaid{Caption: "Flow", Diagram: "A-->B"}},
				{Key: "b", Label: "Beta"},
			}},
			[]string{"Alpha\nFlow\nA-->B", "Beta"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choices, _, _, _, err := visualTUIChoices(tt.primitive)
			if err != nil {
				t.Fatal(err)
			}
			if len(choices) != len(tt.want) {
				t.Fatalf("choices = %#v", choices)
			}
			for i := range choices {
				if choices[i] != tt.want[i] {
					t.Fatalf("choices = %#v", choices)
				}
			}
		})
	}
}

func TestVisualTUIChoices_PresentationContentBecomesDescription(t *testing.T) {
	choices, keys, multiselect, description, err := visualTUIChoices(
		sdkvisual.Markdown{Content: "# Review this in the terminal"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 1 || choices[0] != "Continue" {
		t.Fatalf("choices = %#v", choices)
	}
	if len(keys) != 1 || keys[0] != "continue" {
		t.Fatalf("keys = %#v", keys)
	}
	if multiselect {
		t.Fatal("presentation primitive must not be multiselect")
	}
	if description != "# Review this in the terminal" {
		t.Fatalf("description = %q", description)
	}
}
