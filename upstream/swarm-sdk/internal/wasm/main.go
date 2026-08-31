//go:build js && wasm

// Package main provides WebAssembly bindings for the Swarm SDK.
// This compiles the Go SDK to WASM so it can run directly in the browser.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// clientInstance holds the initialized Swarm SDK client
var clientInstance *client.Client

// promiseResult creates a JavaScript Promise from a Go function
func promiseResult(fn func() (js.Value, error)) js.Value {
	handler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		resolve := args[0]
		reject := args[1]

		go func() {
			result, err := fn()
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(result)
		}()

		return nil
	})

	return js.Global().Get("Promise").New(handler)
}

// jsValue converts Go values to JavaScript values
func jsValue(v interface{}) js.Value {
	switch val := v.(type) {
	case string:
		return js.ValueOf(val)
	case int:
		return js.ValueOf(val)
	case bool:
		return js.ValueOf(val)
	case nil:
		return js.Null()
	default:
		// Convert to JSON for complex types
		jsonBytes, _ := json.Marshal(v)
		return js.Global().Get("JSON").Call("parse", string(jsonBytes))
	}
}

// ============================================================================
// Client Lifecycle
// ============================================================================

// initClient initializes the Swarm SDK client with configuration
func initClient(this js.Value, args []js.Value) interface{} {
	return promiseResult(func() (js.Value, error) {
		if len(args) < 1 {
			return js.Null(), fmt.Errorf("config object required")
		}

		configObj := args[0]

		// Build options from JavaScript config
		var opts []client.Option

		// Provider
		if provider := configObj.Get("provider"); !provider.IsUndefined() {
			model := configObj.Get("model")
			if model.IsUndefined() {
				return js.Null(), fmt.Errorf("model required when provider specified")
			}
			opts = append(opts, client.WithProvider(provider.String(), model.String()))
		}

		// Workspace
		if workspace := configObj.Get("workspace"); !workspace.IsUndefined() {
			opts = append(opts, client.WithWorkspace(workspace.String()))
		}

		// System prompt
		if systemPrompt := configObj.Get("systemPrompt"); !systemPrompt.IsUndefined() {
			opts = append(opts, client.WithSystemPrompt(systemPrompt.String()))
		}

		// API Key
		if apiKey := configObj.Get("apiKey"); !apiKey.IsUndefined() {
			opts = append(opts, client.WithAPIKey(apiKey.String()))
		}

		// Base URL
		if baseURL := configObj.Get("baseURL"); !baseURL.IsUndefined() {
			opts = append(opts, client.WithBaseURL(baseURL.String()))
		}

		// Tools
		if enableAllTools := configObj.Get("enableAllTools"); !enableAllTools.IsUndefined() && enableAllTools.Bool() {
			opts = append(opts, client.WithAllTools())
		}

		if enableBuiltinTools := configObj.Get("enableBuiltinTools"); !enableBuiltinTools.IsUndefined() && enableBuiltinTools.Bool() {
			opts = append(opts, client.WithBuiltinTools())
		}

		if enableIITools := configObj.Get("enableIITools"); !enableIITools.IsUndefined() && enableIITools.Bool() {
			opts = append(opts, client.WithIITools())
		}

		// Hooks
		if enableDefaultHooks := configObj.Get("enableDefaultHooks"); !enableDefaultHooks.IsUndefined() && enableDefaultHooks.Bool() {
			opts = append(opts, client.WithDefaultHooks())
		}

		if enableSteering := configObj.Get("enableSteering"); !enableSteering.IsUndefined() && enableSteering.Bool() {
			opts = append(opts, client.WithSteering())
		}

		// Approval mode
		if approvalMode := configObj.Get("approvalMode"); !approvalMode.IsUndefined() {
			opts = append(opts, client.WithApprovalMode(approvalMode.String()))
		}

		// Create client
		c, err := client.New(opts...)
		if err != nil {
			return js.Null(), fmt.Errorf("failed to create client: %w", err)
		}

		clientInstance = c
		return jsValue(map[string]string{"status": "initialized"}), nil
	})
}

// closeClient closes the Swarm SDK client
func closeClient(this js.Value, args []js.Value) interface{} {
	return promiseResult(func() (js.Value, error) {
		if clientInstance == nil {
			return js.Null(), fmt.Errorf("client not initialized")
		}

		if err := clientInstance.Close(); err != nil {
			return js.Null(), err
		}

		clientInstance = nil
		return jsValue(map[string]string{"status": "closed"}), nil
	})
}

// ============================================================================
// Chat & Generation
// ============================================================================

// chat sends a message and returns a response
func chat(this js.Value, args []js.Value) interface{} {
	return promiseResult(func() (js.Value, error) {
		if clientInstance == nil {
			return js.Null(), fmt.Errorf("client not initialized")
		}

		if len(args) < 1 {
			return js.Null(), fmt.Errorf("message required")
		}

		message := args[0].String()

		var conversationID string
		if len(args) > 1 && !args[1].IsUndefined() {
			conversationID = args[1].String()
		}

		var response string
		var err error

		if conversationID != "" {
			response, err = clientInstance.ChatInConversation(conversationID, message)
		} else {
			response, err = clientInstance.Chat(message)
		}

		if err != nil {
			return js.Null(), err
		}

		return jsValue(map[string]string{
			"response": response,
			"done":     "true",
		}), nil
	})
}

// generate generates a one-off response without conversation state
func generate(this js.Value, args []js.Value) interface{} {
	return promiseResult(func() (js.Value, error) {
		if clientInstance == nil {
			return js.Null(), fmt.Errorf("client not initialized")
		}

		if len(args) < 1 {
			return js.Null(), fmt.Errorf("prompt required")
		}

		prompt := args[0].String()

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		response, err := clientInstance.Generate(ctx, prompt)
		if err != nil {
			return js.Null(), err
		}

		return jsValue(map[string]string{
			"response": response,
			"done":     "true",
		}), nil
	})
}

// ============================================================================
// Conversations
// ============================================================================

// newConversation creates a new conversation
func newConversation(this js.Value, args []js.Value) interface{} {
	return promiseResult(func() (js.Value, error) {
		if clientInstance == nil {
			return js.Null(), fmt.Errorf("client not initialized")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		conv, err := clientInstance.NewConversation(ctx)
		if err != nil {
			return js.Null(), err
		}

		return jsValue(map[string]interface{}{
			"id":         conv.ID,
			"created_at": conv.CreatedAt.Format(time.RFC3339),
			"updated_at": conv.UpdatedAt.Format(time.RFC3339),
			"title":      conv.Title,
		}), nil
	})
}

// listConversations lists all conversations
func listConversations(this js.Value, args []js.Value) interface{} {
	return promiseResult(func() (js.Value, error) {
		if clientInstance == nil {
			return js.Null(), fmt.Errorf("client not initialized")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		convs, err := clientInstance.ListAllConversations(ctx)
		if err != nil {
			return js.Null(), err
		}

		var result []map[string]interface{}
		for _, conv := range convs {
			result = append(result, map[string]interface{}{
				"id":         conv.ID,
				"created_at": conv.CreatedAt.Format(time.RFC3339),
				"updated_at": conv.UpdatedAt.Format(time.RFC3339),
				"title":      conv.Title,
			})
		}

		return jsValue(result), nil
	})
}

// ============================================================================
// Agent Execution
// ============================================================================

// execute runs an agent with tools
func execute(this js.Value, args []js.Value) interface{} {
	return promiseResult(func() (js.Value, error) {
		if clientInstance == nil {
			return js.Null(), fmt.Errorf("client not initialized")
		}

		if len(args) < 1 {
			return js.Null(), fmt.Errorf("request object required")
		}

		// Parse request from JavaScript
		requestJSON := js.Global().Get("JSON").Call("stringify", args[0]).String()

		var req agent.ExecuteRequest
		if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
			return js.Null(), fmt.Errorf("invalid request: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()

		resp, err := clientInstance.Execute(ctx, req)
		if err != nil {
			return js.Null(), err
		}

		// Serialize response to JSON and parse back to JS
		respJSON, _ := json.Marshal(resp)
		return js.Global().Get("JSON").Call("parse", string(respJSON)), nil
	})
}

// ============================================================================
// Main - Register exports
// ============================================================================

func main() {
	// Register functions on the global object
	js.Global().Set("swarmInitClient", js.FuncOf(initClient))
	js.Global().Set("swarmCloseClient", js.FuncOf(closeClient))
	js.Global().Set("swarmChat", js.FuncOf(chat))
	js.Global().Set("swarmGenerate", js.FuncOf(generate))
	js.Global().Set("swarmNewConversation", js.FuncOf(newConversation))
	js.Global().Set("swarmListConversations", js.FuncOf(listConversations))
	js.Global().Set("swarmExecute", js.FuncOf(execute))

	// Keep the program running
	select {}
}
