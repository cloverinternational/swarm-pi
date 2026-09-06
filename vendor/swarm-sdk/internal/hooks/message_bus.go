// Package hooks implements the event hook system.
// This file provides the MessageBus for decoupled hook execution.
package hooks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MessageType identifies the type of message on the bus.
type MessageType string

const (
	MsgHookExecutionRequest  MessageType = "hook_execution_request"
	MsgHookExecutionResponse MessageType = "hook_execution_response"
)

// Message is the interface for all messages on the bus.
type Message interface {
	Type() MessageType
	CorrelationID() string
}

// HookExecutionRequest requests execution of hooks for an event.
type HookExecutionRequest struct {
	correlationID string
	EventName     HookEventName
	Input         map[string]any
	Context       *EventContext
}

// Type returns the message type.
func (r *HookExecutionRequest) Type() MessageType {
	return MsgHookExecutionRequest
}

// CorrelationID returns the correlation ID.
func (r *HookExecutionRequest) CorrelationID() string {
	return r.correlationID
}

// HookExecutionResponse contains the result of hook execution.
type HookExecutionResponse struct {
	correlationID string
	Success       bool
	Output        *HookResponse
	Error         error
}

// Type returns the message type.
func (r *HookExecutionResponse) Type() MessageType {
	return MsgHookExecutionResponse
}

// CorrelationID returns the correlation ID.
func (r *HookExecutionResponse) CorrelationID() string {
	return r.correlationID
}

// MessageHandler handles messages of a specific type.
type MessageHandler func(ctx context.Context, msg Message) error

// MessageBus provides pub/sub messaging for decoupled hook execution.
type MessageBus struct {
	mu           sync.RWMutex
	subscribers  map[MessageType][]MessageHandler
	pending      map[string]chan Message // correlationID -> response channel
	pendingMu    sync.RWMutex
	bufferSize   int
	started      bool
	shutdownChan chan struct{}
}

// NewMessageBus creates a new message bus.
func NewMessageBus(bufferSize int) *MessageBus {
	if bufferSize <= 0 {
		bufferSize = 100
	}

	return &MessageBus{
		subscribers:  make(map[MessageType][]MessageHandler),
		pending:      make(map[string]chan Message),
		bufferSize:   bufferSize,
		shutdownChan: make(chan struct{}),
	}
}

// Start starts the message bus.
func (b *MessageBus) Start() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.started = true
}

// Stop stops the message bus.
func (b *MessageBus) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.started {
		close(b.shutdownChan)
		b.started = false
	}

	// Clean up pending requests
	b.pendingMu.Lock()
	for _, ch := range b.pending {
		close(ch)
	}
	b.pending = make(map[string]chan Message)
	b.pendingMu.Unlock()
}

// Subscribe registers a handler for a message type.
func (b *MessageBus) Subscribe(msgType MessageType, handler MessageHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers[msgType] = append(b.subscribers[msgType], handler)
}

// Publish publishes a message to all subscribers.
func (b *MessageBus) Publish(ctx context.Context, msg Message) error {
	b.mu.RLock()
	handlers := b.subscribers[msg.Type()]
	started := b.started
	b.mu.RUnlock()

	if !started {
		return fmt.Errorf("message bus not started")
	}

	// Notify handlers
	for _, handler := range handlers {
		if err := handler(ctx, msg); err != nil {
			return err
		}
	}

	// If this is a response, deliver to pending request
	if msg.Type() == MsgHookExecutionResponse {
		b.deliverResponse(msg)
	}

	return nil
}

// Request sends a request and waits for a response.
func (b *MessageBus) Request(ctx context.Context, req *HookExecutionRequest, timeout time.Duration) (*HookExecutionResponse, error) {
	// Generate correlation ID
	req.correlationID = uuid.New().String()

	// Create response channel
	respChan := make(chan Message, 1)
	b.pendingMu.Lock()
	b.pending[req.correlationID] = respChan
	b.pendingMu.Unlock()

	// Cleanup on exit
	defer func() {
		b.pendingMu.Lock()
		delete(b.pending, req.correlationID)
		b.pendingMu.Unlock()
	}()

	// Publish request
	if err := b.Publish(ctx, req); err != nil {
		return nil, err
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, fmt.Errorf("request timeout after %v", timeout)
	case <-b.shutdownChan:
		return nil, fmt.Errorf("message bus shutdown")
	case msg := <-respChan:
		if resp, ok := msg.(*HookExecutionResponse); ok {
			return resp, nil
		}
		return nil, fmt.Errorf("unexpected response type")
	}
}

// deliverResponse delivers a response to the waiting request.
func (b *MessageBus) deliverResponse(msg Message) {
	b.pendingMu.RLock()
	ch, ok := b.pending[msg.CorrelationID()]
	b.pendingMu.RUnlock()

	if ok {
		select {
		case ch <- msg:
		default:
			// Channel full, drop message
		}
	}
}

// NewHookExecutionRequest creates a new hook execution request.
func NewHookExecutionRequest(eventName HookEventName, input map[string]any, ctx *EventContext) *HookExecutionRequest {
	return &HookExecutionRequest{
		EventName: eventName,
		Input:     input,
		Context:   ctx,
	}
}

// NewHookExecutionResponse creates a new hook execution response.
func NewHookExecutionResponse(correlationID string, success bool, output *HookResponse, err error) *HookExecutionResponse {
	return &HookExecutionResponse{
		correlationID: correlationID,
		Success:       success,
		Output:        output,
		Error:         err,
	}
}
