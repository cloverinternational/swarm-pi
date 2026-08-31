// Package serve exposes a *client.Client over JSON-RPC 2.0 across HTTP,
// WebSocket, and Server-Sent Events transports.
//
// # Overview
//
// serve is the wire-protocol-agnostic dispatch layer of the SDK.  Given a
// *client.Client, NewMux builds an RPC method table covering the high-traffic
// session surface (sendMessage, snapshot, conversation CRUD, mode/model/agent
// switching, etc.).  Three handler factories then expose that table over
// different transports without any further wiring on the caller's part.
//
// # Basic usage
//
//	c, _ := client.New(client.WithProvider("anthropic", "claude-sonnet-4-5"))
//	c.Start(ctx)
//
//	mux := serve.NewMux(c)
//	http.Handle("/rpc", mux.HTTPHandler())          // POST JSON-RPC 2.0
//	http.Handle("/ws",  mux.WebSocketHandler())     // bidirectional + push events
//	http.Handle("/sse", mux.SSEHandler())           // server-sent events
//	http.ListenAndServe(":8080", nil)
//
// # Wire format
//
// All transports speak JSON-RPC 2.0.  Method names follow the convention
// "client.<verb>" (e.g. "client.sendMessage", "client.snapshot",
// "client.createConversation").  The request envelope is:
//
//	{"jsonrpc":"2.0","id":1,"method":"client.snapshot","params":null}
//
// Responses follow the standard success/error split:
//
//	{"jsonrpc":"2.0","id":1,"result":{...}}
//	{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}
//
// # Streaming
//
// WebSocket clients receive push notifications for both Subscribe events and
// per-call IntermediateUpdates:
//
//	{"jsonrpc":"2.0","method":"client.event","params":{"kind":"agent",...}}
//	{"jsonrpc":"2.0","method":"client.update","params":{...},"id":<corr>}
//
// SSE clients receive a single one-way event stream (one subscription per
// connection); the client closes the connection to unsubscribe.
//
// # Authentication
//
// Optional bearer-token middleware is exposed via Mux.WithAuth.  See AuthFunc
// and BearerAuth.
//
// # Adding more methods
//
// The built-in registration covers ~25 high-value Client methods.  Additional
// methods can be wired via Mux.Register:
//
//	mux.Register(serve.Method{
//	    Name: "client.customCall",
//	    Handler: func(ctx context.Context, c *client.Client, p json.RawMessage) (any, error) {
//	        return c.CustomCall(ctx)
//	    },
//	})
package serve
