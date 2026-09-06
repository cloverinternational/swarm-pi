package rendering

import "testing"

func TestContentTypeHTMLIsDistinct(t *testing.T) {
	if ContentTypeHTML == ContentTypePlainText {
		t.Fatal("ContentTypeHTML must be distinct")
	}
}

func TestRenderableHTML_RoundTrip(t *testing.T) {
	r := RenderableHTML{Content: "<p>hello</p>"}
	got := r.Render(0, 0)
	if len(got) != 1 || got[0] != "<p>hello</p>" {
		t.Errorf("render = %v", got)
	}
	if r.ContentType() != ContentTypeHTML {
		t.Errorf("contentType = %v", r.ContentType())
	}
	if r.IsCollapsible() {
		t.Error("HTML should not be collapsible")
	}
}
