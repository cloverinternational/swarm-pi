package vision

import (
	"fmt"
	"path/filepath"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// ValidateMediaType checks if the provided media type is supported.
func ValidateMediaType(mediaType string) error {
	if mediaType == "" {
		return sdkerr.Permanent(
			"vision.empty_media_type",
			"media type is required",
		)
	}

	if !SupportedMediaTypes[mediaType] {
		return sdkerr.Permanent(
			"vision.unsupported_format",
			fmt.Sprintf("unsupported image format: %s (supported: %s, %s, %s, %s)",
				mediaType,
				MediaTypeJPEG,
				MediaTypePNG,
				MediaTypeGIF,
				MediaTypeWebP,
			),
		)
	}

	return nil
}

// ValidateImageURL checks if a URL is valid and well-formed.
func ValidateImageURL(url string) error {
	if url == "" {
		return sdkerr.Permanent(
			"vision.empty_url",
			"image URL is required",
		)
	}

	// Basic URL validation
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return sdkerr.Permanent(
			"vision.invalid_url",
			fmt.Sprintf("image URL must start with http:// or https://: %s", url),
		)
	}

	return nil
}

// DetectMediaType attempts to detect the media type from a file path.
// Returns a default media type based on file extension.
func DetectMediaType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".jpg", ".jpeg":
		return MediaTypeJPEG
	case ".png":
		return MediaTypePNG
	case ".gif":
		return MediaTypeGIF
	case ".webp":
		return MediaTypeWebP
	default:
		// Default to JPEG if unknown
		return MediaTypeJPEG
	}
}

// IsSupportedFormat checks if a file extension is supported.
func IsSupportedFormat(filePath string) bool {
	mediaType := DetectMediaType(filePath)
	return SupportedMediaTypes[mediaType]
}
