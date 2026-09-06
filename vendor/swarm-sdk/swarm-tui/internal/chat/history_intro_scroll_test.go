package chat

import "testing"

func TestDismissIntroStopsHistoryViewportInvalidationLoop(t *testing.T) {
	app := &App{
		screen:               ScreenChat,
		introComplete:        false,
		introGlowing:         true,
		viewportContentDirty: false,
	}

	app.dismissIntro()

	if !app.introComplete {
		t.Fatal("dismissIntro did not complete the intro")
	}
	if app.introGlowing {
		t.Fatal("dismissIntro left the settled-logo animation active")
	}
	if !app.viewNeedsRefresh {
		t.Fatal("dismissIntro did not request the final static frame")
	}

	// A glow tick already queued before history opened may still arrive. It must
	// bail without dirtying the loaded transcript or scheduling another tick.
	app.viewNeedsRefresh = false
	model, cmd := app.handleGlowTick()
	if model != app {
		t.Fatal("handleGlowTick returned a different model")
	}
	if cmd != nil {
		t.Fatal("completed intro scheduled another glow tick")
	}
	if app.viewportContentDirty {
		t.Fatal("completed intro dirtied the loaded history viewport")
	}
	if app.viewNeedsRefresh {
		t.Fatal("completed intro requested another frame")
	}
}
