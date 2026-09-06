package chat

import "testing"

func TestViewportPrewrapSupersedesStaleInFlightTarget(t *testing.T) {
	viewport := NewMessageList(80, 20)
	viewport.SetContent("old history content")
	app := &App{
		screen:      ScreenChat,
		msgViewport: viewport,
	}

	firstCmd := app.nextViewportPrewrapCmd()
	if firstCmd == nil {
		t.Fatal("expected initial pre-wrap command")
	}
	first := firstCmd().(viewportPrewrappedMsg)

	// Opening a history conversation and laying out the chat can change both
	// content and width before the old background wrap completes.
	viewport.SetSize(120, 20)
	viewport.SetContent("loaded history content\nwith another line")

	secondCmd := app.nextViewportPrewrapCmd()
	if secondCmd == nil {
		t.Fatal("new content/geometry target was blocked by stale in-flight work")
	}
	second := secondCmd().(viewportPrewrappedMsg)
	if second.request == first.request {
		t.Fatalf("replacement request reused generation %d", second.request)
	}

	if app.applyViewportPrewrap(first) {
		t.Fatal("stale pre-wrap result reported itself as current")
	}
	if !app.prewrapInFlight {
		t.Fatal("stale result cleared the newer request's in-flight state")
	}

	if !app.applyViewportPrewrap(second) {
		t.Fatal("newest pre-wrap result was not accepted as current")
	}
	if app.prewrapInFlight {
		t.Fatal("newest result did not clear in-flight state")
	}
	if viewport.NeedsPrewrap(viewport.Width) {
		t.Fatal("viewport still needs pre-wrap after accepting newest result")
	}

	// A slower stale request completing after the current one must not replace
	// the usable cache with old geometry.
	if app.applyViewportPrewrap(first) {
		t.Fatal("stale pre-wrap result became current after newest completion")
	}
	if viewport.preWrappedWidth != second.width {
		t.Fatalf("stale completion replaced cache width: got %d, want %d",
			viewport.preWrappedWidth, second.width)
	}
}

func TestViewportPrewrapDoesNotDuplicateSameTarget(t *testing.T) {
	viewport := NewMessageList(80, 20)
	viewport.SetContent("same target")
	app := &App{
		screen:      ScreenChat,
		msgViewport: viewport,
	}

	if cmd := app.nextViewportPrewrapCmd(); cmd == nil {
		t.Fatal("expected initial pre-wrap command")
	}
	if cmd := app.nextViewportPrewrapCmd(); cmd != nil {
		t.Fatal("same in-flight target scheduled duplicate work")
	}
}
