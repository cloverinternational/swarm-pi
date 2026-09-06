package termimage

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

type rejectGraphicsWriter struct{ bytes.Buffer }

func (w *rejectGraphicsWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("\x1b_G")) {
		return 0, errors.New("graphics write failed")
	}
	return w.Buffer.Write(p)
}

type uploadStatsWriter struct {
	writes   int
	maxWrite int
	total    int
}

func (w *uploadStatsWriter) Write(p []byte) (int, error) {
	w.writes++
	w.total += len(p)
	if len(p) > w.maxWrite {
		w.maxWrite = len(p)
	}
	return len(p), nil
}

func testSource(t *testing.T, key string, width, height int) Source {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return Source{Key: key, PNG: encoded.Bytes(), Width: width, Height: height}
}

func testPlacement(t *testing.T, manager *Manager) Placement {
	t.Helper()
	placement, err := manager.Register(testSource(t, "test", 20, 40), "occurrence", 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	return placement
}

func TestParseKittyResponse(t *testing.T) {
	for _, response := range []string{
		"\x1b_Gi=31;OK\x1b\\",
		"noise\x1b_Gi=2;ENOENT\x1b\\\x1b_Gi=31;OK\x1b\\",
		"i=31;OK",
	} {
		if !ParseKittyResponse(response) {
			t.Errorf("did not accept %q", response)
		}
	}
	parsed, ok := ParseResponse("\x1b_Gi=99;ENOMEM: no space\x1b\\")
	if !ok || parsed.ID != 99 || parsed.OK || parsed.Message != "ENOMEM: no space" {
		t.Fatalf("unexpected parsed response: %+v, %v", parsed, ok)
	}
	for _, response := range []string{
		"\x1b_Gi=31;ENOENT\x1b\\", "\x1b_Gi=30;OK\x1b\\", "\x1b_Gi=31;OK", "OK",
	} {
		if ParseKittyResponse([]byte(response)) {
			t.Errorf("accepted %q", response)
		}
	}
}

func TestPlaceholderLinesEncodeStableOneCellGraphemes(t *testing.T) {
	p := Placement{ImageID: 0x0240202a, PlacementID: 0x010203, Columns: 3, Rows: 2}
	lines := PlaceholderLines(p)
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 3 {
			t.Fatalf("line %d width = %d, want 3: %q", i, got, line)
		}
		if count := strings.Count(line, string('\U0010EEEE')); count != 3 {
			t.Fatalf("line %d placeholders = %d", i, count)
		}
	}
	if !strings.Contains(lines[0], "\x1b[38;2;64;32;42m") ||
		!strings.Contains(lines[0], "\x1b[58;2;1;2;3m") {
		t.Fatalf("IDs not encoded in colors: %q", lines[0])
	}
}

func TestWriterReconcilesAfterSynchronizedStartBeforePlaceholder(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	placement := testPlacement(t, manager)
	manager.Publish(Frame{Placements: []Placement{placement}})
	var output bytes.Buffer
	writer := NewWriter(&output, manager)
	placeholder := PlaceholderLines(placement)[0]
	input := []byte("\x1b[?2026h" + placeholder + "\x1b[?2026l")
	if n, err := writer.Write(input); err != nil || n != len(input) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	s := output.String()
	startAt := strings.Index(s, "\x1b[?2026h")
	graphicsAt := strings.Index(s, "\x1b_G")
	placeholderAt := strings.Index(s, string('\U0010EEEE'))
	endAt := strings.Index(s, "\x1b[?2026l")
	if startAt != 0 || graphicsAt <= startAt || placeholderAt <= graphicsAt || endAt <= placeholderAt {
		t.Fatalf("wrong output ordering: start=%d graphics=%d placeholder=%d end=%d", startAt, graphicsAt, placeholderAt, endAt)
	}
}

func TestWriterReconcilesWhenStartMarkerIsSplit(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	placement := testPlacement(t, manager)
	manager.Publish(Frame{Placements: []Placement{placement}})
	var output bytes.Buffer
	writer := NewWriter(&output, manager)
	for _, chunk := range []string{"\x1b[?20", "26", "htext\x1b[?2026l"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	s := output.String()
	if graphicsAt, textAt := strings.Index(s, "\x1b_G"), strings.Index(s, "text"); graphicsAt < 0 || textAt <= graphicsAt {
		t.Fatalf("split marker ordering is wrong: %q", s)
	}
}

func TestWriterReportsCommittedInputOnReconcileFailure(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	placement := testPlacement(t, manager)
	manager.Publish(Frame{Placements: []Placement{placement}})
	output := &rejectGraphicsWriter{}
	writer := NewWriter(output, manager)
	input := []byte("\x1b[?2026hframe text\x1b[?2026l")
	n, err := writer.Write(input)
	if err == nil {
		t.Fatal("expected graphics write failure")
	}
	if n != len(synchronizedOutputStart) {
		t.Fatalf("committed input = %d, want %d", n, len(synchronizedOutputStart))
	}
}

func TestReconcileReusesUploadAndVirtualPlacement(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	placement := testPlacement(t, manager)
	frame := Frame{Placements: []Placement{placement}}
	manager.Publish(frame)
	var first bytes.Buffer
	if err := manager.Reconcile(&first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "a=t") || !strings.Contains(first.String(), "U=1") {
		t.Fatalf("missing upload or virtual placement: %q", first.String())
	}
	manager.Publish(Frame{})
	if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	manager.Publish(frame)
	var second bytes.Buffer
	if err := manager.Reconcile(&second); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second.String(), "a=t") || strings.Contains(second.String(), "a=p") {
		t.Fatalf("resource retransmitted: %q", second.String())
	}
}

func TestInvalidateRetransmitsUnchangedFrame(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	placement := testPlacement(t, manager)
	manager.Publish(Frame{Placements: []Placement{placement}})
	if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	manager.Invalidate()
	var output bytes.Buffer
	if err := manager.Reconcile(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "a=t") || !strings.Contains(output.String(), "U=1") {
		t.Fatalf("unchanged frame was not restored after invalidation: %q", output.String())
	}
}

func TestDirectUploadChunksPayloadAtProtocolLimit(t *testing.T) {
	source := Source{Key: "large", PNG: bytes.Repeat([]byte{0xaa}, 9000), Width: 1, Height: 1}
	var output bytes.Buffer
	if _, err := writeUpload(&output, source, 77, Direct, Raw); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(output.String(), "\x1b\\")
	if len(parts) < 4 {
		t.Fatalf("expected multiple chunks, got %d", len(parts)-1)
	}
	for _, part := range parts[:len(parts)-1] {
		_, payload, ok := strings.Cut(part, ";")
		if !ok || len(payload) > 4096 {
			t.Fatalf("invalid chunk payload size %d", len(payload))
		}
	}
}

func TestDirectUploadStreamsImageChunks(t *testing.T) {
	source := Source{
		Key:   "image-heavy",
		PNG:   bytes.Repeat([]byte{0xaa}, 1<<20),
		Width: 2048, Height: 2048,
	}
	var output uploadStatsWriter
	if _, err := writeUpload(&output, source, 77, Direct, Raw); err != nil {
		t.Fatal(err)
	}
	if output.writes < 100 {
		t.Fatalf("writes = %d, want streamed protocol chunks", output.writes)
	}
	if output.maxWrite > kitty.MaxChunkSize+128 {
		t.Fatalf("max write = %d, image was buffered instead of streamed", output.maxWrite)
	}
	if output.total == 0 {
		t.Fatal("stream produced no terminal bytes")
	}
}

func TestTmuxWrapsEveryGraphicsChunk(t *testing.T) {
	source := Source{Key: "tmux", PNG: bytes.Repeat([]byte{1}, 5000), Width: 1, Height: 1}
	var output bytes.Buffer
	if _, err := writeUpload(&output, source, 88, Direct, TmuxPassthrough); err != nil {
		t.Fatal(err)
	}
	s := output.String()
	if strings.Count(s, "\x1bPtmux;") < 2 || !strings.Contains(s, "\x1b\x1b_G") {
		t.Fatalf("graphics chunks not independently wrapped: %q", s)
	}
}

func TestProbeTempFilesAreClosedAndCleaned(t *testing.T) {
	manager := NewManager()
	commands, err := manager.ProbeCommands(true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commands, "i=31") || !strings.Contains(commands, "i=34") {
		t.Fatalf("missing probe matrix: %q", commands)
	}
	var paths []string
	for _, path := range manager.probeFiles {
		paths = append(paths, path)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("probe file unavailable before timeout: %v", err)
		}
	}
	manager.FinishProbe()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("probe file was not removed: %s", path)
		}
	}
}

func TestUploadErrorFallsBackThenBecomesSourceError(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	placement := testPlacement(t, manager)
	manager.Publish(Frame{Placements: []Placement{placement}})
	manager.transport = TemporaryFile
	if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	tempPath := manager.transferFiles[placement.ImageID]
	if tempPath == "" {
		t.Fatal("temporary upload path was not tracked")
	}
	if !manager.ObserveResponse(Response{ID: placement.ImageID, Message: "EIO"}) || manager.Transport() != Direct {
		t.Fatal("temporary transfer did not fall back to direct")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("failed temporary upload was not removed: %s", tempPath)
	}
	if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	manager.ObserveResponse(Response{ID: placement.ImageID, Message: "EINVAL"})
	if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	manager.ObserveResponse(Response{ID: placement.ImageID, Message: "EINVAL"})
	if got := manager.SourceError(placement.Source.Key); !strings.Contains(got, "EINVAL") {
		t.Fatalf("source error = %q", got)
	}
}

func TestReleaseDeletesOnlyOwnedResources(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	placement := testPlacement(t, manager)
	manager.Publish(Frame{Placements: []Placement{placement}})
	if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var released bytes.Buffer
	if err := manager.Release(&released); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(released.String(), "a=d") || !strings.Contains(released.String(), "d=I") ||
		strings.Contains(released.String(), "d=A") {
		t.Fatalf("unexpected release sequence: %q", released.String())
	}
}

func TestManagerBoundsRetainedSources(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	var first Placement
	for i := 0; i < maxRetainedImages+1; i++ {
		source := testSource(t, "image-"+uintString(uint32(i+1)), 1, 1)
		placement, err := manager.Register(source, "occurrence", 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = placement
		}
		manager.Publish(Frame{Placements: []Placement{placement}})
		if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(manager.sources) > maxRetainedImages {
		t.Fatalf("retained sources = %d, max %d", len(manager.sources), maxRetainedImages)
	}
	manager.Publish(Frame{Placements: []Placement{first}})
	var restored bytes.Buffer
	if err := manager.Reconcile(&restored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restored.String(), "i="+uintString(first.ImageID)) ||
		!strings.Contains(restored.String(), "a=t") {
		t.Fatalf("evicted cached image was not reconstructable: %q", restored.String())
	}
}
