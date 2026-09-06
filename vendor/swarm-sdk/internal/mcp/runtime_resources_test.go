package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	mcpsdk "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/tools/mcp/mocks"
)

type mockTransport struct {
	sent      []any
	responses []any
	connected bool
}

func (m *mockTransport) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

func (m *mockTransport) Close() error {
	m.connected = false
	return nil
}

func (m *mockTransport) Send(ctx context.Context, message any) error {
	m.sent = append(m.sent, message)
	return nil
}

func (m *mockTransport) Receive(ctx context.Context) (any, error) {
	if len(m.responses) == 0 {
		return nil, fmt.Errorf("no responses queued")
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

func (m *mockTransport) IsConnected() bool {
	return m.connected
}

func (m *mockTransport) TransportType() mcpsdk.TransportType {
	return mcpsdk.TransportStdio
}

type fakeResourceFetcher struct {
	listResources []*mcpsdk.MCPResource
	listCursor    string
	listLimit     int
	nextCursor    string
	readResource  *mcpsdk.ResourceContents
	listCalls     int
	readCalls     []string
	err           error
}

func (f *fakeResourceFetcher) ListResources(ctx context.Context, serverName, cursor string, limit int) ([]*mcpsdk.MCPResource, string, error) {
	f.listCalls++
	f.listCursor = cursor
	f.listLimit = limit
	return f.listResources, f.nextCursor, f.err
}

func (f *fakeResourceFetcher) ReadResource(ctx context.Context, serverName, uri string) (*mcpsdk.ResourceContents, error) {
	f.readCalls = append(f.readCalls, uri)
	if f.err != nil {
		return nil, f.err
	}
	return f.readResource, nil
}

func TestRuntimeManagerResourceToolsInvokeClient(t *testing.T) {
	transport := &mockTransport{
		connected: true,
		responses: []any{
			&mcpsdk.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      1,
				Result: map[string]any{
					"resources": []any{
						map[string]any{
							"uri":  "file:///alpha.txt",
							"name": "alpha.txt",
						},
					},
				},
			},
			&mcpsdk.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      2,
				Result: map[string]any{
					"contents": []any{
						map[string]any{
							"uri":      "file:///alpha.txt",
							"mimeType": "text/plain",
							"text":     "hello",
						},
					},
				},
			},
		},
	}
	client := mcpsdk.NewClient(transport, mocks.NewLogger(), mocks.NewTracer())

	registry := tools.NewSimpleRegistry(nil, nil)
	manager := NewRuntimeManager(nil, registry, nil, nil, nil)
	state := &ServerState{
		Config:  ServerConfig{Name: "alpha", Enabled: true},
		Client:  client,
		Tools:   make(map[string]*ToolState),
		toolMap: make(map[string]string),
		Status:  ServerStatus{State: "connected"},
	}
	manager.servers = map[string]*ServerState{
		"alpha": state,
	}

	manager.registerTools(context.Background(), state)

	if _, ok := registry.Registration("mcp_alpha_resources_list"); !ok {
		t.Fatal("expected resources_list tool to be registered")
	}
	if _, ok := registry.Registration("mcp_alpha_resources_read"); !ok {
		t.Fatal("expected resources_read tool to be registered")
	}

	listTool, err := registry.Get("mcp_alpha_resources_list")
	if err != nil {
		t.Fatalf("get list tool failed: %v", err)
	}
	if _, err := listTool.Execute(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("list tool execute failed: %v", err)
	}

	readTool, err := registry.Get("mcp_alpha_resources_read")
	if err != nil {
		t.Fatalf("get read tool failed: %v", err)
	}
	if _, err := readTool.Execute(context.Background(), map[string]any{
		"uri": "file:///alpha.txt",
	}); err != nil {
		t.Fatalf("read tool execute failed: %v", err)
	}

	if len(transport.sent) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(transport.sent))
	}
	firstReq, ok := transport.sent[0].(*mcpsdk.JSONRPCRequest)
	if !ok || firstReq.Method != "resources/list" {
		t.Fatalf("expected first request resources/list, got %T %v", transport.sent[0], transport.sent[0])
	}
	secondReq, ok := transport.sent[1].(*mcpsdk.JSONRPCRequest)
	if !ok || secondReq.Method != "resources/read" {
		t.Fatalf("expected second request resources/read, got %T %v", transport.sent[1], transport.sent[1])
	}
	if secondReq.Params["uri"] != "file:///alpha.txt" {
		t.Fatalf("expected read uri param file:///alpha.txt, got %v", secondReq.Params["uri"])
	}
}

func TestMCPResourcesListToolExecute(t *testing.T) {
	fetcher := &fakeResourceFetcher{
		listResources: []*mcpsdk.MCPResource{
			{URI: "file:///one.txt", Name: "one.txt"},
			{URI: "file:///two.txt", Name: "two.txt"},
		},
		nextCursor: "cursor-2",
	}
	tool := newMCPResourcesListTool("alpha", fetcher)

	result, err := tool.Execute(context.Background(), map[string]any{
		"cursor": "cursor-1",
		"limit":  2,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if fetcher.listCalls != 1 {
		t.Fatalf("expected 1 list call, got %d", fetcher.listCalls)
	}
	if fetcher.listCursor != "cursor-1" {
		t.Fatalf("expected cursor-1, got %q", fetcher.listCursor)
	}
	if fetcher.listLimit != 2 {
		t.Fatalf("expected limit 2, got %d", fetcher.listLimit)
	}
	if len(result.ResourceBlocks()) != 2 {
		t.Fatalf("expected 2 resource blocks, got %d", len(result.ResourceBlocks()))
	}
	if !strings.Contains(result.Output, "2 resources") {
		t.Errorf("expected output to mention resource count, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "cursor-2") {
		t.Errorf("expected output to mention next cursor, got %q", result.Output)
	}
}

func TestMCPResourcesReadToolExecuteBinary(t *testing.T) {
	fetcher := &fakeResourceFetcher{
		readResource: &mcpsdk.ResourceContents{
			URI:      "file:///blob.bin",
			MimeType: "application/octet-stream",
			Blob:     "AAAA",
		},
	}
	tool := newMCPResourcesReadTool("alpha", fetcher)

	result, err := tool.Execute(context.Background(), map[string]any{
		"uri": "file:///blob.bin",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(fetcher.readCalls) != 1 || fetcher.readCalls[0] != "file:///blob.bin" {
		t.Fatalf("expected read call for file:///blob.bin, got %v", fetcher.readCalls)
	}
	if len(result.ResourceBlocks()) != 1 {
		t.Fatalf("expected 1 resource block, got %d", len(result.ResourceBlocks()))
	}
	if !strings.Contains(result.Output, "Binary resource") {
		t.Errorf("expected binary summary in output, got %q", result.Output)
	}
}
