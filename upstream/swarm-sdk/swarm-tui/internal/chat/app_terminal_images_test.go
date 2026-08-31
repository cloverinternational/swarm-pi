package chat

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
)

func encodedTestImage(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func encodedTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestTerminalImageCapabilityIgnoresUnrelatedBootstrapDA(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Unknown)
	app := &App{appOptions: AppOptions{ImageManager: manager}}

	app.observeTerminalImageCapability(uv.PrimaryDeviceAttributesEvent{1, 2})
	if got := manager.Capability(); got != termimage.Unknown {
		t.Fatalf("capability changed before query: got %v", got)
	}

	app.imageCapabilityQueryPending = true
	app.observeTerminalImageCapability(uv.PrimaryDeviceAttributesEvent{1, 2})
	if got := manager.Capability(); got != termimage.Unknown {
		t.Fatalf("unrelated DA changed capability: %v", got)
	}
	app.observeTerminalImageCapability(uv.KittyGraphicsEvent{
		Options: kitty.Options{ID: 31},
		Payload: []byte("OK"),
	})
	if got := manager.Capability(); got != termimage.Kitty {
		t.Fatalf("capability = %v, want Kitty", got)
	}
}

func TestTerminalImageCapabilityTimeoutClosesQuery(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Unknown)
	app := &App{
		appOptions:                  AppOptions{ImageManager: manager},
		imageCapabilityQueryPending: true,
	}
	app.observeTerminalImageCapability(terminalImageQueryTimeoutMsg{})
	if app.imageCapabilityQueryPending {
		t.Fatal("query remained pending after timeout")
	}
	if got := manager.Capability(); got != termimage.Unsupported {
		t.Fatalf("capability = %v, want Unsupported", got)
	}
}

func TestUnsupportedTerminalRendersUserOnlyMetadataCard(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Unsupported)
	app := &App{appOptions: AppOptions{ImageManager: manager}}
	data := encodedTestImage(t, 40, 30)
	block := app.renderImageUnavailable(data, "preview.png", "image/png", nil)
	rendered := strings.Join(block.Lines, "\n")
	for _, want := range []string{"preview.png", "PNG", "40×30", "Kitty graphics support"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("metadata card missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "▀") || strings.Contains(rendered, string('\U0010EEEE')) {
		t.Fatalf("unsupported card contains an image fallback: %q", rendered)
	}
}

func TestUnsupportedTerminalRendersJPEGMetadataWithoutPNGPreparation(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Unsupported)
	app := &App{appOptions: AppOptions{ImageManager: manager}}
	block := app.renderImageUnavailable(
		encodedTestJPEG(t, 640, 480), "screen.jpg", "image/jpeg", nil,
	)
	rendered := strings.Join(block.Lines, "\n")
	for _, want := range []string{"screen.jpg", "JPEG", "640×480", "Kitty graphics support"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("metadata card missing %q: %q", want, rendered)
		}
	}
	if app.imageCacheBytes != 0 {
		t.Fatalf("fallback metadata unexpectedly retained prepared bytes: %d", app.imageCacheBytes)
	}
}

func TestNativeImageUsesUnicodePlaceholdersOnly(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Kitty)
	app := &App{appOptions: AppOptions{ImageManager: manager}}
	app.imageCellPixelWidth, app.imageCellPixelHeight = 10, 20
	block, err := app.renderNativeImage(encodedTestImage(t, 20, 20), "preview.png", "image/png", "preview", 40)
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.Join(block.Lines, "\n")
	if !strings.Contains(rendered, string('\U0010EEEE')) || strings.Contains(rendered, "▀") {
		t.Fatalf("native image did not use only Kitty placeholders: %q", rendered)
	}
}

func TestNativeImagePreparationIsReused(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Kitty)
	app := &App{appOptions: AppOptions{ImageManager: manager}}
	data := encodedTestJPEG(t, 32, 24)

	first, err := app.preparedImage(data, "screen.jpg", "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.preparedImage(data, "screen.jpg", "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PNG) == 0 || &first.PNG[0] != &second.PNG[0] {
		t.Fatal("prepared JPEG was not reused from the cache")
	}
	if app.imageCacheBytes != len(first.PNG) {
		t.Fatalf("cached bytes = %d, want %d", app.imageCacheBytes, len(first.PNG))
	}
}

func TestTemporaryFileAttachmentIsSnapshottedForRedraw(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Unsupported)
	app := &App{appOptions: AppOptions{ImageManager: manager}}
	file, err := os.CreateTemp(t.TempDir(), "image-*.png")
	if err != nil {
		t.Fatal(err)
	}
	data := encodedTestImage(t, 12, 8)
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result := &ToolResultDisplay{
		Output: "Read image",
		Attachments: []Attachment{{
			FilePath: file.Name(), FileName: "temporary.png", MimeType: "image/png",
		}},
	}
	first := app.renderImageAttachment(result, result.Output, 80)
	if len(first.Lines) == 0 || len(result.Attachments[0].Content) == 0 {
		t.Fatal("temporary image was not snapshotted into the TUI attachment")
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatal(err)
	}
	second := app.renderImageAttachment(result, result.Output, 80)
	if !strings.Contains(strings.Join(second.Lines, "\n"), "12×8") {
		t.Fatalf("redraw depended on deleted temporary path: %q", second.Lines)
	}
}

func TestPublishTerminalImageFrameTracksVisiblePlaceholderResources(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Kitty)
	data := encodedTestImage(t, 40, 30)
	placement, err := manager.Register(termimage.Source{
		Key: "source", PNG: data, Width: 40, Height: 30,
	}, "occurrence", 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	viewport := NewMessageList(20, 2)
	viewport.OriginX = 2
	viewport.OriginY = 3
	viewport.YOffset = 2
	viewport.lines = []string{"zero", "image", "tail"}
	viewport.lastContentHash = 7
	viewport.preWrappedHash = 7
	viewport.preWrappedMapping = []int{0, 1, 2, 3}
	viewport.imageAnchors = []nativeImageAnchor{{
		SourceKey: "source",
		Line:      1,
		Column:    4,
		Columns:   8,
		Rows:      3,
		Placement: placement,
	}}
	app := &App{
		appOptions:  AppOptions{ImageManager: manager},
		screen:      ScreenChat,
		msgViewport: viewport,
	}

	app.publishTerminalImageFrame()
	frame := manager.Desired()
	if len(frame.Placements) != 1 {
		t.Fatalf("placements = %d, want 1", len(frame.Placements))
	}
	got := frame.Placements[0]
	if got.ImageID != placement.ImageID || got.PlacementID != placement.PlacementID ||
		got.Rows != 3 || got.Columns != 8 {
		t.Fatalf("visible placement changed: %+v", got)
	}
}

func TestRenderNativeImageReservesAspectRatioRows(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Kitty)
	app := &App{appOptions: AppOptions{ImageManager: manager}}
	// Pin a deterministic 10x20 cell so expectations are cell-size independent.
	app.imageCellPixelWidth, app.imageCellPixelHeight = 10, 20

	// 200x100 px at a 10x20 cell => natural 20x5 cells (fits the box).
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{R: 0x22, G: 0x66, B: 0xaa, A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	block, err := app.renderNativeImage(encoded.Bytes(), "sample.png", "image/png", "sample", 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Images) != 1 || block.Images[0].Columns != 20 || block.Images[0].Rows != 5 {
		t.Fatalf("anchor = %+v", block.Images)
	}
	if len(block.Lines) != 5 {
		t.Fatalf("reserved lines = %d, want 5", len(block.Lines))
	}
}

// TestRenderNativeImageDoesNotUpscaleSmallImage verifies that a small image is
// rendered at its natural cell size, not blown up to the full content width.
func TestRenderNativeImageDoesNotUpscaleSmallImage(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Kitty)
	app := &App{appOptions: AppOptions{ImageManager: manager}}
	app.imageCellPixelWidth, app.imageCellPixelHeight = 10, 20

	// 160x120 px at 10x20 cell => natural 16x6 cells. Even with 80 columns of
	// available width, it must stay 16x6 (no upscaling).
	img := image.NewRGBA(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{R: 0x40, G: 0x80, B: 0xc0, A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	block, err := app.renderNativeImage(encoded.Bytes(), "small.png", "image/png", "small", 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Images) != 1 || block.Images[0].Columns != 16 || block.Images[0].Rows != 6 {
		t.Fatalf("small image should render at natural 16x6, got %+v", block.Images)
	}
}

// TestObserveTerminalCellSizeRerendersImages verifies a late CSI 16 t response
// updates the stored cell size and marks image messages for re-render.
func TestObserveTerminalCellSizeRerendersImages(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Kitty)
	app := &App{appOptions: AppOptions{ImageManager: manager}}

	app.observeTerminalCellSize(uv.CellSizeEvent{Width: 8, Height: 16})
	if app.imageCellPixelWidth != 8 || app.imageCellPixelHeight != 16 {
		t.Fatalf("cell size = %dx%d, want 8x16", app.imageCellPixelWidth, app.imageCellPixelHeight)
	}
	if !app.viewNeedsRefresh {
		t.Fatal("cell-size change should request a view refresh")
	}
	w, h := app.cellPixelSize()
	if w != 8 || h != 16 {
		t.Fatalf("cellPixelSize() = %dx%d, want 8x16", w, h)
	}
}

// TestRenderNativeImageTallImageDoesNotSquish guards the aspect-preserving row
// cap. A tall (portrait) image whose aspect-correct row count exceeds
// maxNativeImageRows must shrink its Columns to keep the Columns×Rows box
// matching the source aspect, instead of stretching to the full width (Kitty
// fills the box exactly, so a mismatched box squishes the image).
func TestRenderNativeImageTallImageDoesNotSquish(t *testing.T) {
	manager := termimage.NewManager()
	manager.SetCapability(termimage.Kitty)
	app := &App{appOptions: AppOptions{ImageManager: manager}}

	const pw, ph = 40, 800 // aspect 1:20 (very tall)
	img := image.NewRGBA(image.Rect(0, 0, pw, ph))
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			img.Set(x, y, color.RGBA{R: 0x22, G: 0x66, B: 0xaa, A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	// Offer the full width (80); the aspect-correct height (800) exceeds the
	// 40-row cap, so columns must shrink rather than the image being stretched.
	block, err := app.renderNativeImage(encoded.Bytes(), "tall.png", "image/png", "tall", 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Images) != 1 {
		t.Fatalf("anchor count = %d, want 1", len(block.Images))
	}
	got := block.Images[0]
	// Rows must be capped at the maximum.
	if got.Rows != 40 {
		t.Fatalf("rows = %d, want 40 (capped)", got.Rows)
	}
	// The box aspect (columns cells wide : rows*2 pixel-equivalent tall) must
	// track the true 1:20 source aspect. Full-width (80) would be a 20x
	// horizontal stretch; the aspect-correct width is ~4.
	if got.Columns >= 20 {
		t.Fatalf("columns = %d: tall image stretched horizontally (squished); expected a narrow box", got.Columns)
	}
	// Reserved line count must equal the final rows.
	if len(block.Lines) != got.Rows {
		t.Fatalf("reserved lines = %d, want %d", len(block.Lines), got.Rows)
	}
	// Verify aspect within one cell of exact: columns ≈ rows*2*pw/ph.
	wantCols := (2 * pw * got.Rows) / ph
	if got.Columns < wantCols-1 || got.Columns > wantCols+1 {
		t.Fatalf("columns = %d, want ~%d for aspect preservation", got.Columns, wantCols)
	}
}

func TestPrewrapCompletionInvalidatesFrameCacheAndClampsOffset(t *testing.T) {
	viewport := NewMessageList(10, 2)
	viewport.SetContent("one\ntwo")
	viewport.YOffset = 99
	viewport.cachedDirty = false
	viewport.SetPreWrappedLines([]string{"one", "two"}, []int{0, 1, 2}, 10, viewport.lastContentHash)
	if !viewport.cachedDirty {
		t.Fatal("pre-wrap completion did not invalidate the frame cache")
	}
	if viewport.YOffset != 0 {
		t.Fatalf("offset = %d, want clamped 0", viewport.YOffset)
	}
}
