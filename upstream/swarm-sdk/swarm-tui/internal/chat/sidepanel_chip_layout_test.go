package chat

import (
	"reflect"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestPackSidebarChipsFitsCommonControlsOnOneRow(t *testing.T) {
	chips := []string{
		lipgloss.NewStyle().Padding(0, 1).Render("YOLO"),
		lipgloss.NewStyle().Padding(0, 1).Render("OFF"),
		lipgloss.NewStyle().Padding(0, 1).Render("think"),
		lipgloss.NewStyle().Padding(0, 1).Render("verbose"),
	}

	rows := packSidebarChips(chips, 34)
	if len(rows) != 1 {
		t.Fatalf("expected common controls on one row, got %d rows: %#v", len(rows), rows)
	}
	if got := lipgloss.Width(rows[0]); got > 34 {
		t.Fatalf("row width %d exceeds sidebar width 34", got)
	}
}

func TestPackSidebarChipsWrapsWholeStyledChips(t *testing.T) {
	chips := []string{
		lipgloss.NewStyle().Padding(0, 1).Render("AUTO/YOLO"),
		lipgloss.NewStyle().Padding(0, 1).Render("AUTO"),
		lipgloss.NewStyle().Padding(0, 1).Render("think"),
		lipgloss.NewStyle().Padding(0, 1).Render("verbose"),
	}

	rows := packSidebarChips(chips, 34)
	want := []string{
		chips[0] + " " + chips[1] + " " + chips[2],
		chips[3],
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("unexpected rows:\n got: %#v\nwant: %#v", rows, want)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got > 34 {
			t.Fatalf("row %d width %d exceeds sidebar width 34", i, got)
		}
	}
}

func TestPackSidebarChipsHandlesEmptyInput(t *testing.T) {
	if rows := packSidebarChips(nil, 34); rows != nil {
		t.Fatalf("expected nil rows for empty input, got %#v", rows)
	}
}
