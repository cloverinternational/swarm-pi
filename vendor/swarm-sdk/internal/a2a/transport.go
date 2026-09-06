package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	jsonRPCVersion = "2.0"
	contentJSON    = "application/json"
	contentSSE     = "text/event-stream"
)

const (
	errCodeTaskNotFound                 = -32001
	errCodeTaskNotCancelable            = -32002
	errCodePushNotificationUnsupported  = -32003
	errCodeUnsupportedOperation         = -32004
	errCodeContentTypeNotSupported      = -32005
	errCodeInvalidAgentResponse         = -32006
	errCodeExtendedAgentCardUnavailable = -32007
	errCodeExtensionSupportRequired     = -32008
	errCodeVersionNotSupported          = -32009
)

// DefaultRPCTimeout is the default timeout for A2A RPC calls.
// This prevents indefinite hangs when peers are unresponsive.
const DefaultRPCTimeout = 30 * time.Second

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type a2aError struct {
	Code       int
	Reason     string
	Message    string
	HTTPStatus int
	Metadata   map[string]string
}

func (e *a2aError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func taskNotFoundError(taskID string) *a2aError {
	return &a2aError{
		Code:       errCodeTaskNotFound,
		Reason:     "TASK_NOT_FOUND",
		Message:    "Task not found",
		HTTPStatus: http.StatusNotFound,
		Metadata:   map[string]string{"taskId": taskID},
	}
}

func taskNotCancelableError(taskID string) *a2aError {
	return &a2aError{
		Code:       errCodeTaskNotCancelable,
		Reason:     "TASK_NOT_CANCELABLE",
		Message:    "Task is not cancelable",
		HTTPStatus: http.StatusConflict,
		Metadata:   map[string]string{"taskId": taskID},
	}
}

func unsupportedOperationError(message string) *a2aError {
	return &a2aError{
		Code:       errCodeUnsupportedOperation,
		Reason:     "UNSUPPORTED_OPERATION",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

func pushNotificationUnsupportedError() *a2aError {
	return &a2aError{
		Code:       errCodePushNotificationUnsupported,
		Reason:     "PUSH_NOTIFICATION_NOT_SUPPORTED",
		Message:    "Push notifications are not supported",
		HTTPStatus: http.StatusBadRequest,
	}
}

func versionNotSupportedError(version string) *a2aError {
	return &a2aError{
		Code:       errCodeVersionNotSupported,
		Reason:     "VERSION_NOT_SUPPORTED",
		Message:    fmt.Sprintf("The requested A2A protocol version %s is not supported by this agent", version),
		HTTPStatus: http.StatusBadRequest,
		Metadata:   map[string]string{"requestedVersion": version},
	}
}

func marshalProto(msg proto.Message) (json.RawMessage, error) {
	if msg == nil {
		return json.RawMessage("null"), nil
	}
	raw, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func unmarshalProto(raw json.RawMessage, msg proto.Message) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return protojson.Unmarshal(raw, msg)
}

func writeJSONRPCResult(w http.ResponseWriter, id any, msg proto.Message) {
	result, err := marshalProto(msg)
	if err != nil {
		writeJSONRPCError(w, id, &a2aError{
			Code:       errCodeInvalidAgentResponse,
			Reason:     "INVALID_AGENT_RESPONSE",
			Message:    "Failed to encode protocol response",
			HTTPStatus: http.StatusBadGateway,
		})
		return
	}
	writeJSONRPCResponse(w, http.StatusOK, jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
	})
}

func writeJSONRPCError(w http.ResponseWriter, id any, err *a2aError) {
	status := http.StatusInternalServerError
	if err != nil && err.HTTPStatus > 0 {
		status = err.HTTPStatus
	}
	data := any(nil)
	if err != nil && err.Reason != "" {
		info := map[string]any{
			"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
			"reason": err.Reason,
			"domain": "a2a-protocol.org",
		}
		if len(err.Metadata) > 0 {
			info["metadata"] = err.Metadata
		}
		data = []map[string]any{info}
	}
	writeJSONRPCResponse(w, status, jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &jsonRPCError{
			Code:    err.Code,
			Message: err.Message,
			Data:    data,
		},
	})
}

func writeJSONRPCResponse(w http.ResponseWriter, status int, resp jsonRPCResponse) {
	w.Header().Set("Content-Type", contentJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func validateServiceHeaders(h http.Header) *a2aError {
	version := strings.TrimSpace(h.Get("A2A-Version"))
	if version == "" {
		version = ProtocolVersion
	}
	if version != ProtocolVersion {
		return versionNotSupportedError(version)
	}
	return nil
}

// Client performs A2A JSON-RPC and SSE requests.
type Client struct {
	httpClient *http.Client
}

// NewClient constructs an A2A JSON-RPC client.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: DefaultRPCTimeout,
		}
	}
	return &Client{httpClient: httpClient}
}

// FetchAgentCard retrieves the well-known A2A agent card from a base URL.
func (c *Client) FetchAgentCard(ctx context.Context, baseURL string) (*a2apb.AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+AgentCardPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("A2A-Version", ProtocolVersion)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return nil, fmt.Errorf("fetch agent card: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	card := &a2apb.AgentCard{}
	if err := protojson.Unmarshal(raw, card); err != nil {
		return nil, err
	}
	return card, nil
}

// SendMessage executes the JSON-RPC SendMessage method.
func (c *Client) SendMessage(ctx context.Context, rpcURL string, req *a2apb.SendMessageRequest, extensions []string) (*a2apb.SendMessageResponse, error) {
	resp := &a2apb.SendMessageResponse{}
	if err := c.callProto(ctx, rpcURL, "SendMessage", req, resp, extensions); err != nil {
		return nil, err
	}
	return resp, nil
}

// GetTask executes the JSON-RPC GetTask method.
func (c *Client) GetTask(ctx context.Context, rpcURL string, req *a2apb.GetTaskRequest, extensions []string) (*a2apb.Task, error) {
	task := &a2apb.Task{}
	if err := c.callProto(ctx, rpcURL, "GetTask", req, task, extensions); err != nil {
		return nil, err
	}
	return task, nil
}

// ListTasks executes the JSON-RPC ListTasks method.
func (c *Client) ListTasks(ctx context.Context, rpcURL string, req *a2apb.ListTasksRequest, extensions []string) (*a2apb.ListTasksResponse, error) {
	resp := &a2apb.ListTasksResponse{}
	if err := c.callProto(ctx, rpcURL, "ListTasks", req, resp, extensions); err != nil {
		return nil, err
	}
	return resp, nil
}

// CancelTask executes the JSON-RPC CancelTask method.
func (c *Client) CancelTask(ctx context.Context, rpcURL string, req *a2apb.CancelTaskRequest, extensions []string) (*a2apb.Task, error) {
	task := &a2apb.Task{}
	if err := c.callProto(ctx, rpcURL, "CancelTask", req, task, extensions); err != nil {
		return nil, err
	}
	return task, nil
}

// SendStreamingMessage executes the streaming SendStreamingMessage method.
func (c *Client) SendStreamingMessage(ctx context.Context, rpcURL string, req *a2apb.SendMessageRequest, extensions []string, onEvent func(*a2apb.StreamResponse) error) error {
	return c.callStream(ctx, rpcURL, "SendStreamingMessage", req, extensions, onEvent)
}

// SubscribeToTask executes the streaming SubscribeToTask method.
func (c *Client) SubscribeToTask(ctx context.Context, rpcURL string, req *a2apb.SubscribeToTaskRequest, extensions []string, onEvent func(*a2apb.StreamResponse) error) error {
	return c.callStream(ctx, rpcURL, "SubscribeToTask", req, extensions, onEvent)
}

func (c *Client) callProto(ctx context.Context, rpcURL, method string, req proto.Message, resp proto.Message, extensions []string) error {
	body, err := c.buildJSONRPCBody(method, req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", contentJSON)
	httpReq.Header.Set("Accept", contentJSON)
	httpReq.Header.Set("A2A-Version", ProtocolVersion)
	if len(extensions) > 0 {
		httpReq.Header.Set("A2A-Extensions", strings.Join(extensions, ","))
	}
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return err
	}
	envelope := &jsonRPCResponse{}
	if err := json.Unmarshal(raw, envelope); err != nil {
		return fmt.Errorf("decode json-rpc response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("a2a %s: %s", method, envelope.Error.Message)
	}
	return unmarshalProto(envelope.Result, resp)
}

func (c *Client) callStream(ctx context.Context, rpcURL, method string, req proto.Message, extensions []string, onEvent func(*a2apb.StreamResponse) error) error {
	body, err := c.buildJSONRPCBody(method, req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", contentJSON)
	httpReq.Header.Set("Accept", contentSSE)
	httpReq.Header.Set("A2A-Version", ProtocolVersion)
	if len(extensions) > 0 {
		httpReq.Header.Set("A2A-Extensions", strings.Join(extensions, ","))
	}
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 16<<10))
		return fmt.Errorf("stream %s: %s: %s", method, httpResp.Status, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(httpResp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		envelope := &jsonRPCResponse{}
		if err := json.Unmarshal([]byte(payload), envelope); err != nil {
			return fmt.Errorf("decode sse json-rpc envelope: %w", err)
		}
		if envelope.Error != nil {
			return fmt.Errorf("a2a %s: %s", method, envelope.Error.Message)
		}
		event := &a2apb.StreamResponse{}
		if err := unmarshalProto(envelope.Result, event); err != nil {
			return fmt.Errorf("decode stream event: %w", err)
		}
		if onEvent != nil {
			if err := onEvent(event); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func (c *Client) buildJSONRPCBody(method string, req proto.Message) ([]byte, error) {
	params, err := marshalProto(req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(jsonRPCRequest{
		JSONRPC: jsonRPCVersion,
		ID:      ensureID(""),
		Method:  method,
		Params:  params,
	})
}
