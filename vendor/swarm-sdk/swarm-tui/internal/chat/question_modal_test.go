package chat

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
)

func newQuestionModalForTest(questionType interaction.QuestionType, choices []string, responses *[]interaction.QuestionResponse) *QuestionModal {
	return NewQuestionModal(
		interaction.QuestionRequest{
			Question: "Test question",
			Type:     questionType,
			Choices:  choices,
		},
		1,
		1,
		func(response interaction.QuestionResponse) {
			*responses = append(*responses, response)
		},
		nil,
	)
}

func TestBubbleTeaV2SpaceKeyString(t *testing.T) {
	key := tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}).String()
	if key != "space" {
		t.Fatalf("Bubble Tea v2 space key string = %q, want %q", key, "space")
	}
}

func TestQuestionModalMultiChoiceTogglesBothSpaceSpellings(t *testing.T) {
	for _, key := range []string{"space", " "} {
		t.Run(key, func(t *testing.T) {
			var responses []interaction.QuestionResponse
			modal := newQuestionModalForTest(interaction.QuestionTypeMultiChoice, []string{"A", "B"}, &responses)
			modal.multiCursor = 1

			modal.updateMultiChoice(key)
			if !modal.selectedItems[1] {
				t.Fatalf("selectedItems = %v, want item under cursor toggled on", modal.selectedItems)
			}
			modal.updateMultiChoice(key)
			if modal.selectedItems[1] {
				t.Fatalf("selectedItems = %v, want item under cursor toggled off", modal.selectedItems)
			}
			if len(responses) != 0 {
				t.Fatalf("responses = %v, want no submission while toggling", responses)
			}
		})
	}
}

func TestQuestionModalChoiceSubmitsOnEnterAndSpace(t *testing.T) {
	for _, key := range []string{"enter", "space", " "} {
		t.Run(key, func(t *testing.T) {
			var responses []interaction.QuestionResponse
			modal := newQuestionModalForTest(interaction.QuestionTypeChoice, []string{"A", "B"}, &responses)
			modal.selectedIdx = 1

			modal.updateChoice(key)
			if len(responses) != 1 {
				t.Fatalf("submission count = %d, want 1", len(responses))
			}
			if responses[0].Answer != "B" {
				t.Fatalf("answer = %q, want %q", responses[0].Answer, "B")
			}
		})
	}
}

func TestQuestionModalNavigationAndNumberKeys(t *testing.T) {
	t.Run("choice", func(t *testing.T) {
		var responses []interaction.QuestionResponse
		modal := newQuestionModalForTest(interaction.QuestionTypeChoice, []string{"A", "B", "C"}, &responses)

		modal.updateChoice("down")
		if modal.selectedIdx != 1 {
			t.Fatalf("selectedIdx after down = %d, want 1", modal.selectedIdx)
		}
		modal.updateChoice("k")
		if modal.selectedIdx != 0 {
			t.Fatalf("selectedIdx after k = %d, want 0", modal.selectedIdx)
		}
		modal.updateChoice("up")
		if modal.selectedIdx != 2 {
			t.Fatalf("selectedIdx after wrapped up = %d, want 2", modal.selectedIdx)
		}
		modal.updateChoice("j")
		if modal.selectedIdx != 0 {
			t.Fatalf("selectedIdx after wrapped j = %d, want 0", modal.selectedIdx)
		}

		modal.updateChoice("3")
		if modal.selectedIdx != 2 {
			t.Fatalf("selectedIdx after number key = %d, want 2", modal.selectedIdx)
		}
		if len(responses) != 1 || responses[0].Answer != "C" {
			t.Fatalf("responses after number key = %v, want one C response", responses)
		}
	})

	t.Run("multi choice", func(t *testing.T) {
		var responses []interaction.QuestionResponse
		modal := newQuestionModalForTest(interaction.QuestionTypeMultiChoice, []string{"A", "B", "C"}, &responses)

		modal.updateMultiChoice("down")
		if modal.multiCursor != 1 {
			t.Fatalf("multiCursor after down = %d, want 1", modal.multiCursor)
		}
		modal.updateMultiChoice("k")
		if modal.multiCursor != 0 {
			t.Fatalf("multiCursor after k = %d, want 0", modal.multiCursor)
		}
		modal.updateMultiChoice("up")
		if modal.multiCursor != 2 {
			t.Fatalf("multiCursor after wrapped up = %d, want 2", modal.multiCursor)
		}
		modal.updateMultiChoice("j")
		if modal.multiCursor != 0 {
			t.Fatalf("multiCursor after wrapped j = %d, want 0", modal.multiCursor)
		}

		modal.updateMultiChoice("2")
		if !modal.selectedItems[1] {
			t.Fatalf("selectedItems after number key = %v, want item 2 selected", modal.selectedItems)
		}
		if len(responses) != 0 {
			t.Fatalf("responses after number key = %v, want no submission", responses)
		}
	})
}

func TestQuestionModalMultiChoiceEnterSubmitsSelectedItems(t *testing.T) {
	var responses []interaction.QuestionResponse
	modal := newQuestionModalForTest(interaction.QuestionTypeMultiChoice, []string{"A", "B", "C"}, &responses)

	modal.updateMultiChoice("space")
	modal.updateMultiChoice("down")
	modal.updateMultiChoice("down")
	modal.updateMultiChoice(" ")
	modal.updateMultiChoice("enter")

	if len(responses) != 1 {
		t.Fatalf("submission count = %d, want 1", len(responses))
	}
	if want := []string{"A", "C"}; !reflect.DeepEqual(responses[0].Answers, want) {
		t.Fatalf("answers = %v, want %v", responses[0].Answers, want)
	}
	if responses[0].Answer != "A, C" {
		t.Fatalf("answer = %q, want %q", responses[0].Answer, "A, C")
	}
}

func TestQuestionModalSpaceHandlersAcceptBothSpellings(t *testing.T) {
	for _, key := range []string{"space", " "} {
		t.Run("text/"+key, func(t *testing.T) {
			var responses []interaction.QuestionResponse
			modal := newQuestionModalForTest(interaction.QuestionTypeText, nil, &responses)
			modal.updateTextInput(key)
			if got := string(modal.textInput); got != " " {
				t.Fatalf("text input = %q, want one space", got)
			}
		})

		t.Run("context/"+key, func(t *testing.T) {
			var responses []interaction.QuestionResponse
			modal := newQuestionModalForTest(interaction.QuestionTypeText, nil, &responses)
			modal.focus = questionFocusContext
			modal.updateContextInput(key)
			if got := string(modal.contextInput); got != " " {
				t.Fatalf("context input = %q, want one space", got)
			}
		})

		t.Run("confirm/"+key, func(t *testing.T) {
			var responses []interaction.QuestionResponse
			modal := newQuestionModalForTest(interaction.QuestionTypeConfirm, nil, &responses)
			modal.updateConfirm(key)
			if len(responses) != 1 {
				t.Fatalf("submission count = %d, want 1", len(responses))
			}
		})
	}
}

func TestQuestionModalMultiChoiceHintNamesToggleKey(t *testing.T) {
	var responses []interaction.QuestionResponse
	modal := newQuestionModalForTest(interaction.QuestionTypeMultiChoice, []string{"A"}, &responses)

	hint := strings.ToLower(modal.renderQuestionHints(Theme{}))
	if !strings.Contains(hint, "space") || !strings.Contains(hint, "toggle") {
		t.Fatalf("multi-choice hint = %q, want space toggle keybinding", hint)
	}
}

func TestQuestionModalStructuredOptionsRenderDescriptionsAndReturnValues(t *testing.T) {
	for _, questionType := range []interaction.QuestionType{
		interaction.QuestionTypeChoice,
		interaction.QuestionTypeMultiChoice,
	} {
		t.Run(string(questionType), func(t *testing.T) {
			var responses []interaction.QuestionResponse
			modal := NewQuestionModal(interaction.QuestionRequest{
				Question: "Database?",
				Type:     questionType,
				Options: []interaction.QuestionOption{
					{Value: "postgres", Label: "PostgreSQL", Description: "Relational and full-featured"},
					{Value: "sqlite", Label: "SQLite", Description: "Embedded and simple"},
				},
			}, 1, 1, func(response interaction.QuestionResponse) {
				responses = append(responses, response)
			}, nil)

			rendered := modal.renderInput(60, Theme{})
			for _, want := range []string{"PostgreSQL", "Relational and full-featured", "SQLite", "Embedded and simple"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("structured option render missing %q:\n%s", want, rendered)
				}
			}
			modal.Update("down")
			if questionType == interaction.QuestionTypeMultiChoice {
				modal.Update("space")
				modal.Update("enter")
			} else {
				modal.Update("enter")
			}
			if len(responses) != 1 {
				t.Fatalf("responses = %#v, want one submission", responses)
			}
			if questionType == interaction.QuestionTypeChoice && responses[0].Answer != "sqlite" {
				t.Fatalf("choice answer = %q, want stable value sqlite", responses[0].Answer)
			}
			if questionType == interaction.QuestionTypeMultiChoice &&
				!reflect.DeepEqual(responses[0].Answers, []string{"sqlite"}) {
				t.Fatalf("multi answers = %#v, want stable value sqlite", responses[0].Answers)
			}
		})
	}
}

func questionnaireRequest(questions ...interaction.QuestionnaireQuestion) interaction.QuestionRequest {
	return interaction.QuestionRequest{
		Question:  "Configure the run",
		Type:      interaction.QuestionTypeQuestionnaire,
		Questions: questions,
	}
}

func questionnaireQuestion(id, label string, allowOther bool, options ...interaction.QuestionOption) interaction.QuestionnaireQuestion {
	return interaction.QuestionnaireQuestion{
		ID:         id,
		Prompt:     "Choose " + label,
		Label:      label,
		Options:    options,
		AllowOther: allowOther,
	}
}

func TestQuestionnaireSingleQuestionSubmitsOnlyAfterAnswer(t *testing.T) {
	var responses []interaction.QuestionResponse
	req := questionnaireRequest(questionnaireQuestion("speed", "Speed", false,
		interaction.QuestionOption{Value: "safe", Label: "Safe"},
		interaction.QuestionOption{Value: "fast", Label: "Fast"},
	))
	modal := NewQuestionModal(req, 1, 1, func(r interaction.QuestionResponse) {
		responses = append(responses, r)
	}, nil)

	modal.Update("right")
	modal.Update("enter") // Review cannot submit an unanswered questionnaire.
	if len(responses) != 0 {
		t.Fatalf("unanswered questionnaire submitted: %#v", responses)
	}

	modal.Update("left")
	modal.Update("down")
	modal.Update("enter")
	if len(responses) != 1 {
		t.Fatalf("submission count = %d, want 1", len(responses))
	}
	got := responses[0].QuestionnaireAnswers
	if len(got) != 1 || got[0].ID != "speed" || got[0].Value != "fast" ||
		got[0].Label != "Fast" || got[0].WasCustom || got[0].Index == nil || *got[0].Index != 1 {
		t.Fatalf("questionnaire answer = %#v, want structured Fast answer at index 1", got)
	}
}

func TestQuestionnaireNavigationPersistsEditsAndReviews(t *testing.T) {
	var responses []interaction.QuestionResponse
	req := questionnaireRequest(
		questionnaireQuestion("color", "Color", false,
			interaction.QuestionOption{Value: "red", Label: "Red"},
			interaction.QuestionOption{Value: "blue", Label: "Blue"},
		),
		questionnaireQuestion("size", "Size", false,
			interaction.QuestionOption{Value: "s", Label: "Small"},
			interaction.QuestionOption{Value: "l", Label: "Large"},
		),
	)
	modal := NewQuestionModal(req, 1, 1, func(r interaction.QuestionResponse) {
		responses = append(responses, r)
	}, nil)

	modal.Update("enter") // Red; advances to Size.
	modal.Update("left")
	if modal.questionnaireIndex != 0 || !modal.questionnaireAnswered[0] {
		t.Fatalf("answer was not retained while navigating: index=%d answered=%v",
			modal.questionnaireIndex, modal.questionnaireAnswered)
	}
	modal.Update("down")
	modal.Update("enter") // Edit Color to Blue; advances to Size.
	modal.Update("down")
	modal.Update("enter") // Large; opens review.
	if !modal.questionnaireReview {
		t.Fatal("last answer did not open final review")
	}
	modal.Update("enter")

	if len(responses) != 1 {
		t.Fatalf("submission count = %d, want 1", len(responses))
	}
	got := responses[0].QuestionnaireAnswers
	if len(got) != 2 || got[0].Value != "blue" || got[1].Value != "l" {
		t.Fatalf("answers = %#v, want edited Blue and Large answers", got)
	}
}

func TestQuestionnaireOtherEditorPasteEscapeAndSave(t *testing.T) {
	var responses []interaction.QuestionResponse
	canceled := 0
	req := questionnaireRequest(questionnaireQuestion("tool", "Tool", true,
		interaction.QuestionOption{Value: "go", Label: "Go"},
	))
	modal := NewQuestionModal(req, 1, 1, func(r interaction.QuestionResponse) {
		responses = append(responses, r)
	}, func() { canceled++ })

	modal.Update("down")
	modal.Update("enter")
	if !modal.questionnaireEditing {
		t.Fatal("Other did not open custom editor")
	}
	modal.HandlePaste("Rust")
	modal.Update("esc")
	if modal.questionnaireEditing || canceled != 0 {
		t.Fatalf("Esc in editor: editing=%v canceled=%d, want editor closed without cancel",
			modal.questionnaireEditing, canceled)
	}
	modal.Update("enter") // Reopen with the draft preserved.
	modal.Update("enter") // Save.

	if len(responses) != 1 {
		t.Fatalf("submission count = %d, want 1", len(responses))
	}
	got := responses[0].QuestionnaireAnswers[0]
	if got.Value != "Rust" || got.Label != "Rust" || !got.WasCustom || got.Index != nil {
		t.Fatalf("custom answer = %#v", got)
	}
}

func TestQuestionnaireReviewBlocksIncompleteSubmissionAndAllowsEditing(t *testing.T) {
	var responses []interaction.QuestionResponse
	req := questionnaireRequest(
		questionnaireQuestion("one", "One", false,
			interaction.QuestionOption{Value: "a", Label: "A"},
		),
		questionnaireQuestion("two", "Two", false,
			interaction.QuestionOption{Value: "b", Label: "B"},
		),
	)
	modal := NewQuestionModal(req, 1, 1, func(r interaction.QuestionResponse) {
		responses = append(responses, r)
	}, nil)

	modal.Update("tab") // Skip first.
	modal.Update("enter")
	if !modal.questionnaireReview {
		t.Fatal("answering final tab did not open review")
	}
	modal.Update("enter")
	if len(responses) != 0 {
		t.Fatal("incomplete review submitted")
	}
	modal.Update("1")
	if modal.questionnaireReview || modal.questionnaireIndex != 0 {
		t.Fatalf("numeric review edit did not open question 1: review=%v index=%d",
			modal.questionnaireReview, modal.questionnaireIndex)
	}
	modal.Update("enter")
	modal.Update("right") // Return to review from the final question.
	modal.Update("enter")
	if len(responses) != 1 {
		t.Fatalf("completed review submission count = %d, want 1", len(responses))
	}
}

func TestQuestionnaireRenderShowsTabsDescriptionsMarkersAndNarrowWidths(t *testing.T) {
	req := questionnaireRequest(
		questionnaireQuestion("runtime", "Runtime", false,
			interaction.QuestionOption{
				Value:       "node",
				Label:       "Node.js",
				Description: "Best for JavaScript services.",
			},
		),
		questionnaireQuestion("region", "Region", false,
			interaction.QuestionOption{Value: "eu", Label: "Europe"},
		),
	)
	modal := NewQuestionModal(req, 1, 1, nil, nil)
	rendered := modal.renderQuestionnaireInput(80, Theme{})
	for _, want := range []string{"Runtime", "Region", "Review", "Node.js", "Best for JavaScript services.", "○"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}
	modal.Update("enter")
	rendered = modal.renderQuestionnaireInput(80, Theme{})
	if !strings.Contains(rendered, "✓ Runtime") {
		t.Fatalf("answered tab lacks marker:\n%s", rendered)
	}

	for _, width := range []int{1, 5, 12} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			if got := modal.RenderBar(width, 8, Theme{}); got == "" {
				t.Fatal("narrow render unexpectedly empty")
			}
		})
	}
}

func TestQuestionnaireCustomEditorFitsRequestedWidth(t *testing.T) {
	req := questionnaireRequest(questionnaireQuestion("tool", "Tool", true,
		interaction.QuestionOption{Value: "go", Label: "Go"},
	))
	modal := NewQuestionModal(req, 1, 1, nil, nil)
	modal.Update("down")
	modal.Update("enter")
	modal.HandlePaste("a custom toolchain answer")

	for _, width := range []int{5, 12, 24} {
		rendered := modal.renderQuestionnaireInput(width, Theme{})
		for _, line := range strings.Split(rendered, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d rendered line width %d:\n%s", width, got, rendered)
			}
		}
	}
}
