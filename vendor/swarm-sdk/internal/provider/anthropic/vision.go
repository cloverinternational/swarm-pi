package anthropic

import (
	"encoding/base64"
	"fmt"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
	"io"
	"net/http"
	"os"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Vision-related constants
const (
	MaxImageSizeBytes int64 = 5242880 // 5MB max image size per Anthropic API limits

	// Supported image media types
	ImageMediaTypeJPEG string = "image/jpeg"
	ImageMediaTypePNG  string = "image/png"
	ImageMediaTypeGIF  string = "image/gif"
	ImageMediaTypeWebP string = "image/webp"
)

// SupportedImageFormats lists all supported image media types.
var SupportedImageFormats = map[string]bool{
	ImageMediaTypeJPEG: true,
	ImageMediaTypePNG:  true,
	ImageMediaTypeGIF:  true,
	ImageMediaTypeWebP: true,
}

// ImageData represents image data with metadata.
type ImageData struct {
	Type      string // "base64" or "url"
	MediaType string // e.g., "image/jpeg", "image/png"
	Data      string // Base64 encoded data (for base64 type)
	URL       string // Image URL (for url type)
}

// ValidateMediaType checks if the provided media type is supported.
func ValidateMediaType(mediaType string) error {
	if mediaType == "" {
		return sdkerr.Permanent(
			"anthropic.vision.empty_media_type",
			"media type is required",
		)
	}

	if !SupportedImageFormats[mediaType] {
		return sdkerr.Permanent(
			"anthropic.vision.unsupported_format",
			fmt.Sprintf("unsupported image format: %s (supported: %s, %s, %s, %s)",
				mediaType,
				ImageMediaTypeJPEG,
				ImageMediaTypePNG,
				ImageMediaTypeGIF,
				ImageMediaTypeWebP,
			),
		)
	}

	return nil
}

// EncodeImageToBase64 reads an image file and returns base64-encoded data.
func EncodeImageToBase64(filePath string) (string, error) {
	if filePath == "" {
		return "", sdkerr.Permanent(
			"anthropic.vision.empty_path",
			"file path is required",
		)
	}

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return "", sdkerr.Permanent(
			"anthropic.vision.file_open_failed",
			fmt.Sprintf("failed to open image file: %v", err),
		)
	}
	defer file.Close()

	// Check file size
	fileInfo, err := file.Stat()
	if err != nil {
		return "", sdkerr.Permanent(
			"anthropic.vision.file_stat_failed",
			fmt.Sprintf("failed to stat image file: %v", err),
		)
	}

	if fileInfo.Size() > MaxImageSizeBytes {
		return "", sdkerr.Permanent(
			"anthropic.vision.file_too_large",
			fmt.Sprintf("image file size exceeds limit: %d bytes (max: %d bytes)",
				fileInfo.Size(), MaxImageSizeBytes),
		)
	}

	// Read file content
	imageData, err := io.ReadAll(file)
	if err != nil {
		return "", sdkerr.Permanent(
			"anthropic.vision.file_read_failed",
			fmt.Sprintf("failed to read image file: %v", err),
		)
	}

	// Encode to base64
	encoded := base64.StdEncoding.EncodeToString(imageData)
	return encoded, nil
}

// ValidateImageURL checks if a URL is valid and accessible.
func ValidateImageURL(imageURL string) error {
	if imageURL == "" {
		return sdkerr.Permanent(
			"anthropic.vision.empty_url",
			"image URL is required",
		)
	}

	// Basic URL validation (check if it starts with http)
	if !strings.HasPrefix(imageURL, "http://") && !strings.HasPrefix(imageURL, "https://") {
		return sdkerr.Permanent(
			"anthropic.vision.invalid_url",
			fmt.Sprintf("image URL must start with http:// or https://: %s", imageURL),
		)
	}

	return nil
}

// CreateImageContentBlock creates a ContentBlock from image data.
func CreateImageContentBlock(imageType string, mediaType string, data string, url string) (*ContentBlock, error) {
	// Validate image type
	if imageType != "base64" && imageType != "url" {
		return nil, sdkerr.Permanent(
			"anthropic.vision.invalid_image_type",
			fmt.Sprintf("invalid image type: %s (must be 'base64' or 'url')", imageType),
		)
	}

	// Create content block
	contentBlock := &ContentBlock{
		Type: "image",
	}

	if imageType == "base64" {
		// Validate media type for base64 images
		if err := ValidateMediaType(mediaType); err != nil {
			return nil, err
		}

		if data == "" {
			return nil, sdkerr.Permanent(
				"anthropic.vision.empty_base64_data",
				"base64 image data is required",
			)
		}

		contentBlock.Source = &ContentSource{
			Type:      "base64",
			MediaType: &mediaType,
			Data:      data,
		}
	} else { // imageType == "url"
		if err := ValidateImageURL(url); err != nil {
			return nil, err
		}

		contentBlock.Source = &ContentSource{
			Type: "url",
			URL:  url,
			// MediaType is nil for URL images (not included in JSON)
		}
	}

	return contentBlock, nil
}

// FetchImageAndEncode fetches a remote image and returns base64-encoded data.
// This is useful for converting URL-based images to base64 if needed.
func FetchImageAndEncode(imageURL string) (string, string, error) {
	if err := ValidateImageURL(imageURL); err != nil {
		return "", "", err
	}

	// Fetch the image
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", "", sdkerr.Wrap(err, "anthropic.vision.fetch_failed")
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return "", "", sdkerr.Permanent(
			"anthropic.vision.fetch_status_error",
			fmt.Sprintf("failed to fetch image: HTTP %d", resp.StatusCode),
		)
	}

	// Read response body with size limit
	limitedReader := io.LimitReader(resp.Body, MaxImageSizeBytes+1)
	imageData, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", "", sdkerr.Wrap(err, "anthropic.vision.fetch_read_failed")
	}

	// Check size
	if int64(len(imageData)) > MaxImageSizeBytes {
		return "", "", sdkerr.Permanent(
			"anthropic.vision.fetched_image_too_large",
			fmt.Sprintf("fetched image exceeds size limit: %d bytes (max: %d bytes)",
				len(imageData), MaxImageSizeBytes),
		)
	}

	// Extract media type from Content-Type header
	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		// Default to JPEG if not specified
		mediaType = ImageMediaTypeJPEG
	}

	// Validate media type
	if err := ValidateMediaType(mediaType); err != nil {
		return "", "", err
	}

	// Encode to base64
	encoded := base64.StdEncoding.EncodeToString(imageData)
	return encoded, mediaType, nil
}

// ProcessImageMetadata extracts image data from message metadata and converts to Anthropic ContentBlocks.
// This is a convenience wrapper around vision.ExtractImagesFromMetadata that converts to Anthropic's format.
func ProcessImageMetadata(metadata map[string]any) ([]*ContentBlock, error) {
	// Use shared vision package for metadata extraction
	images, err := vision.ExtractImagesFromMetadata(metadata)
	if err != nil {
		return nil, err
	}

	if len(images) == 0 {
		return nil, nil
	}

	// Convert vision.ImageData to Anthropic ContentBlocks
	contentBlocks := make([]*ContentBlock, 0, len(images))

	for _, img := range images {
		contentBlock, err := CreateImageContentBlock(img.Type, img.MediaType, img.Data, img.URL)
		if err != nil {
			return nil, err
		}
		contentBlocks = append(contentBlocks, contentBlock)
	}

	return contentBlocks, nil
}
