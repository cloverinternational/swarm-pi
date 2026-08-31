package serve

import (
	"encoding/json"
	"fmt"
)

const jsonrpcVersion = "2.0"

// JSON-RPC 2.0 standard error codes.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
	ErrUnauthorized   = -32001 // server-defined
)

// Request is a JSON-RPC 2.0 request envelope.
//
// ID is intentionally json.RawMessage so that callers may use either numeric
// or string ids per spec; absent or null means "notification" — the server
// does not send a Response.
type Request struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether req is a JSON-RPC notification (no id, or id
// is JSON null).  Notifications must not receive a response.
func (r *Request) IsNotification() bool {
	if len(r.ID) == 0 {
		return true
	}
	s := string(r.ID)
	return s == "null"
}

// Response is a JSON-RPC 2.0 response envelope.  Exactly one of Result or
// Error is populated.
type Response struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *ErrorObject    `json:"error,omitempty"`
}

// Notification is a server-pushed JSON-RPC notification (id omitted).
type Notification struct {
	Jsonrpc string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// ErrorObject is the JSON-RPC 2.0 error payload.
type ErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface so handlers can return *ErrorObject
// directly via Mux.Dispatch.
func (e *ErrorObject) Error() string {
	if e == nil {
		return ""
	}
	if e.Data != nil {
		return fmt.Sprintf("rpc error %d: %s: %v", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// NewError constructs an *ErrorObject with the given code/message/data.
func NewError(code int, message string, data any) *ErrorObject {
	return &ErrorObject{Code: code, Message: message, Data: data}
}

// makeResponse builds a success Response.
func makeResponse(id json.RawMessage, result any) Response {
	return Response{Jsonrpc: jsonrpcVersion, ID: id, Result: result}
}

// makeErrorResponse builds an error Response.  If err is already an
// *ErrorObject it is preserved verbatim (structured, bounded by the encoder
// at write time — see bounded_json.go). Any other error — including one
// returned by arbitrary/untrusted method-handler code — is mapped to a
// fixed, safe internal-error message. This deliberately never calls err's
// own Error() method: per CONTRACT.md's bounded-encoding contract, an
// unknown generic error must never have arbitrary code (a crafted, slow, or
// panicking Error() implementation) invoked on it just to build a response.
func makeErrorResponse(id json.RawMessage, err error) Response {
	if obj, ok := err.(*ErrorObject); ok {
		return Response{Jsonrpc: jsonrpcVersion, ID: id, Error: obj}
	}
	return Response{Jsonrpc: jsonrpcVersion, ID: id, Error: NewError(ErrInternalError, "internal error", nil)}
}
