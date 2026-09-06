package components

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func encodedJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var data bytes.Buffer
	if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestInspectImageReadsJPEGMetadataWithoutPreparingPixels(t *testing.T) {
	metadata, err := InspectImage(encodedJPEG(t, 640, 480), "screen.jpg", "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Width != 640 || metadata.Height != 480 || metadata.Format != "jpeg" {
		t.Fatalf("unexpected JPEG metadata: %+v", metadata)
	}
}

func TestInspectImageReadsSVGMetadataWithoutRasterizing(t *testing.T) {
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 24"><rect width="32" height="24" fill="red"/></svg>`)
	metadata, err := InspectImage(data, "sample.svg", "image/svg+xml")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Width != 32 || metadata.Height != 24 || metadata.Format != "svg" {
		t.Fatalf("unexpected SVG metadata: %+v", metadata)
	}
}

func TestPrepareImageReusesValidatedPNGBytes(t *testing.T) {
	data := encodedPNG(t, 40, 30)
	prepared, err := PrepareImage(data, "sample.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Width != 40 || prepared.Height != 30 || prepared.Format != "png" {
		t.Fatalf("unexpected metadata: %+v", prepared)
	}
	if !bytes.Equal(prepared.PNG, data) {
		t.Fatal("PNG source was unnecessarily re-encoded")
	}
}

func TestPrepareImageRasterizesSVGOnce(t *testing.T) {
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 24"><rect width="32" height="24" fill="red"/></svg>`)
	prepared, err := PrepareImage(data, "sample.svg", "image/svg+xml")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Width != 32 || prepared.Height != 24 || prepared.Format != "svg" {
		t.Fatalf("unexpected SVG metadata: %+v", prepared)
	}
	if !bytes.HasPrefix(prepared.PNG, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("SVG was not converted to PNG")
	}
}

func TestDecodeImageFromBytesRejectsOversizedSVGViewBox(t *testing.T) {
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100000 100000"></svg>`)
	if _, err := DecodeImageFromBytes(data, "oversized.svg", "image/svg+xml"); err == nil {
		t.Fatal("oversized SVG was accepted")
	}
}

func TestPrepareImageRejectsOversizedRasterConfig(t *testing.T) {
	// A valid PNG signature/IHDR with dimensions beyond the configured pixel
	// bound is enough for DecodeConfig; no huge allocation is performed.
	data := encodedPNG(t, 1, 1)
	data[16], data[17], data[18], data[19] = 0, 0, 0x27, 0x10
	data[20], data[21], data[22], data[23] = 0, 0, 0x27, 0x10
	if _, err := PrepareImage(data, "huge.png", "image/png"); err == nil {
		t.Fatal("oversized raster dimensions were accepted")
	}
}
