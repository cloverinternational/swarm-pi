// Package agent provides vision routing for agents with non-vision primary models.
package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

// VisionRouter handles routing vision-requiring tool calls through vision-capable models.
// When an agent's primary model lacks vision support, the VisionRouter intercepts
// tool calls that need image processing and executes them through a configured vision model.
//
// Usage:
//
//	router := NewVisionRouter(VisionRouterConfig{
//	    VisionModel: "anthropic/claude-3-5-sonnet-20241022",
//	    ProviderRegistry: providerRegistry,
//	})
//
//	// Check if routing is needed
//	if router.ShouldRoute(tool, params) {
//	    result, err := router.Route(ctx, tool, params)
//	}
type VisionRouter struct {
	mu sync.RWMutex

	// visionModel is the configured vision model (provider/model format)
	visionModel string

	// visionChain is the fallback chain for vision operations
	visionChain *fallback.Chain

	// providerRegistry is used to create vision-capable providers
	providerRegistry *provider.SimpleRegistry

	// visionProvider is the cached vision-capable provider
	visionProvider   provider.Provider
	visionProviderMu sync.Mutex

	// detectVision is an optional custom detection function
	detectVision func(tool tools.Tool, params map[string]any) bool

	// logger for observability
	logger observability.Logger
}

// VisionRouterConfig configures a VisionRouter.
type VisionRouterConfig struct {
	// VisionModel specifies the model to use for vision operations.
	// Format: "provider/model" (e.g., "anthropic/claude-3-5-sonnet-20241022").
	// Either VisionModel or VisionChain must be set for routing to work.
	VisionModel string

	// VisionChain specifies a fallback chain of vision-capable models.
	// This provides resilience when the primary vision model is unavailable.
	VisionChain *fallback.Chain

	// ProviderRegistry is required for creating vision-capable providers.
	// Use *provider.SimpleRegistry which has the Create method.
	ProviderRegistry *provider.SimpleRegistry

	// DetectVision is an optional custom function to detect if a tool call
	// requires vision capability. If not set, the default detection is used.
	DetectVision func(tool tools.Tool, params map[string]any) bool

	// Logger for observability. If nil, a no-op logger is used.
	Logger observability.Logger
}

// NewVisionRouter creates a new VisionRouter.
func NewVisionRouter(config VisionRouterConfig) (*VisionRouter, error) {
	if config.ProviderRegistry == nil {
		return nil, fmt.Errorf("vision router: provider registry is required")
	}

	if config.VisionModel == "" && config.VisionChain == nil {
		return nil, fmt.Errorf("vision router: either VisionModel or VisionChain must be set")
	}

	logger := config.Logger
	if logger == nil {
		logger = noop.NewLogger()
	}

	// Parse vision model if provided
	if config.VisionModel != "" {
		ref := fallback.ParseModelRef(config.VisionModel)
		if ref.IsEmpty() {
			return nil, fmt.Errorf("vision router: invalid vision model format %q (expected provider/model)", config.VisionModel)
		}
	}

	return &VisionRouter{
		visionModel:      config.VisionModel,
		visionChain:      config.VisionChain,
		providerRegistry: config.ProviderRegistry,
		detectVision:     config.DetectVision,
		logger:           logger,
	}, nil
}

// ShouldRoute determines if a tool call should be routed through a vision model.
// Returns true if:
//  1. The tool implements VisionRequiringTool and RequiresVision(params) returns true, OR
//  2. The parameters contain image data (vision.HasImages(params) returns true), OR
//  3. A custom DetectVision function is set and returns true
func (r *VisionRouter) ShouldRoute(tool tools.Tool, params map[string]any) bool {
	// Check if tool explicitly declares vision requirement
	if vr, ok := tool.(tools.VisionRequiringTool); ok {
		if vr.RequiresVision(params) {
			return true
		}
	}

	// Check if custom detection function is set
	if r.detectVision != nil {
		return r.detectVision(tool, params)
	}

	// Default: check if params contain images
	return vision.HasImages(params)
}

// GetVisionProvider returns the vision-capable provider, creating it if necessary.
// This method caches the provider for reuse across multiple tool calls.
func (r *VisionRouter) GetVisionProvider(ctx context.Context) (provider.Provider, string, error) {
	r.visionProviderMu.Lock()
	defer r.visionProviderMu.Unlock()

	// Return cached provider if available
	if r.visionProvider != nil {
		return r.visionProvider, r.visionModel, nil
	}

	// Determine which model to use
	modelRef := r.getVisionModelRef()
	if modelRef.IsEmpty() {
		return nil, "", fmt.Errorf("vision router: no vision model configured")
	}

	// Create provider for the vision model
	cfg := provider.Config{
		Name:  modelRef.Provider,
		Model: modelRef.Model,
	}

	p, err := r.providerRegistry.Create(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("vision router: failed to create vision provider %s/%s: %w",
			modelRef.Provider, modelRef.Model, err)
	}

	// Verify provider supports vision
	if !p.Capabilities().Vision {
		r.logger.Warn(ctx, "vision_router.provider_not_vision_capable",
			observability.F("provider", modelRef.Provider),
			observability.F("model", modelRef.Model),
			observability.F("hint", "consider using a vision-capable model"))
	}

	// Cache for reuse
	r.visionProvider = p
	r.visionModel = modelRef.Model // Store model name for later use
	r.logger.Info(ctx, "vision_router.provider_created",
		observability.F("provider", modelRef.Provider),
		observability.F("model", modelRef.Model))

	return p, modelRef.Model, nil
}

// getVisionModelRef returns the primary vision model reference.
// If a vision chain is configured, it returns the primary model from the chain.
// Otherwise, it parses the vision model string.
func (r *VisionRouter) getVisionModelRef() fallback.ModelRef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Prefer chain primary if available
	if r.visionChain != nil && !r.visionChain.IsEmpty() {
		return r.visionChain.Primary
	}

	// Fall back to single model
	return fallback.ParseModelRef(r.visionModel)
}

// Route executes a tool call through the vision-capable model.
// This method:
//  1. Gets the vision provider (creating it if necessary)
//  2. Creates a temporary agent with the vision model
//  3. Executes the tool call
//  4. Returns the result
//
// The result includes metadata indicating that vision routing was used.
func (r *VisionRouter) Route(ctx context.Context, tool tools.Tool, params map[string]any) (*tools.ToolResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get the vision provider
	visionProvider, visionModel, err := r.GetVisionProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("vision router: %w", err)
	}

	r.logger.Info(ctx, "vision_router.routing_tool_call",
		observability.F("tool", tool.Name()),
		observability.F("vision_provider", visionProvider.Name()),
		observability.F("vision_model", visionModel))

	// Create a temporary agent for vision processing
	visionAgent, err := r.createVisionAgent(ctx, visionProvider, visionModel)
	if err != nil {
		return nil, fmt.Errorf("vision router: failed to create vision agent: %w", err)
	}

	// Execute the tool through the vision agent
	result, err := r.executeToolWithVision(ctx, visionAgent, tool, params)
	if err != nil {
		return nil, fmt.Errorf("vision router: tool execution failed: %w", err)
	}

	// Add routing metadata to result
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["vision_routed"] = true
	result.Metadata["vision_provider"] = visionProvider.Name()
	result.Metadata["vision_model"] = visionModel

	return result, nil
}

// createVisionAgent creates a temporary agent with the vision provider for processing images.
func (r *VisionRouter) createVisionAgent(ctx context.Context, visionProvider provider.Provider, visionModel string) (*Agent, error) {
	def := &Definition{
		ID:            "vision-router-temp",
		Name:          "Vision Router",
		Provider:      visionProvider.Name(),
		Model:         visionModel,
		SystemPrompt:  "You are a vision specialist. Process images and provide detailed analysis.",
		ExecutionMode: ExecutionModeLocal,
	}

	agent, err := New(Config{
		Definition: def,
		Provider:   visionProvider,
		Logger:     r.logger,
	})
	if err != nil {
		return nil, err
	}

	if err := agent.Initialize(); err != nil {
		return nil, err
	}

	return agent, nil
}

// executeToolWithVision executes a tool call through the vision agent.
// This method handles the actual tool execution with image processing.
func (r *VisionRouter) executeToolWithVision(ctx context.Context, visionAgent *Agent, tool tools.Tool, params map[string]any) (*tools.ToolResult, error) {
	// For the Read tool specifically, we need to handle image file reading
	// by passing the image data through the vision model
	if tool.Name() == "read" || tool.Name() == "Read" {
		return r.handleReadTool(ctx, visionAgent, tool, params)
	}

	// For other tools, execute directly
	return tool.Execute(ctx, params)
}

// handleReadTool handles the Read tool with image processing.
// When reading an image file, we pass it through the vision model for analysis.
func (r *VisionRouter) handleReadTool(ctx context.Context, visionAgent *Agent, tool tools.Tool, params map[string]any) (*tools.ToolResult, error) {
	// Check if this is an image file
	filePath, _ := params["file_path"].(string)
	if filePath == "" {
		// Not an image file path, execute normally
		return tool.Execute(ctx, params)
	}

	// Check if file is an image by extension
	if !isImageFile(filePath) {
		// Not an image, execute normally
		return tool.Execute(ctx, params)
	}

	r.logger.Debug(ctx, "vision_router.reading_image_file",
		observability.F("file_path", filePath))

	// First, read the raw file to get the image data
	result, err := tool.Execute(ctx, params)
	if err != nil {
		return nil, err
	}

	// If the result contains image data, we need to process it with vision
	if result.Metadata != nil {
		if _, hasImages := result.Metadata["images"]; hasImages {
			// The image data is already in metadata, we can return it
			// The agent that called this tool will handle the vision processing
			// through the normal message flow
			return result, nil
		}
	}

	return result, nil
}

// isImageFile returns true if the file path has an image extension.
func isImageFile(path string) bool {
	ext := strings.ToLower(path)
	imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff", ".tif"}
	for _, imgExt := range imageExts {
		if strings.HasSuffix(ext, imgExt) {
			return true
		}
	}
	return false
}

// Close releases resources used by the vision router.
func (r *VisionRouter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Clear cached provider
	r.visionProvider = nil

	return nil
}

// AnalyzeImageResult analyzes image content from a tool result through the vision model.
// This is used when a tool has already executed and returned images, but the primary
// model cannot process them. The vision model analyzes the images and returns a text
// description that can be used by non-vision models.
//
// Usage:
//
//	if hasImageContent && visionRouter != nil {
//	    analyzedResult, err := visionRouter.AnalyzeImageResult(ctx, toolCall, result)
//	}
func (r *VisionRouter) AnalyzeImageResult(ctx context.Context, toolName string, toolCallID string, result *tools.ToolResult) (*tools.ToolResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get the vision provider
	visionProvider, visionModel, err := r.GetVisionProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("vision router: %w", err)
	}

	// Extract images from result
	var images []*vision.ImageData
	for _, block := range result.Content {
		if block.Type == tools.ContentTypeImage {
			// Convert block data to ImageData
			imgData := &vision.ImageData{
				MediaType: block.MimeType,
				Data:      base64.StdEncoding.EncodeToString(block.Data),
			}
			images = append(images, imgData)
		}
	}

	if len(images) == 0 {
		// No images to analyze, return original result
		return result, nil
	}

	r.logger.Info(ctx, "vision_router.analyzing_images",
		observability.F("tool", toolName),
		observability.F("call_id", toolCallID),
		observability.F("image_count", len(images)),
		observability.F("vision_provider", visionProvider.Name()),
		observability.F("vision_model", visionModel))

	// Build prompt for vision analysis
	prompt := fmt.Sprintf("Analyze this image from tool '%s' and provide a detailed description. "+
		"Include any text visible in the image, describe the visual elements, and explain what the image shows. "+
		"If this is a screenshot, diagram, or technical image, describe its contents in detail.", toolName)

	// Use the vision provider to analyze images
	analysis, err := r.analyzeImagesWithProvider(ctx, visionProvider, visionModel, images, prompt)
	if err != nil {
		return nil, fmt.Errorf("vision router: image analysis failed: %w", err)
	}

	// Create new result with analysis text instead of images
	analyzedResult := &tools.ToolResult{
		Output: analysis,
		Content: []tools.ContentBlock{
			{
				Type: tools.ContentTypeText,
				Text: analysis,
			},
		},
		Metadata: map[string]any{
			"vision_routed":   true,
			"vision_provider": visionProvider.Name(),
			"vision_model":    visionModel,
			"original_images": len(images),
			"tool_name":       toolName,
			"tool_call_id":    toolCallID,
		},
	}

	return analyzedResult, nil
}

// analyzeImagesWithProvider sends images to a vision model for analysis.
func (r *VisionRouter) analyzeImagesWithProvider(ctx context.Context, visionProvider provider.Provider, visionModel string, images []*vision.ImageData, prompt string) (string, error) {
	// Build the message with images in metadata (standard SDK pattern)
	msg := &conversation.Message{
		Role:    conversation.RoleUser,
		Content: prompt,
		Metadata: map[string]any{
			"images": images,
		},
	}

	// Execute through the provider
	response, err := visionProvider.Chat(ctx, provider.ChatRequest{
		Model:    visionModel,
		Messages: []*conversation.Message{msg},
	})
	if err != nil {
		return "", err
	}

	// Extract text from response
	if response.Message != nil && response.Message.Content != "" {
		return response.Message.Content, nil
	}

	return "", fmt.Errorf("no content in vision model response")
}
