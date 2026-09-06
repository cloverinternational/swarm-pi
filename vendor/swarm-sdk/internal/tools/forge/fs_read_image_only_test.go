package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFSReadIsImageOnly pins the post-removal contract: Read serves images and
// refuses everything else with actionable shell guidance.
//
// Text reading (and the mode=search / mode=files / head / tail / start_line
// surface) was removed in favour of the shell, which is faster, composes
// better, and is output-capped so it cannot flood the context window. The
// content-sniffing classifier this file used to test went with it; images are
// identified by extension, exactly as the old image branch did.
func TestFSReadIsImageOnly(t *testing.T) {
	dir := t.TempDir()
	sqlPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(sqlPath, []byte("CREATE TABLE t (id INT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := NewFSRead(dir).Execute(context.Background(), map[string]any{"file_path": sqlPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(res.Output, "ERROR: Read handles image files only") {
		t.Errorf("non-image should be refused, got: %s", res.Output)
	}
	// A refusal without a next step is a dead end for the model.
	for _, want := range []string{"sed -n", "rg "} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("refusal must suggest %q, got: %s", want, res.Output)
		}
	}
	// It must not advertise parameters that no longer exist.
	for _, bad := range []string{"mode=", "start_line", "max_size"} {
		if strings.Contains(res.Output, bad) {
			t.Errorf("refusal references removed parameter %q: %s", bad, res.Output)
		}
	}
}

// TestFSReadAcceptsImage proves the one capability the shell cannot provide
// still works: returning image content the model can actually see.
func TestFSReadAcceptsImage(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "pixel.png")
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
		0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(pngPath, png, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := NewFSRead(dir).Execute(context.Background(), map[string]any{"file_path": pngPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.HasPrefix(res.Output, "ERROR:") {
		t.Fatalf("image read failed: %s", res.Output)
	}
	if len(res.Content) == 0 {
		t.Error("image read must return a content block, not just text")
	}
}
