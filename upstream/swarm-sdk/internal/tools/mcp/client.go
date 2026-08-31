package mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

// Client represents an MCP client connection to a server.
// This wraps the official MCP Go SDK client while maintaining backward compatibility.
type Client struct {
	transport    Transport
	capabilities *ServerCapabilities
	toolsMap     map[string]*MCPTool
	resourcesMap map[string]*MCPResource
	promptsMap   map[string]*MCPPrompt

	requestID atomic.Uint64
	mu        sync.RWMutex
	logger    observability.Logger
	tracer    observability.Tracer

	// Official SDK client (for future direct integration)
}

const maxResourceContentBytes = 1024 * 1024

// NewClient creates a new MCP client with the given transport.
func NewClient(transport Transport, logger observability.Logger, tracer observability.Tracer) *Client {
	return &Client{
		transport:    transport,
		toolsMap:     make(map[string]*MCPTool),
		resourcesMap: make(map[string]*MCPResource),
		promptsMap:   make(map[string]*MCPPrompt),
		logger:       logger,
		tracer:       tracer,
	}
}

// Connect establishes the connection and initializes the MCP session.
func (c *Client) Connect(ctx context.Context) error {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.connect")
	defer span.End()

	// Connect transport
	if err := c.transport.Connect(ctx); err != nil {
		return fmt.Errorf("transport connection failed: %w", err)
	}

	// Initialize protocol
	if err := c.initialize(ctx); err != nil {
		c.transport.Close()
		return fmt.Errorf("initialization failed: %w", err)
	}

	// Discover tools
	if err := c.discoverTools(ctx); err != nil {
		c.logger.Warn(ctx, "tool discovery failed",
			observability.F("error", err.Error()))
		// Don't fail connection on discovery error
	}

	// Discover prompts
	if err := c.discoverPrompts(ctx); err != nil {
		c.logger.Warn(ctx, "prompt discovery failed",
			observability.F("error", err.Error()))
	}

	// Discover resources
	if err := c.discoverResources(ctx); err != nil {
		c.logger.Warn(ctx, "resource discovery failed",
			observability.F("error", err.Error()))
	}

	c.logger.Info(ctx, "MCP client connected",
		observability.F("transport", string(c.transport.TransportType())),
		observability.F("tools", len(c.toolsMap)),
		observability.F("prompts", len(c.promptsMap)),
		observability.F("resources", len(c.resourcesMap)))

	return nil
}

func (c *Client) initialize(ctx context.Context) error {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.initialize")
	defer span.End()

	// Send initialize request
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextRequestID(),
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"roots": map[string]any{
					"listChanged": true,
				},
				"sampling": map[string]any{},
			},
			"clientInfo": map[string]any{
				"name":    "swarmos-sdk",
				"version": MCPGoSDKVersion,
			},
		},
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return err
	}

	// Receive response
	msg, err := c.transport.Receive(ctx)
	if err != nil {
		return err
	}

	resp, ok := msg.(*JSONRPCResponse)
	if !ok {
		return fmt.Errorf("expected response, got %T", msg)
	}

	if resp.Error != nil {
		return fmt.Errorf("initialization error: %s", resp.Error.Message)
	}

	// Parse server capabilities
	if caps, ok := resp.Result["capabilities"].(map[string]any); ok {
		c.capabilities = &ServerCapabilities{}
		c.logger.Debug(ctx, "Server capabilities received",
			observability.F("capabilities", fmt.Sprintf("%v", caps)))
	}

	// Send initialized notification
	notif := &JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	if err := c.transport.Send(ctx, notif); err != nil {
		return err
	}

	c.logger.Info(ctx, "MCP protocol initialized")

	return nil
}

func (c *Client) discoverTools(ctx context.Context) error {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.discover_tools")
	defer span.End()

	// Send tools/list request
	reqID := c.nextRequestID()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  "tools/list",
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return err
	}

	// Receive response (may need to handle server requests first)
	var resp *JSONRPCResponse
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			return err
		}

		switch m := msg.(type) {
		case *JSONRPCResponse:
			resp = m
			goto GotResponse
		case *JSONRPCRequest:
			// Server is asking us something - handle it
			if err := c.handleServerRequest(ctx, m); err != nil {
				c.logger.Warn(ctx, "failed to handle server request",
					observability.F("method", m.Method),
					observability.F("error", err.Error()))
			}
			// Continue waiting for our response
		case *JSONRPCNotification:
			// Just log notifications
			c.logger.Debug(ctx, "received notification during tool discovery",
				observability.F("method", m.Method))
		}
	}

GotResponse:

	if resp.Error != nil {
		return fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	// Parse tools
	toolsData, ok := resp.Result["tools"].([]any)
	if !ok {
		return fmt.Errorf("invalid tools response format")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, t := range toolsData {
		toolMap, ok := t.(map[string]any)
		if !ok {
			continue
		}

		tool := &MCPTool{
			Name:        toolMap["name"].(string),
			InputSchema: toolMap["inputSchema"].(map[string]any),
		}

		if desc, ok := toolMap["description"].(string); ok {
			tool.Description = desc
		}

		c.toolsMap[tool.Name] = tool

		c.logger.Debug(ctx, "Discovered MCP tool",
			observability.F("name", tool.Name),
			observability.F("description", tool.Description))
	}

	return nil
}

func (c *Client) discoverPrompts(ctx context.Context) error {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.discover_prompts")
	defer span.End()

	reqID := c.nextRequestID()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  "prompts/list",
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return err
	}

	var resp *JSONRPCResponse
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			return err
		}

		switch m := msg.(type) {
		case *JSONRPCResponse:
			resp = m
			goto GotPromptsResponse
		case *JSONRPCRequest:
			if err := c.handleServerRequest(ctx, m); err != nil {
				c.logger.Warn(ctx, "failed to handle server request",
					observability.F("method", m.Method),
					observability.F("error", err.Error()))
			}
		case *JSONRPCNotification:
			c.logger.Debug(ctx, "received notification during prompt discovery",
				observability.F("method", m.Method))
		}
	}

GotPromptsResponse:

	if resp.Error != nil {
		return fmt.Errorf("prompts/list error: %s", resp.Error.Message)
	}

	promptsData, ok := resp.Result["prompts"].([]any)
	if !ok {
		return fmt.Errorf("invalid prompts response format")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range promptsData {
		promptMap, ok := p.(map[string]any)
		if !ok {
			continue
		}

		prompt := &MCPPrompt{
			Name: promptMap["name"].(string),
		}

		if desc, ok := promptMap["description"].(string); ok {
			prompt.Description = desc
		}

		if args, ok := promptMap["arguments"].([]any); ok {
			for _, arg := range args {
				argMap, ok := arg.(map[string]any)
				if !ok {
					continue
				}

				promptArg := PromptArgument{
					Name: argMap["name"].(string),
				}

				if desc, ok := argMap["description"].(string); ok {
					promptArg.Description = desc
				}

				if req, ok := argMap["required"].(bool); ok {
					promptArg.Required = req
				}

				prompt.Arguments = append(prompt.Arguments, promptArg)
			}
		}

		c.promptsMap[prompt.Name] = prompt

		c.logger.Debug(ctx, "Discovered MCP prompt",
			observability.F("name", prompt.Name),
			observability.F("description", prompt.Description))
	}

	return nil
}

func (c *Client) discoverResources(ctx context.Context) error {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.discover_resources")
	defer span.End()

	reqID := c.nextRequestID()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  "resources/list",
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return err
	}

	var resp *JSONRPCResponse
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			return err
		}

		switch m := msg.(type) {
		case *JSONRPCResponse:
			resp = m
			goto GotResourcesResponse
		case *JSONRPCRequest:
			if err := c.handleServerRequest(ctx, m); err != nil {
				c.logger.Warn(ctx, "failed to handle server request",
					observability.F("method", m.Method),
					observability.F("error", err.Error()))
			}
		case *JSONRPCNotification:
			c.logger.Debug(ctx, "received notification during resource discovery",
				observability.F("method", m.Method))
		}
	}

GotResourcesResponse:

	if resp.Error != nil {
		return fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}

	resourcesData, ok := resp.Result["resources"].([]any)
	if !ok {
		return fmt.Errorf("invalid resources response format")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range resourcesData {
		resourceMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		resource := &MCPResource{
			URI:  resourceMap["uri"].(string),
			Name: resourceMap["name"].(string),
		}

		if desc, ok := resourceMap["description"].(string); ok {
			resource.Description = desc
		}

		if mime, ok := resourceMap["mimeType"].(string); ok {
			resource.MimeType = mime
		}

		c.resourcesMap[resource.URI] = resource

		c.logger.Debug(ctx, "Discovered MCP resource",
			observability.F("uri", resource.URI),
			observability.F("name", resource.Name))
	}

	return nil
}

func (c *Client) receiveResponse(ctx context.Context) (*JSONRPCResponse, error) {
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			return nil, err
		}

		switch m := msg.(type) {
		case *JSONRPCResponse:
			return m, nil
		case *JSONRPCRequest:
			if err := c.handleServerRequest(ctx, m); err != nil && c.logger != nil {
				c.logger.Warn(ctx, "failed to handle server request",
					observability.F("method", m.Method),
					observability.F("error", err.Error()))
			}
		case *JSONRPCNotification:
			if c.logger != nil {
				c.logger.Debug(ctx, "received notification",
					observability.F("method", m.Method))
			}
		default:
			return nil, fmt.Errorf("unexpected message type: %T", msg)
		}
	}
}

func (c *Client) handleServerRequest(ctx context.Context, req *JSONRPCRequest) error {
	switch req.Method {
	case "roots/list":
		resp := &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"roots": []any{},
			},
		}
		return c.transport.Send(ctx, resp)

	default:
		resp := &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
		return c.transport.Send(ctx, resp)
	}
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*tools.ToolResult, error) {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.call_tool")
	defer span.End()

	span.SetAttribute("tool.name", name)

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextRequestID(),
		Method:  "tools/call",
		Params: map[string]any{
			"name":      name,
			"arguments": args,
		},
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, err
	}

	msg, err := c.transport.Receive(ctx)
	if err != nil {
		return nil, err
	}

	resp, ok := msg.(*JSONRPCResponse)
	if !ok {
		return nil, fmt.Errorf("expected response, got %T", msg)
	}

	if resp.Error != nil {
		return &tools.ToolResult{
			IsError: true,
			Error:   fmt.Errorf("%s", resp.Error.Message),
			Output:  resp.Error.Message,
		}, nil
	}

	// Parse result
	isError := false
	if ie, ok := resp.Result["isError"].(bool); ok {
		isError = ie
	}

	result := &tools.ToolResult{
		IsError: isError,
		Content: make([]tools.ContentBlock, 0),
	}

	// Parse content blocks
	if contentData, ok := resp.Result["content"].([]any); ok {
		for _, content := range contentData {
			blockMap, ok := content.(map[string]any)
			if !ok {
				continue
			}

			block := parseContentBlock(blockMap)
			result.Content = append(result.Content, block)

			if result.Output == "" && block.Type == tools.ContentTypeText {
				result.Output = block.Text
			}
		}
	}

	return result, nil
}

func parseContentBlock(block map[string]any) tools.ContentBlock {
	blockType, _ := block["type"].(string)

	switch blockType {
	case "text":
		text, _ := block["text"].(string)
		return tools.TextContent(text)

	case "image":
		data, _ := block["data"].(string)
		mimeType, _ := block["mimeType"].(string)
		// MCP servers frequently mislabel the declared mimeType (most often
		// claiming "image/png" for data that is actually JPEG — observed
		// from browser-automation bridges that switch to JPEG encoding for
		// large screenshots but leave the declared type at its default).
		// Anthropic and other providers sniff the real bytes server-side and
		// hard-reject the request when media_type disagrees with the
		// content, so reconcile against the actual bytes here rather than
		// trusting the server's claim verbatim.
		mimeType = vision.ReconcileMediaType(data, mimeType)
		// MCP image data is already base64-encoded — use ImageContentBase64 so translate.go
		// picks it up directly without re-encoding. Using ImageContent([]byte(data)) would
		// store the ASCII bytes of the base64 string, and translate.go would then
		// base64-encode those again, sending double-encoded garbage to Anthropic.
		return tools.ImageContentBase64(data, mimeType)

	case "audio":
		data, _ := block["data"].(string)
		mimeType, _ := block["mimeType"].(string)
		return tools.AudioContent([]byte(data), mimeType)

	case "resource":
		if resource, ok := block["resource"].(map[string]any); ok {
			mimeType, _ := resource["mimeType"].(string)
			// HTML embedded resource: render in client
			if mimeType == "text/html" {
				text, _ := resource["text"].(string)
				cb := tools.HTMLContent(text)
				// Preserve URI as annotation for reference
				if uri, _ := resource["uri"].(string); uri != "" {
					cb = cb.WithAnnotation("uri", uri)
				}
				return cb
			}
			uri, _ := resource["uri"].(string)
			name, _ := resource["name"].(string)
			desc, _ := resource["description"].(string)
			text, _ := resource["text"].(string)
			// Plain text embedded resource
			if text != "" && uri == "" {
				return tools.TextContent(text)
			}
			return tools.ResourceContent(uri, name, desc)
		}
		return tools.TextContent("Invalid resource block")

	default:
		return tools.TextContent(fmt.Sprintf("Unknown content type: %s", blockType))
	}
}

// ListTools returns all discovered tools.
func (c *Client) ListTools() []*MCPTool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	toolList := make([]*MCPTool, 0, len(c.toolsMap))
	for _, tool := range c.toolsMap {
		toolList = append(toolList, tool)
	}
	return toolList
}

// GetTool returns a specific tool by name.
func (c *Client) Tool(name string) (*MCPTool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tool, ok := c.toolsMap[name]
	return tool, ok
}

// GetPrompts returns all discovered prompts.
func (c *Client) Prompts() []*MCPPrompt {
	c.mu.RLock()
	defer c.mu.RUnlock()

	promptList := make([]*MCPPrompt, 0, len(c.promptsMap))
	for _, prompt := range c.promptsMap {
		promptList = append(promptList, prompt)
	}
	return promptList
}

// GetPrompt returns a specific prompt by name.
func (c *Client) Prompt(name string) (*MCPPrompt, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	prompt, ok := c.promptsMap[name]
	return prompt, ok
}

// GetResources returns all discovered resources.
func (c *Client) Resources() []*MCPResource {
	c.mu.RLock()
	defer c.mu.RUnlock()

	resourceList := make([]*MCPResource, 0, len(c.resourcesMap))
	for _, resource := range c.resourcesMap {
		resourceList = append(resourceList, resource)
	}
	return resourceList
}

// GetResource returns a specific resource by URI.
func (c *Client) Resource(uri string) (*MCPResource, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	resource, ok := c.resourcesMap[uri]
	return resource, ok
}

// GetPromptContent executes a prompt with the given arguments.
func (c *Client) PromptContent(ctx context.Context, name string, args map[string]string) (*PromptResult, error) {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.get_prompt_content")
	defer span.End()

	span.SetAttribute("prompt.name", name)

	params := map[string]any{
		"name": name,
	}
	if len(args) > 0 {
		params["arguments"] = args
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextRequestID(),
		Method:  "prompts/get",
		Params:  params,
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.receiveResponse(ctx)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("prompts/get error: %s", resp.Error.Message)
	}

	result := &PromptResult{}

	if desc, ok := resp.Result["description"].(string); ok {
		result.Description = desc
	}

	if messagesData, ok := resp.Result["messages"].([]any); ok {
		for _, msg := range messagesData {
			msgMap, ok := msg.(map[string]any)
			if !ok {
				continue
			}

			promptMsg := PromptMessage{}
			if role, ok := msgMap["role"].(string); ok {
				promptMsg.Role = role
			}

			if content, ok := msgMap["content"].(map[string]any); ok {
				promptMsg.Content = parsePromptContent(content)
			}

			result.Messages = append(result.Messages, promptMsg)
		}
	}

	return result, nil
}

func parsePromptContent(content map[string]any) PromptContent {
	pc := PromptContent{}

	if t, ok := content["type"].(string); ok {
		pc.Type = t
	}
	if text, ok := content["text"].(string); ok {
		pc.Text = text
	}
	if data, ok := content["data"].(string); ok {
		pc.Data = data
	}
	if mimeType, ok := content["mimeType"].(string); ok {
		pc.MimeType = mimeType
	}
	if resource, ok := content["resource"].(map[string]any); ok {
		pc.Resource = &EmbeddedResource{}
		if uri, ok := resource["uri"].(string); ok {
			pc.Resource.URI = uri
		}
		if mt, ok := resource["mimeType"].(string); ok {
			pc.Resource.MimeType = mt
		}
		if text, ok := resource["text"].(string); ok {
			pc.Resource.Text = text
		}
		if blob, ok := resource["blob"].(string); ok {
			pc.Resource.Blob = blob
		}
	}

	return pc
}

// ListResources returns all discovered resources, handling pagination automatically.
func (c *Client) ListResources(ctx context.Context) ([]*MCPResource, error) {
	var allResources []*MCPResource
	var cursor string

	for {
		// Pass limit=0 to use server default
		resources, nextCursor, err := c.ListResourcesPaged(ctx, cursor, 0)
		if err != nil {
			return nil, err
		}
		allResources = append(allResources, resources...)

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return allResources, nil
}

// ListResourcesPaged returns resources with pagination support.
func (c *Client) ListResourcesPaged(ctx context.Context, cursor string, limit int) ([]*MCPResource, string, error) {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.list_resources_paged")
	defer span.End()

	params := map[string]any{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	if limit > 0 {
		params["limit"] = limit
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextRequestID(),
		Method:  "resources/list",
		Params:  params,
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, "", err
	}

	resp, err := c.receiveResponse(ctx)
	if err != nil {
		return nil, "", err
	}

	if resp.Error != nil {
		return nil, "", fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}

	resourcesData, ok := resp.Result["resources"].([]any)
	if !ok {
		return nil, "", fmt.Errorf("resources/list response missing resources array")
	}

	var resources []*MCPResource
	for _, r := range resourcesData {
		resourceMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		resource := &MCPResource{
			URI:  resourceMap["uri"].(string),
			Name: resourceMap["name"].(string),
		}

		if desc, ok := resourceMap["description"].(string); ok {
			resource.Description = desc
		}

		if mime, ok := resourceMap["mimeType"].(string); ok {
			resource.MimeType = mime
		}

		resources = append(resources, resource)
	}

	nextCursor := ""
	if nc, ok := resp.Result["nextCursor"].(string); ok {
		nextCursor = nc
	}

	return resources, nextCursor, nil
}

// ReadResource reads the contents of a specific resource by URI.
func (c *Client) ReadResource(ctx context.Context, uri string) (*ResourceContents, error) {
	ctx, span := c.tracer.StartSpan(ctx, "mcp.client.read_resource")
	defer span.End()

	span.SetAttribute("resource.uri", uri)

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextRequestID(),
		Method:  "resources/read",
		Params: map[string]any{
			"uri": uri,
		},
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.receiveResponse(ctx)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("resources/read error: %s", resp.Error.Message)
	}

	contentsData, ok := resp.Result["contents"].([]any)
	if !ok || len(contentsData) == 0 {
		return nil, fmt.Errorf("no contents in response")
	}

	contentMap, ok := contentsData[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid content format")
	}

	contents := &ResourceContents{
		URI: uri,
	}

	if mime, ok := contentMap["mimeType"].(string); ok {
		contents.MimeType = mime
	}

	if text, ok := contentMap["text"].(string); ok {
		if len(text) > maxResourceContentBytes {
			return nil, fmt.Errorf("resource content exceeds %d bytes", maxResourceContentBytes)
		}
		contents.Text = text
	}

	if blob, ok := contentMap["blob"].(string); ok {
		if len(blob) > maxResourceContentBytes {
			return nil, fmt.Errorf("resource content exceeds %d bytes", maxResourceContentBytes)
		}
		contents.Blob = blob
	}

	return contents, nil
}

// Close terminates the connection.
func (c *Client) Close() error {
	return c.transport.Close()
}

// IsConnected returns true if the client transport is connected.
func (c *Client) IsConnected() bool {
	if c.transport == nil {
		return false
	}
	return c.transport.IsConnected()
}

func (c *Client) nextRequestID() int {
	return int(c.requestID.Add(1))
}
