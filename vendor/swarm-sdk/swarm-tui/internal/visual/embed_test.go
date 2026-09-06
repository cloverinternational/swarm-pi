package visual

import "testing"

func TestEmbeddedAssetsPresent(t *testing.T) {
	fs := TemplatesFS()
	for _, name := range []string{
		"templates/frame.html",
		"templates/app.css",
		"templates/app.bundle.css",
		"templates/app.bundle.js",
		"templates/transport.js",
	} {
		data, err := fs.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if len(data) < 20 {
			t.Errorf("%s too small (%d bytes)", name, len(data))
		}
	}
}
