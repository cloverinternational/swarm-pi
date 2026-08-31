package chat

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// ToolResultParser extracts images and binary data from tool outputs
type ToolResultParser struct {
	// Patterns for detecting base64 images in text
	dataURIPattern  *regexp.Regexp
	markdownPattern *regexp.Regexp
	jsonPattern     *regexp.Regexp
}

// NewToolResultParser creates a new parser
func NewToolResultParser() *ToolResultParser {
	return &ToolResultParser{
		// Matches: data:image/png;base64,iVBORw0KGgo...
		dataURIPattern: regexp.MustCompile(`data:image/(png|jpeg|jpg|gif|webp);base64,([A-Za-z0-9+/=]+)`),

		// Matches: ![desc](data:image/png;base64,...)
		markdownPattern: regexp.MustCompile(`!\[[^\]]*\]\(data:image/(png|jpeg|jpg|gif|webp);base64,([A-Za-z0-9+/=]+)\)`),

		// Matches: {"type":"image","data":"base64...","mime_type":"image/png"}
		jsonPattern: regexp.MustCompile(`\{\s*"type"\s*:\s*"image"\s*,\s*"data"\s*:\s*"([A-Za-z0-9+/=]+)"\s*,\s*"mime_type"\s*:\s*"(image/[^"]+)"\s*\}`),
	}
}

// ParseOutput extracts images from tool output text
func (p *ToolResultParser) ParseOutput(output string) ([]Attachment, string) {
	var attachments []Attachment
	cleanOutput := output
	imageCounter := 1

	// Extract data URIs
	matches := p.dataURIPattern.FindAllStringSubmatch(output, -1)
	for _, match := range matches {
		if len(match) >= 3 {
			imageType := match[1]
			base64Data := match[2]

			// Validate base64
			decoded, err := base64.StdEncoding.DecodeString(base64Data)
			if err == nil && len(decoded) > 0 {
				mimeType := fmt.Sprintf("image/%s", imageType)
				if imageType == "jpg" {
					mimeType = "image/jpeg"
				}

				att := Attachment{
					FilePath: "",
					FileName: fmt.Sprintf("tool_image_%d.%s", imageCounter, imageType),
					MimeType: mimeType,
					Content:  decoded,
					Size:     int64(len(decoded)),
				}
				attachments = append(attachments, att)

				// Replace in output with reference
				placeholder := fmt.Sprintf("[Tool Image %d: %s]", imageCounter, att.FileName)
				cleanOutput = strings.Replace(cleanOutput, match[0], placeholder, 1)
				imageCounter++
			}
		}
	}

	// Extract markdown embedded images
	mdMatches := p.markdownPattern.FindAllStringSubmatch(output, -1)
	for _, match := range mdMatches {
		if len(match) >= 3 {
			imageType := match[1]
			base64Data := match[2]

			decoded, err := base64.StdEncoding.DecodeString(base64Data)
			if err == nil && len(decoded) > 0 {
				mimeType := fmt.Sprintf("image/%s", imageType)
				if imageType == "jpg" {
					mimeType = "image/jpeg"
				}

				att := Attachment{
					FilePath: "",
					FileName: fmt.Sprintf("tool_image_%d.%s", imageCounter, imageType),
					MimeType: mimeType,
					Content:  decoded,
					Size:     int64(len(decoded)),
				}
				attachments = append(attachments, att)

				placeholder := fmt.Sprintf("[Tool Image %d: %s]", imageCounter, att.FileName)
				cleanOutput = strings.Replace(cleanOutput, match[0], placeholder, 1)
				imageCounter++
			}
		}
	}

	// Extract JSON format
	jsonMatches := p.jsonPattern.FindAllStringSubmatch(output, -1)
	for _, match := range jsonMatches {
		if len(match) >= 3 {
			base64Data := match[1]
			mimeType := match[2]

			decoded, err := base64.StdEncoding.DecodeString(base64Data)
			if err == nil && len(decoded) > 0 {
				// Get extension from mime type
				ext := strings.TrimPrefix(mimeType, "image/")

				att := Attachment{
					FilePath: "",
					FileName: fmt.Sprintf("tool_image_%d.%s", imageCounter, ext),
					MimeType: mimeType,
					Content:  decoded,
					Size:     int64(len(decoded)),
				}
				attachments = append(attachments, att)

				placeholder := fmt.Sprintf("[Tool Image %d: %s]", imageCounter, att.FileName)
				cleanOutput = strings.Replace(cleanOutput, match[0], placeholder, 1)
				imageCounter++
			}
		}
	}

	return attachments, cleanOutput
}

// ParseMCPContent extracts images from MCP-style content blocks
// Format: []{"type": "image", "data": "base64...", "mimeType": "image/png"}
func (p *ToolResultParser) ParseMCPContent(contentBlocks []map[string]any) []Attachment {
	var attachments []Attachment
	imageCounter := 1

	for _, block := range contentBlocks {
		blockType, ok := block["type"].(string)
		if !ok || blockType != "image" {
			continue
		}

		// Get base64 data
		var base64Data string
		if data, ok := block["data"].(string); ok {
			base64Data = data
		} else if data, ok := block["base64"].(string); ok {
			base64Data = data
		} else {
			continue
		}

		// Get mime type
		mimeType := "image/png" // default
		if mime, ok := block["mimeType"].(string); ok {
			mimeType = mime
		} else if mime, ok := block["mime_type"].(string); ok {
			mimeType = mime
		} else if mime, ok := block["mediaType"].(string); ok {
			mimeType = mime
		}

		// Decode
		decoded, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil || len(decoded) == 0 {
			continue
		}

		// Get extension
		ext := strings.TrimPrefix(mimeType, "image/")
		if ext == "jpeg" {
			ext = "jpg"
		}

		att := Attachment{
			FilePath: "",
			FileName: fmt.Sprintf("mcp_image_%d.%s", imageCounter, ext),
			MimeType: mimeType,
			Content:  decoded,
			Size:     int64(len(decoded)),
		}
		attachments = append(attachments, att)
		imageCounter++
	}

	return attachments
}

// extractImagesFromContentBlocks extracts image attachments from SDK content blocks.
// This handles image data returned by tools like Read that use content blocks.
func extractImagesFromContentBlocks(contentBlocks []any) []Attachment {
	var attachments []Attachment
	imageCounter := 1

	for _, blockInterface := range contentBlocks {
		var blockType, base64Data, mimeType string
		var rawData []byte

		// Use reflection to access struct fields since Go type assertions
		// require exact type match and tools.ContentBlock uses custom types
		val := reflect.ValueOf(blockInterface)

		// Handle pointer types
		if val.Kind() == reflect.Pointer {
			if val.IsNil() {
				continue
			}
			val = val.Elem()
		}

		if val.Kind() == reflect.Struct {
			// Try to get Type field
			if typeField := val.FieldByName("Type"); typeField.IsValid() {
				// ContentType is a string-based type, so use String() or convert
				blockType = fmt.Sprintf("%v", typeField.Interface())
			}

			// Try to get Text field (base64 data for ImageContentBase64)
			if textField := val.FieldByName("Text"); textField.IsValid() && textField.Kind() == reflect.String {
				base64Data = textField.String()
			}

			// Try to get Data field (raw bytes for ImageContent)
			if dataField := val.FieldByName("Data"); dataField.IsValid() && dataField.Kind() == reflect.Slice {
				if dataField.Type().Elem().Kind() == reflect.Uint8 {
					rawData = dataField.Bytes()
				}
			}

			// Try to get MimeType field
			if mimeField := val.FieldByName("MimeType"); mimeField.IsValid() && mimeField.Kind() == reflect.String {
				mimeType = mimeField.String()
			}
		} else if blockMap, ok := blockInterface.(map[string]any); ok {
			// Fallback: Try map-based access for flexibility
			if t, ok := blockMap["Type"].(string); ok {
				blockType = t
			} else if t, ok := blockMap["type"].(string); ok {
				blockType = t
			}

			// Base64 data is in Text field for ImageContentBase64
			if d, ok := blockMap["Text"].(string); ok {
				base64Data = d
			} else if d, ok := blockMap["text"].(string); ok {
				base64Data = d
			}

			// Raw data
			if d, ok := blockMap["Data"].([]byte); ok {
				rawData = d
			} else if d, ok := blockMap["data"].([]byte); ok {
				rawData = d
			}

			if m, ok := blockMap["MimeType"].(string); ok {
				mimeType = m
			} else if m, ok := blockMap["mimeType"].(string); ok {
				mimeType = m
			}
		} else {
			// Log unexpected type for debugging
			logDebug("[extractImages] Unknown block type: %T, value: %+v", blockInterface, blockInterface)
			continue
		}

		// Check if this is an image block
		// ContentType "image" appears as just "image" when converted to string
		if blockType != "image" && !strings.Contains(strings.ToLower(blockType), "image") {
			logDebug("[extractImages] Skipping non-image block, type=%s", blockType)
			continue
		}

		logDebug("[extractImages] Found image block: type=%s, mimeType=%s, base64len=%d, rawlen=%d",
			blockType, mimeType, len(base64Data), len(rawData))

		// Get image data - either from raw bytes or base64
		var imageData []byte
		if len(rawData) > 0 {
			imageData = rawData
		} else if base64Data != "" {
			var err error
			imageData, err = base64.StdEncoding.DecodeString(base64Data)
			if err != nil {
				logDebug("[extractImages] Failed to decode base64: %v", err)
				continue
			}
		} else {
			logDebug("[extractImages] No image data found in block")
			continue
		}

		if len(imageData) == 0 {
			continue
		}

		if mimeType == "" {
			mimeType = "image/png"
		}

		ext := strings.TrimPrefix(mimeType, "image/")
		if ext == "jpeg" {
			ext = "jpg"
		}

		att := Attachment{
			FilePath: "",
			FileName: fmt.Sprintf("content_image_%d.%s", imageCounter, ext),
			MimeType: mimeType,
			Content:  imageData,
			Size:     int64(len(imageData)),
		}
		attachments = append(attachments, att)
		logDebug("[extractImages] Created attachment: %s (%d bytes)", att.FileName, att.Size)
		imageCounter++
	}

	return attachments
}
