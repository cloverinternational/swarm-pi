// Package vision provides unified image handling utilities for all providers.
// This package standardizes image encoding, validation, and metadata management
// across different LLM providers (Anthropic, OpenAI, etc.).
package vision

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Image size and format constants
const (
	// MaxImageSizeBytes is the maximum allowed image size (5MB - Anthropic's limit)
	MaxImageSizeBytes int64 = 5242880

	// Supported image MIME types
	MediaTypeJPEG = "image/jpeg"
	MediaTypePNG  = "image/png"
	MediaTypeGIF  = "image/gif"
	MediaTypeWebP = "image/webp"
)

// SupportedMediaTypes maps all supported image formats.
var SupportedMediaTypes = map[string]bool{
	MediaTypeJPEG: true,
	MediaTypePNG:  true,
	MediaTypeGIF:  true,
	MediaTypeWebP: true,
}

// ImageData represents image data with metadata.
// This is the canonical format used across all providers.
type ImageData struct {
	// Type indicates how the image is provided: "base64" or "url"
	Type string

	// MediaType is the MIME type (e.g., "image/jpeg", "image/png")
	// Required for Type="base64", optional for Type="url"
	MediaType string

	// Data contains base64-encoded image data (for Type="base64")
	Data string

	// URL contains the image URL (for Type="url")
	URL string
}

// Validate checks if the ImageData is valid.
func (img *ImageData) Validate() error {
	if img.Type != "base64" && img.Type != "url" {
		return sdkerr.Permanent(
			"vision.invalid_type",
			fmt.Sprintf("invalid image type: %s (must be 'base64' or 'url')", img.Type),
		)
	}

	if img.Type == "base64" {
		if err := ValidateMediaType(img.MediaType); err != nil {
			return err
		}
		if img.Data == "" {
			return sdkerr.Permanent(
				"vision.empty_data",
				"base64 image data is required",
			)
		}
		// Validate base64 format
		if _, err := base64.StdEncoding.DecodeString(img.Data); err != nil {
			return sdkerr.Permanent(
				"vision.invalid_base64",
				fmt.Sprintf("invalid base64 data: %v", err),
			)
		}
	} else if img.Type == "url" {
		if err := ValidateImageURL(img.URL); err != nil {
			return err
		}
	}

	return nil
}

// EncodeImageFile reads an image file and returns base64-encoded data with media type.
func EncodeImageFile(filePath string) (string, string, error) {
	if filePath == "" {
		return "", "", sdkerr.Permanent(
			"vision.empty_path",
			"file path is required",
		)
	}

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", sdkerr.Permanent(
			"vision.file_open_failed",
			fmt.Sprintf("failed to open image file: %v", err),
		)
	}
	defer file.Close()

	// Check file size
	fileInfo, err := file.Stat()
	if err != nil {
		return "", "", sdkerr.Permanent(
			"vision.file_stat_failed",
			fmt.Sprintf("failed to stat image file: %v", err),
		)
	}

	if fileInfo.Size() > MaxImageSizeBytes {
		return "", "", sdkerr.Permanent(
			"vision.file_too_large",
			fmt.Sprintf("image file size exceeds limit: %d bytes (max: %d bytes)",
				fileInfo.Size(), MaxImageSizeBytes),
		)
	}

	// Read file content
	imageData, err := io.ReadAll(file)
	if err != nil {
		return "", "", sdkerr.Permanent(
			"vision.file_read_failed",
			fmt.Sprintf("failed to read image file: %v", err),
		)
	}

	// Detect media type from file extension
	mediaType := DetectMediaType(filePath)
	if err := ValidateMediaType(mediaType); err != nil {
		return "", "", err
	}

	// Encode to base64
	encoded := base64.StdEncoding.EncodeToString(imageData)
	return encoded, mediaType, nil
}

// EncodeImageBytes encodes raw image bytes to base64.
func EncodeImageBytes(data []byte, mediaType string) (string, error) {
	if len(data) == 0 {
		return "", sdkerr.Permanent(
			"vision.empty_data",
			"image data is required",
		)
	}

	if int64(len(data)) > MaxImageSizeBytes {
		return "", sdkerr.Permanent(
			"vision.data_too_large",
			fmt.Sprintf("image data exceeds limit: %d bytes (max: %d bytes)",
				len(data), MaxImageSizeBytes),
		)
	}

	if err := ValidateMediaType(mediaType); err != nil {
		return "", err
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return encoded, nil
}

// DecodeBase64Image decodes base64 image data to raw bytes.
func DecodeBase64Image(data string) ([]byte, error) {
	if data == "" {
		return nil, sdkerr.Permanent(
			"vision.empty_data",
			"base64 data is required",
		)
	}

	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, sdkerr.Permanent(
			"vision.decode_failed",
			fmt.Sprintf("failed to decode base64 data: %v", err),
		)
	}

	if int64(len(decoded)) > MaxImageSizeBytes {
		return nil, sdkerr.Permanent(
			"vision.decoded_too_large",
			fmt.Sprintf("decoded image exceeds limit: %d bytes (max: %d bytes)",
				len(decoded), MaxImageSizeBytes),
		)
	}

	return decoded, nil
}

// NewImageFromFile creates an ImageData from a file path.
func NewImageFromFile(filePath string) (*ImageData, error) {
	data, mediaType, err := EncodeImageFile(filePath)
	if err != nil {
		return nil, err
	}

	return &ImageData{
		Type:      "base64",
		MediaType: mediaType,
		Data:      data,
	}, nil
}

// NewImageFromBytes creates an ImageData from raw bytes.
func NewImageFromBytes(data []byte, mediaType string) (*ImageData, error) {
	encoded, err := EncodeImageBytes(data, mediaType)
	if err != nil {
		return nil, err
	}

	return &ImageData{
		Type:      "base64",
		MediaType: mediaType,
		Data:      encoded,
	}, nil
}

// NewImageFromURL creates an ImageData from a URL.
func NewImageFromURL(url string) (*ImageData, error) {
	if err := ValidateImageURL(url); err != nil {
		return nil, err
	}

	return &ImageData{
		Type: "url",
		URL:  url,
	}, nil
}

// DetectMediaTypeFromBytes sniffs the real image format from raw bytes using
// their magic-number signature and returns one of the SupportedMediaTypes
// values plus true, or ("", false) when the bytes are not a recognized image
// format (e.g. truncated data, or a non-image payload).
//
// This exists because callers (MCP servers, browser automation bridges, etc.)
// frequently mislabel the mimeType/media_type of image payloads — most
// commonly declaring "image/png" for data that is actually JPEG. Providers
// like Anthropic independently sniff the bytes server-side and reject the
// request outright when the declared media_type disagrees with the actual
// content, so anything that forwards externally-declared image metadata to a
// provider MUST verify it against the bytes first rather than trusting it.
func DetectMediaTypeFromBytes(data []byte) (string, bool) {
	if len(data) == 0 {
		return "", false
	}
	// http.DetectContentType only needs the first 512 bytes and never errors.
	n := len(data)
	if n > 512 {
		n = 512
	}
	detected := http.DetectContentType(data[:n])
	// DetectContentType returns values like "image/png", "image/jpeg",
	// "image/gif", "image/webp" (with no parameters for these formats), or
	// generic fallbacks such as "application/octet-stream" for anything it
	// doesn't recognize.
	if SupportedMediaTypes[detected] {
		return detected, true
	}
	return "", false
}

// DetectMediaTypeFromBase64 decodes just enough of a base64-encoded image
// payload to sniff its real media type. It returns ("", false) when the data
// can't be decoded or isn't a recognized image format. Use this to correct a
// declared/default mimeType before it reaches a provider that validates the
// media_type against the actual bytes.
func DetectMediaTypeFromBase64(base64Data string) (string, bool) {
	if base64Data == "" {
		return "", false
	}
	// 512 raw bytes needs ceil(512/3)*4 base64 chars; be generous and decode
	// up to ~700 chars of input (≈512 decoded bytes) so DetectContentType has
	// its full sniffing window.
	n := len(base64Data)
	if n > 700 {
		n = 700
	}
	prefix := base64Data[:n]
	// Trim to a multiple of 4 so StdEncoding can decode it without needing
	// the (possibly truncated) tail of the real payload.
	prefix = prefix[:len(prefix)-len(prefix)%4]
	if prefix == "" {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(prefix)
	if err != nil {
		// Some sources emit unpadded/raw base64; fall back before giving up.
		decoded, err = base64.RawStdEncoding.DecodeString(prefix)
		if err != nil {
			return "", false
		}
	}
	return DetectMediaTypeFromBytes(decoded)
}

// ReconcileMediaType returns the media type that should actually be sent to
// a provider for a base64-encoded image payload: the sniffed type when the
// bytes are a recognized image format, falling back to declared (which may
// itself be empty) when sniffing is inconclusive. This is the single place
// callers should route through instead of trusting an externally-declared
// mimeType/media_type verbatim.
func ReconcileMediaType(base64Data string, declared string) string {
	if detected, ok := DetectMediaTypeFromBase64(base64Data); ok {
		return detected
	}
	return declared
}
