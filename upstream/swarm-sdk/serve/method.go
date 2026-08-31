package serve

import (
	"context"
	"encoding/json"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// MethodHandler is the signature for an RPC method implementation.
//
// params arrives as a json.RawMessage so each handler controls its own
// unmarshalling (and validation) into a typed param struct.  Returning a
// non-nil error short-circuits to a JSON-RPC error response; returning an
// *ErrorObject preserves a custom code/message.
type MethodHandler func(ctx context.Context, c *client.Client, params json.RawMessage) (any, error)

// Method describes a single RPC method registration.
//
// Streams indicates that the handler emits intermediate updates over the
// connection while the call is in flight (e.g. SendMessage).  The HTTP
// transport ignores this flag; WebSocket honours it by correlating push
// notifications via request id.
type Method struct {
	Name    string
	Handler MethodHandler
	Streams bool
}
