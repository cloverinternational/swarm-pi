package vision

import (
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// MetadataKey is the standard key used to store images in conversation.Message.Metadata
const MetadataKey = "images"

// ExtractImagesFromMetadata extracts ImageData from conversation.Message.Metadata.
// Supports multiple input formats:
// - []map[string]string
// - []map[string]any
// - []any
func ExtractImagesFromMetadata(metadata map[string]any) ([]*ImageData, error) {
	if metadata == nil {
		return nil, nil
	}

	// Check for images in metadata
	imagesRaw, ok := metadata[MetadataKey]
	if !ok {
		return nil, nil
	}

	var imagesList []map[string]string

	// Try []map[string]string first (most specific)
	if imgs, ok := imagesRaw.([]map[string]string); ok {
		imagesList = imgs
	} else if imgs, ok := imagesRaw.([]map[string]any); ok {
		// Handle []map[string]any
		imagesList = make([]map[string]string, 0, len(imgs))
		for _, imgMap := range imgs {
			strMap := make(map[string]string)
			for k, v := range imgMap {
				if strVal, ok := v.(string); ok {
					strMap[k] = strVal
				}
			}
			imagesList = append(imagesList, strMap)
		}
	} else if imgsInterface, ok := imagesRaw.([]any); ok {
		// Handle []any (most generic case)
		imagesList = make([]map[string]string, 0, len(imgsInterface))
		for _, img := range imgsInterface {
			if imgMap, ok := img.(map[string]any); ok {
				strMap := make(map[string]string)
				for k, v := range imgMap {
					if strVal, ok := v.(string); ok {
						strMap[k] = strVal
					}
				}
				imagesList = append(imagesList, strMap)
			} else if imgMap, ok := img.(map[string]string); ok {
				imagesList = append(imagesList, imgMap)
			}
		}
	} else {
		return nil, sdkerr.Permanent(
			"vision.invalid_metadata_format",
			"images metadata must be []map[string]string, []map[string]any, or []any",
		)
	}

	if len(imagesList) == 0 {
		return nil, nil
	}

	// Convert to ImageData structs
	images := make([]*ImageData, 0, len(imagesList))

	for i, imageMap := range imagesList {
		imageType, ok := imageMap["type"]
		if !ok {
			return nil, sdkerr.Permanent(
				"vision.missing_type",
				fmt.Sprintf("image %d missing 'type' field", i),
			)
		}

		var imgData *ImageData

		if imageType == "base64" {
			mediaType, ok := imageMap["media_type"]
			if !ok {
				return nil, sdkerr.Permanent(
					"vision.missing_media_type",
					fmt.Sprintf("base64 image %d missing 'media_type' field", i),
				)
			}

			data, ok := imageMap["data"]
			if !ok {
				return nil, sdkerr.Permanent(
					"vision.missing_data",
					fmt.Sprintf("base64 image %d missing 'data' field", i),
				)
			}

			imgData = &ImageData{
				Type:      "base64",
				MediaType: mediaType,
				Data:      data,
			}
		} else if imageType == "url" {
			url, ok := imageMap["url"]
			if !ok {
				return nil, sdkerr.Permanent(
					"vision.missing_url",
					fmt.Sprintf("URL image %d missing 'url' field", i),
				)
			}

			imgData = &ImageData{
				Type: "url",
				URL:  url,
			}
		} else {
			return nil, sdkerr.Permanent(
				"vision.invalid_type_value",
				fmt.Sprintf("image %d has invalid type: %s (must be 'base64' or 'url')", i, imageType),
			)
		}

		// Validate the image data
		if err := imgData.Validate(); err != nil {
			return nil, fmt.Errorf("image %d validation failed: %w", i, err)
		}

		images = append(images, imgData)
	}

	return images, nil
}

// AddImagesToMetadata adds ImageData to conversation.Message.Metadata.
// Creates the metadata map if it doesn't exist.
// Stores images in the standard format: []map[string]string
func AddImagesToMetadata(metadata map[string]any, images []*ImageData) error {
	if len(images) == 0 {
		return nil
	}

	// Convert ImageData to metadata format
	imagesList := make([]map[string]string, len(images))

	for i, img := range images {
		if err := img.Validate(); err != nil {
			return fmt.Errorf("image %d validation failed: %w", i, err)
		}

		imgMap := make(map[string]string)
		imgMap["type"] = img.Type

		if img.Type == "base64" {
			imgMap["media_type"] = img.MediaType
			imgMap["data"] = img.Data
		} else if img.Type == "url" {
			imgMap["url"] = img.URL
		}

		imagesList[i] = imgMap
	}

	metadata[MetadataKey] = imagesList
	return nil
}

// ConvertToMetadataFormat converts ImageData array to metadata format.
// Returns []map[string]string suitable for storing in metadata.
func ConvertToMetadataFormat(images []*ImageData) ([]map[string]string, error) {
	if len(images) == 0 {
		return nil, nil
	}

	imagesList := make([]map[string]string, len(images))

	for i, img := range images {
		if err := img.Validate(); err != nil {
			return nil, fmt.Errorf("image %d validation failed: %w", i, err)
		}

		imgMap := make(map[string]string)
		imgMap["type"] = img.Type

		if img.Type == "base64" {
			imgMap["media_type"] = img.MediaType
			imgMap["data"] = img.Data
		} else if img.Type == "url" {
			imgMap["url"] = img.URL
		}

		imagesList[i] = imgMap
	}

	return imagesList, nil
}

// HasImages checks if metadata contains any images.
func HasImages(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}

	images, ok := metadata[MetadataKey]
	if !ok {
		return false
	}

	// Check if it's a non-empty array
	switch v := images.(type) {
	case []map[string]string:
		return len(v) > 0
	case []map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	default:
		return false
	}
}
