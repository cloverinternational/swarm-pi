// Package http provides HTTP client utilities with integrated observability.
//
// The package offers a fluent API for building HTTP requests with automatic
// tracing, logging, and header management. It wraps Go's standard net/http
// library while adding observability-first design patterns.
//
// # Core Types
//
// The main types are:
//
//   - Request: Wraps http.Request with observability metadata
//   - RequestBuilder: Fluent builder for constructing requests
//   - Response: Wraps http.Response with convenience methods
//   - Client: Higher-level HTTP client with defaults
//   - ResponseHandler: Fluent response handling wrapper
//
// # Builder Pattern
//
// Requests are built using a fluent API that enables method chaining:
//
//	req, err := NewRequest(ctx, logger, tracer).
//		Method("POST").
//		URL("https://api.example.com/data").
//		Header("Authorization", "Bearer token").
//		Body(data).
//		OperationName("send_data").
//		Attribute("user_id", userID).
//		Build()
//
// # Observability Integration
//
// All requests automatically integrate with the observability system:
//
//   - Spans: Automatically created with client span kind
//   - Logging: Request/response details logged at appropriate levels
//   - Trace Context: Injected into request headers for propagation
//   - Attributes: HTTP method, URL, status code, duration recorded
//
// # Response Handling
//
// Responses provide convenient methods for common patterns:
//
//	resp := NewResponse(httpResp, req)
//	defer resp.Close()
//
//	if resp.IsSuccess() {
//		var data MyType
//		err := resp.JSON(&data)
//	}
//
// # Client Usage
//
// For higher-level usage, create a Client with defaults:
//
//	client, err := NewClient(ClientConfig{
//		Logger: logger,
//		Tracer: tracer,
//		DefaultHeaders: map[string]string{
//			"Authorization": "Bearer token",
//		},
//	})
//
//	resp, err := client.Get(ctx, "https://api.example.com/data")
//	if err != nil {
//		// Handle error
//	}
//	defer resp.Close()
//
// # Design Principles
//
// 1. Fluent API: All builder methods return the builder for method chaining
// 2. Observability First: Automatic span creation and logging
// 3. Type Safety: Strong typing with minimal any usage
// 4. Context Propagation: Full context support for cancellation
// 5. Zero Configuration: Works with sensible defaults
//
// # Performance
//
// The package is designed for production use:
//
//   - Body caching: Response body cached after first read
//   - Lazy completion: Spans ended only when Complete() called
//   - Memory pooling: Headers allocated once during build
//   - Connection reuse: Underlying http.Client manages connections
//
// # Error Handling
//
// Errors are recorded on spans and logged:
//
//	if err != nil {
//		req.RecordError(err)
//		req.Complete(0) // 0 indicates no response
//	}
//
// # Thread Safety
//
// Individual Request and Response objects are not thread-safe.
// Each goroutine should create its own request/response objects.
// The underlying http.Request and http.Response follow standard Go patterns.
//
// See the README.md file for detailed documentation and examples.
package http
