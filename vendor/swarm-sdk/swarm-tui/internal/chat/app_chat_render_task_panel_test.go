package chat

import (
	"strings"
	"testing"
)

func TestShouldShowTaskPanel(t *testing.T) {
	tests := []struct {
		name             string
		modalActive      bool
		sidePanelToggled bool
		want             bool
	}{
		{name: "normal chat", want: true},
		{name: "modal active", modalActive: true, want: false},
		{name: "side panel toggled", sidePanelToggled: true, want: false},
		{name: "modal and side panel", modalActive: true, sidePanelToggled: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldShowTaskPanel(tt.modalActive, tt.sidePanelToggled); got != tt.want {
				t.Fatalf("shouldShowTaskPanel(%v, %v) = %v, want %v", tt.modalActive, tt.sidePanelToggled, got, tt.want)
			}
		})
	}
}

func TestWriteTaskPanelAboveInput(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("viewport")
	writeTaskPanelAboveInput(&builder, "tasks", "separator")

	if got, want := builder.String(), "viewport\ntasks\nseparator"; got != want {
		t.Fatalf("task panel layout = %q, want %q", got, want)
	}
}

func TestWriteTaskPanelAboveInputWithoutTasks(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("viewport")
	writeTaskPanelAboveInput(&builder, "", "separator")

	if got, want := builder.String(), "viewport\nseparator"; got != want {
		t.Fatalf("task panel layout = %q, want %q", got, want)
	}
}
