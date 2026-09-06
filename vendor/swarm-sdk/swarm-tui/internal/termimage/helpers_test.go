package termimage

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"sync"
	"testing"
)

var (
	syntheticOnce sync.Once
	syntheticPNG  []byte
)

// syntheticSource builds a deterministic, valid PNG payload big enough to be
// chunked by the direct transport, so chunk framing is exercised too.
func syntheticSource(key string) Source {
	syntheticOnce.Do(func() {
		img := image.NewRGBA(image.Rect(0, 0, 48, 48))
		for y := 0; y < 48; y++ {
			for x := 0; x < 48; x++ {
				img.Set(x, y, color.RGBA{uint8(x * 5), uint8(y * 5), 0x80, 0xff})
			}
		}
		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		syntheticPNG = buf.Bytes()
	})
	// Prefix the key so each source has distinct content, matching how the
	// caller derives Key from a content hash.
	payload := append([]byte(nil), syntheticPNG...)
	payload = append(payload, key...)
	return Source{Key: key, PNG: payload, Width: 48, Height: 48}
}

// trackedTempFiles snapshots every temp path the manager currently owns so a
// test can prove they are all gone after eviction or Release.
func trackedTempFiles(m *Manager) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var paths []string
	for _, p := range m.transferFiles {
		paths = append(paths, p)
	}
	for _, p := range m.probeFiles {
		paths = append(paths, p)
	}
	return paths
}

func assertPathsRemoved(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("temp file leaked: %s", path)
		}
	}
}
