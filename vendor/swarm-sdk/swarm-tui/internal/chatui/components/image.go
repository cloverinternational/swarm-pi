package components

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/disintegration/imageorient"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	_ "golang.org/x/image/webp"
)

const maxDecodedImagePixels = 25_000_000

// PreparedImage is a validated, durable Kitty graphics source. PNG is kept in
// encoded form so the terminal path never has to re-encode it during a frame.
type PreparedImage struct {
	PNG           []byte
	Width, Height int
	Format        string
}

// ImageMetadata contains the information needed to describe an image without
// decoding its pixels. It is intentionally separate from PreparedImage:
// unsupported terminals only need this small description and must not pay the
// cost of rasterizing and PNG-encoding the source.
type ImageMetadata struct {
	Width, Height int
	Format        string
}

// InspectImage validates image dimensions and returns metadata without
// rasterizing or re-encoding the image. This is the fast path for fallback
// cards when native terminal images are unavailable.
func InspectImage(data []byte, filename, mediaType string) (ImageMetadata, error) {
	if len(data) == 0 {
		return ImageMetadata{}, fmt.Errorf("%s", i18n.T("chatui.image.empty"))
	}
	if isSVG(filename, mediaType) {
		width, height, err := inspectSVG(bytes.NewReader(data))
		if err != nil {
			return ImageMetadata{}, err
		}
		return ImageMetadata{Width: width, Height: height, Format: "svg"}, nil
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ImageMetadata{}, fmt.Errorf(i18n.T("chatui.image.decode_config_failed"), err)
	}
	if err := validateDimensions(config.Width, config.Height); err != nil {
		return ImageMetadata{}, err
	}
	return ImageMetadata{
		Width: config.Width, Height: config.Height, Format: strings.ToLower(format),
	}, nil
}

// PrepareImage validates image bytes, applies orientation/rasterization when
// needed, and returns a PNG payload suitable for Kitty f=100 transmission.
// Existing PNG bytes are reused instead of decoded and re-encoded.
func PrepareImage(data []byte, filename, mediaType string) (PreparedImage, error) {
	if len(data) == 0 {
		return PreparedImage{}, fmt.Errorf("%s", i18n.T("chatui.image.empty"))
	}
	if isSVG(filename, mediaType) {
		img, err := decodeSVGImage(bytes.NewReader(data))
		if err != nil {
			return PreparedImage{}, err
		}
		return encodePrepared(img, "svg")
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return PreparedImage{}, fmt.Errorf(i18n.T("chatui.image.decode_config_failed"), err)
	}
	if err := validateDimensions(config.Width, config.Height); err != nil {
		return PreparedImage{}, err
	}
	if strings.EqualFold(format, "png") {
		return PreparedImage{
			PNG:    data,
			Width:  config.Width,
			Height: config.Height,
			Format: "png",
		}, nil
	}

	img, _, err := imageorient.Decode(bytes.NewReader(data))
	if err != nil {
		return PreparedImage{}, fmt.Errorf(i18n.T("chatui.image.decode_failed"), err)
	}
	return encodePrepared(img, strings.ToLower(format))
}

// DecodeImageFromBytes remains as the bounded decoder used by callers that
// need pixels rather than a Kitty-ready encoded source.
func DecodeImageFromBytes(data []byte, filename, mediaType string) (image.Image, error) {
	if isSVG(filename, mediaType) {
		return decodeSVGImage(bytes.NewReader(data))
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf(i18n.T("chatui.image.decode_config_failed"), err)
	}
	if err := validateDimensions(config.Width, config.Height); err != nil {
		return nil, err
	}
	img, _, err := imageorient.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf(i18n.T("chatui.image.decode_failed"), err)
	}
	return img, nil
}

func isSVG(filename, mediaType string) bool {
	return strings.Contains(strings.ToLower(mediaType), "svg") ||
		strings.HasSuffix(strings.ToLower(filename), ".svg")
}

func validateDimensions(width, height int) error {
	if width <= 0 || height <= 0 ||
		int64(width)*int64(height) > int64(maxDecodedImagePixels) {
		return fmt.Errorf("%s", i18n.T("chatui.image.dimensions_unsupported", width, height))
	}
	return nil
}

func encodePrepared(img image.Image, format string) (PreparedImage, error) {
	bounds := img.Bounds()
	if err := validateDimensions(bounds.Dx(), bounds.Dy()); err != nil {
		return PreparedImage{}, err
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return PreparedImage{}, fmt.Errorf(i18n.T("chatui.image.encode_png_failed"), err)
	}
	return PreparedImage{
		PNG:    encoded.Bytes(),
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
		Format: format,
	}, nil
}

func decodeSVGImage(r io.Reader) (image.Image, error) {
	icon, err := oksvg.ReadIconStream(r)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("chatui.image.parse_svg_failed"), err)
	}
	w, h, err := validateSVGDimensions(icon.ViewBox.W, icon.ViewBox.H)
	if err != nil {
		return nil, err
	}
	icon.SetTarget(0, 0, float64(w), float64(h))
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	icon.Draw(rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())), 1)
	return rgba, nil
}

func inspectSVG(r io.Reader) (int, int, error) {
	icon, err := oksvg.ReadIconStream(r)
	if err != nil {
		return 0, 0, fmt.Errorf("%s", i18n.T("chatui.image.parse_svg_failed", err))
	}
	return validateSVGDimensions(icon.ViewBox.W, icon.ViewBox.H)
}

func validateSVGDimensions(width, height float64) (int, int, error) {
	if width == 0 || height == 0 {
		width, height = 256, 256
	}
	if width < 1 || height < 1 || width > 8192 || height > 8192 ||
		width*height > maxDecodedImagePixels {
		return 0, 0, fmt.Errorf("%s", i18n.T("chatui.image.svg_dimensions_unsupported", width, height))
	}
	return int(width), int(height), nil
}
