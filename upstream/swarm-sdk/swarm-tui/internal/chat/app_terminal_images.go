package chat

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/components"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
	uv "github.com/charmbracelet/ultraviolet"
)

const (
	maxNativeImageBytes  = 20 << 20
	maxNativeImagePixels = 25_000_000
	maxNativeImageCols   = 80
	maxNativeImageRows   = 40
	maxImageCacheBytes   = 64 << 20
	maxImageCacheEntries = 256

	// Fallback terminal cell pixel size (~1:2 width:height) used only until the
	// terminal answers the CSI 16 t cell-size query. Roughly a 10px font cell.
	defaultCellPixelWidth  = 10
	defaultCellPixelHeight = 20
)

type imageCacheEntry struct {
	metadata   components.ImageMetadata
	metadataOK bool
	prepared   components.PreparedImage
	preparedOK bool
	bytes      int
	lastUsed   uint64
}

type terminalImageQueryTimeoutMsg struct{}

func (a *App) observeTerminalImageCapability(msg any) {
	manager := a.appOptions.ImageManager
	if manager == nil {
		return
	}

	changed := false
	switch event := msg.(type) {
	case terminalImageQueryTimeoutMsg:
		if !a.imageCapabilityQueryPending {
			return
		}
		a.imageCapabilityQueryPending = false
		changed = manager.FinishProbe()
	case uv.KittyGraphicsEvent:
		if event.Options.ID <= 0 {
			return
		}
		changed = manager.ObserveResponse(termimage.Response{
			ID: uint32(event.Options.ID), Message: string(event.Payload), OK: string(event.Payload) == "OK",
		})
	case uv.UnknownApcEvent:
		response, ok := termimage.ParseResponse(string(event))
		if !ok {
			return
		}
		changed = manager.ObserveResponse(response)
	case tea.ResumeMsg:
		manager.Invalidate()
		changed = true
	default:
		return
	}

	if !changed {
		return
	}
	for i := range a.messages {
		if messageContainsImages(&a.messages[i]) {
			a.messages[i].MarkDirty()
		}
	}
	a.viewportContentDirty = true
	a.viewNeedsRefresh = true
}

func terminalImageQueryTimeout() tea.Cmd {
	return tea.Tick(750*time.Millisecond, func(time.Time) tea.Msg {
		return terminalImageQueryTimeoutMsg{}
	})
}

// observeTerminalCellSize records the terminal's cell pixel dimensions reported
// in response to the CSI 16 t query. Knowing the exact cell size lets images be
// sized at their true on-screen resolution instead of being stretched to the
// full content width. A late response re-renders any image messages.
func (a *App) observeTerminalCellSize(msg any) {
	event, ok := msg.(uv.CellSizeEvent)
	if !ok {
		return
	}
	w, h := event.Width, event.Height
	if w <= 0 || h <= 0 || (w == a.imageCellPixelWidth && h == a.imageCellPixelHeight) {
		return
	}
	a.imageCellPixelWidth = w
	a.imageCellPixelHeight = h
	for i := range a.messages {
		if messageContainsImages(&a.messages[i]) {
			a.messages[i].MarkDirty()
		}
	}
	a.viewportContentDirty = true
	a.viewNeedsRefresh = true
}

// cellPixelSize returns the terminal cell size in pixels, falling back to a
// typical ~1:2 cell until the terminal answers the CSI 16 t query.
func (a *App) cellPixelSize() (int, int) {
	w, h := a.imageCellPixelWidth, a.imageCellPixelHeight
	if w <= 0 {
		w = defaultCellPixelWidth
	}
	if h <= 0 {
		h = defaultCellPixelHeight
	}
	return w, h
}

// nativeImageCells computes the terminal-cell footprint for an image: sized at
// its true on-screen resolution (never upscaled), aspect-preserved, and capped
// to the available content width (maxColumns) and the hard row/column limits.
// Kitty stretches the source to exactly fill the returned box, so the box is
// chosen to match the image's real proportions given the terminal cell size.
func (a *App) nativeImageCells(pixelWidth, pixelHeight, maxColumns int) (int, int) {
	cellW, cellH := a.cellPixelSize()
	// Natural size in whole cells at 1:1 pixels (nearest, minimum one cell).
	naturalCols := max((pixelWidth+cellW/2)/cellW, 1)
	naturalRows := max((pixelHeight+cellH/2)/cellH, 1)

	boxCols := min(max(maxColumns, 1), maxNativeImageCols)
	boxRows := maxNativeImageRows

	cols, rows := naturalCols, naturalRows
	// Fit width first, deriving the paired dimension from the natural ratio so
	// aspect is preserved. Only ever scales down (no upscaling past native).
	if cols > boxCols {
		cols = boxCols
		rows = max((naturalRows*boxCols+naturalCols/2)/naturalCols, 1)
	}
	// Then fit height the same way (handles tall/portrait images).
	if rows > boxRows {
		rows = boxRows
		cols = max((naturalCols*boxRows+naturalRows/2)/naturalRows, 1)
	}
	return cols, rows
}

func messageContainsImages(msg *Message) bool {
	if msg == nil {
		return false
	}
	if len(msg.Images) > 0 {
		return true
	}
	for _, result := range msg.ToolResults {
		for _, attachment := range result.Attachments {
			if strings.HasPrefix(attachment.MimeType, "image/") && len(attachment.Content) > 0 {
				return true
			}
		}
	}
	for _, block := range msg.OrderedBlocks {
		if block.ToolResult == nil {
			continue
		}
		for _, attachment := range block.ToolResult.Attachments {
			if strings.HasPrefix(attachment.MimeType, "image/") && len(attachment.Content) > 0 {
				return true
			}
		}
	}
	return false
}

func (a *App) nativeImagesEnabled() bool {
	return a.appOptions.ImageManager != nil &&
		a.appOptions.ImageManager.Capability() == termimage.Kitty
}

func imageCacheKey(data []byte, filename, mimeType string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.ToLower(filename)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.ToLower(mimeType)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (a *App) imageMetadata(data []byte, filename, mimeType string) (components.ImageMetadata, error) {
	key := imageCacheKey(data, filename, mimeType)
	a.imageCacheMu.Lock()
	if entry, ok := a.imageCache[key]; ok && entry.metadataOK {
		a.imageCacheClock++
		entry.lastUsed = a.imageCacheClock
		a.imageCache[key] = entry
		a.imageCacheMu.Unlock()
		return entry.metadata, nil
	}
	a.imageCacheMu.Unlock()

	metadata, err := components.InspectImage(data, filename, mimeType)
	if err != nil {
		return components.ImageMetadata{}, err
	}

	a.imageCacheMu.Lock()
	if a.imageCache == nil {
		a.imageCache = make(map[string]imageCacheEntry)
	}
	a.imageCacheClock++
	entry := a.imageCache[key]
	entry.metadata = metadata
	entry.metadataOK = true
	entry.lastUsed = a.imageCacheClock
	a.imageCache[key] = entry
	a.evictImageCacheLocked("")
	a.imageCacheMu.Unlock()
	return metadata, nil
}

func (a *App) preparedImage(data []byte, filename, mimeType string) (components.PreparedImage, error) {
	key := imageCacheKey(data, filename, mimeType)
	a.imageCacheMu.Lock()
	if entry, ok := a.imageCache[key]; ok && entry.preparedOK {
		a.imageCacheClock++
		entry.lastUsed = a.imageCacheClock
		a.imageCache[key] = entry
		a.imageCacheMu.Unlock()
		return entry.prepared, nil
	}
	a.imageCacheMu.Unlock()

	prepared, err := components.PrepareImage(data, filename, mimeType)
	if err != nil {
		return components.PreparedImage{}, err
	}

	a.imageCacheMu.Lock()
	if a.imageCache == nil {
		a.imageCache = make(map[string]imageCacheEntry)
	}
	a.imageCacheClock++
	entry := a.imageCache[key]
	a.imageCacheBytes -= entry.bytes
	entry.prepared = prepared
	entry.preparedOK = true
	entry.bytes = len(prepared.PNG)
	entry.lastUsed = a.imageCacheClock
	a.imageCache[key] = entry
	a.imageCacheBytes += entry.bytes
	if entry.bytes > maxImageCacheBytes {
		delete(a.imageCache, key)
		a.imageCacheBytes -= entry.bytes
	} else {
		a.evictImageCacheLocked(key)
	}
	a.imageCacheMu.Unlock()
	return prepared, nil
}

func (a *App) evictImageCacheLocked(protected string) {
	for a.imageCacheBytes > maxImageCacheBytes || len(a.imageCache) > maxImageCacheEntries {
		oldestKey := ""
		var oldest uint64
		for key, entry := range a.imageCache {
			if key == protected {
				continue
			}
			if oldestKey == "" || entry.lastUsed < oldest {
				oldestKey, oldest = key, entry.lastUsed
			}
		}
		if oldestKey == "" {
			return
		}
		a.imageCacheBytes -= a.imageCache[oldestKey].bytes
		delete(a.imageCache, oldestKey)
	}
}

func (a *App) renderNativeImage(data []byte, filename, mimeType, occurrence string, maxColumns int) (renderedImageBlock, error) {
	if !a.nativeImagesEnabled() {
		return renderedImageBlock{}, fmt.Errorf("native images unavailable")
	}
	if len(data) == 0 || len(data) > maxNativeImageBytes {
		return renderedImageBlock{}, fmt.Errorf("native image payload size %d is unsupported", len(data))
	}
	prepared, err := a.preparedImage(data, filename, mimeType)
	if err != nil {
		return renderedImageBlock{}, err
	}
	pixelWidth, pixelHeight := prepared.Width, prepared.Height
	if pixelWidth <= 0 || pixelHeight <= 0 ||
		int64(pixelWidth)*int64(pixelHeight) > maxNativeImagePixels {
		return renderedImageBlock{}, fmt.Errorf("native image dimensions %dx%d are unsupported", pixelWidth, pixelHeight)
	}

	columns, rows := a.nativeImageCells(pixelWidth, pixelHeight, maxColumns)

	sum := sha256.Sum256(data)
	sourceKey := fmt.Sprintf("%x:%s", sum[:12], strings.ToLower(mimeType))
	placement, err := a.appOptions.ImageManager.Register(termimage.Source{
		Key: sourceKey, PNG: prepared.PNG, Width: pixelWidth, Height: pixelHeight,
	}, occurrence, columns, rows)
	if err != nil {
		return renderedImageBlock{}, err
	}
	if sourceErr := a.appOptions.ImageManager.SourceError(sourceKey); sourceErr != "" {
		return renderedImageBlock{}, fmt.Errorf("%s", sourceErr)
	}

	return renderedImageBlock{
		Lines: termimage.PlaceholderLines(placement),
		Images: []nativeImageAnchor{{
			OccurrenceKey: occurrence,
			SourceKey:     sourceKey,
			FileName:      filename,
			MimeType:      mimeType,
			Columns:       columns,
			Rows:          rows,
			PixelWidth:    pixelWidth,
			PixelHeight:   pixelHeight,
			Placement:     placement,
		}},
	}, nil
}

func (a *App) renderImageUnavailable(data []byte, filename, mimeType string, renderErr error) renderedImageBlock {
	name := filepath.Base(filename)
	if name == "." || name == "" {
		name = "Image"
	}
	format := strings.TrimPrefix(strings.ToLower(mimeType), "image/")
	width, height := 0, 0
	if metadata, err := a.imageMetadata(data, filename, mimeType); err == nil {
		width, height = metadata.Width, metadata.Height
		if metadata.Format != "" {
			format = metadata.Format
		}
	} else if renderErr == nil {
		renderErr = err
	}
	if format == "" {
		format = "image"
	}
	meta := strings.ToUpper(format)
	if width > 0 && height > 0 {
		meta += fmt.Sprintf(" · %d×%d", width, height)
	}
	meta += " · " + formatImageBytes(len(data))
	lines := []string{fmt.Sprintf("%s · %s", name, meta)}
	if a.appOptions.ImageManager == nil || a.appOptions.ImageManager.Capability() != termimage.Kitty {
		lines = append(lines, "Preview unavailable — use a terminal with Kitty graphics support")
	} else if renderErr != nil {
		lines = append(lines, "Preview unavailable — "+renderErr.Error())
	} else {
		lines = append(lines, "Preview unavailable")
	}
	return renderedImageBlock{Lines: lines}
}

func formatImageBytes(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func (a *App) terminalImagesSuppressed() bool {
	return a.screen != ScreenChat ||
		a.workspaceMode ||
		(a.debugScreen != nil && a.debugScreen.IsVisible()) ||
		a.newChatModal != nil ||
		a.activeModal != nil ||
		a.modelSwitcher.IsVisible() ||
		a.agentSwitcher.IsVisible() ||
		a.promptSwitcher.IsVisible() ||
		a.profileSwitcher.IsVisible() ||
		a.skillPicker.IsVisible() ||
		(a.commandPalette != nil && a.commandPalette.IsVisible()) ||
		a.activeCommand != nil ||
		a.taskActivityModal != nil ||
		a.approvalModal != nil ||
		a.questionModal != nil
}

func (a *App) publishTerminalImageFrame() {
	manager := a.appOptions.ImageManager
	if manager == nil {
		return
	}
	if a.terminalImagesSuppressed() || a.msgViewport == nil || manager.Capability() != termimage.Kitty {
		manager.Publish(termimage.Frame{})
		return
	}

	viewport := a.msgViewport
	if viewport.preWrappedHash != viewport.lastContentHash ||
		len(viewport.preWrappedMapping) != len(viewport.lines)+1 {
		manager.Publish(termimage.Frame{})
		return
	}

	placements := make([]termimage.Placement, 0, len(viewport.imageAnchors))
	viewportHeight := viewport.Height - viewport.Style.GetVerticalFrameSize()
	for _, anchor := range viewport.imageAnchors {
		if anchor.Placement.ImageID == 0 || anchor.Line < 0 || anchor.Line >= len(viewport.preWrappedMapping) {
			continue
		}
		y := viewport.preWrappedMapping[anchor.Line]
		if y+anchor.Rows <= viewport.YOffset || y >= viewport.YOffset+viewportHeight {
			continue
		}
		placements = append(placements, anchor.Placement)
	}
	manager.Publish(termimage.Frame{Placements: placements})
}
