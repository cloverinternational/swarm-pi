// Package testpkg contains example violations for the swarmlint analyzer.
// This file is used for integration testing.
package testpkg

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// =============================================================================
// Example 1: Direct OrderedBlocks Access (VIOLATION)
// =============================================================================

// BadDirectAccess directly accesses OrderedBlocks - this is a violation.
func BadDirectAccess(msg *conversation.Message) []conversation.MessageBlock {
	// This will trigger: CONTRACT VIOLATION: Use GetOrderedBlocks()
	return msg.OrderedBlocks // want "CONTRACT VIOLATION.*GetOrderedBlocks"
}

// =============================================================================
// Example 2: Correct OrderedBlocks Access (NO VIOLATION)
// =============================================================================

// GoodAccess uses the correct method to access blocks.
func GoodAccess(msg *conversation.Message) []conversation.MessageBlock {
	// This is correct - uses the accessor method
	return msg.GetOrderedBlocks()
}

// =============================================================================
// Example 3: Direct Message Construction with OrderedBlocks (VIOLATION)
// =============================================================================

// BadMessageConstruction creates Message directly - this is a violation.
func BadMessageConstruction() *conversation.Message {
	// This will trigger: CONTRACT VIOLATION: Use NewMessage() factory
	return &conversation.Message{
		Role:    "user",
		Content: "hello",
		OrderedBlocks: []conversation.MessageBlock{ // want "CONTRACT VIOLATION.*factory"
			{Sequence: 5, Content: "second"},
			{Sequence: 1, Content: "first"},
		},
	}
}

// =============================================================================
// Example 4: Message Construction Without OrderedBlocks (OK)
// =============================================================================

// OKMessageConstruction creates Message without OrderedBlocks.
// This is allowed because the blocks are empty.
func OKMessageConstruction() *conversation.Message {
	return &conversation.Message{
		Role:    "user",
		Content: "hello",
	}
}

// =============================================================================
// Example 5: Pointer Receiver Access (VIOLATION)
// =============================================================================

// BadPointerAccess uses pointer receiver to access OrderedBlocks.
func BadPointerAccess() {
	msg := new(conversation.Message)
	// This will trigger: CONTRACT VIOLATION
	_ = msg.OrderedBlocks // want "CONTRACT VIOLATION.*GetOrderedBlocks"
}

// =============================================================================
// Example 6: Nested Access (VIOLATION)
// =============================================================================

// BadNestedAccess accesses OrderedBlocks through a slice.
func BadNestedAccess(messages []*conversation.Message) {
	for _, msg := range messages {
		// This will trigger: CONTRACT VIOLATION
		_ = msg.OrderedBlocks // want "CONTRACT VIOLATION.*GetOrderedBlocks"
	}
}

// =============================================================================
// Example 7: Conditional Access (VIOLATION)
// =============================================================================

// BadConditionalAccess conditionally accesses OrderedBlocks.
func BadConditionalAccess(msg *conversation.Message, useBlocks bool) {
	if useBlocks {
		// This will trigger: CONTRACT VIOLATION
		blocks := msg.OrderedBlocks // want "CONTRACT VIOLATION.*GetOrderedBlocks"
		fmt.Println(len(blocks))
	}
}

// =============================================================================
// Example 8: Correct Usage Pattern
// =============================================================================

// ProcessMessageCorrectly demonstrates the correct pattern.
func ProcessMessageCorrectly(msg *conversation.Message) {
	// CORRECT: Use GetOrderedBlocks() for reading
	blocks := msg.GetOrderedBlocks()

	// Process blocks in correct order
	for _, block := range blocks {
		fmt.Printf("Block %d: %s\n", block.Sequence, block.Content)
	}
}

// =============================================================================
// Example 9: Assignment is Allowed
// =============================================================================

// AssignmentToOrderedBlocks assigns to the field.
// This is allowed because it's construction/serialization context.
func AssignmentToOrderedBlocks() *conversation.Message {
	msg := &conversation.Message{
		Role:    "assistant",
		Content: "response",
	}

	// Assignment is allowed (we're constructing)
	msg.OrderedBlocks = []conversation.MessageBlock{
		{Sequence: 1, Content: "first"},
		{Sequence: 2, Content: "second"},
	}

	return msg
}

// =============================================================================
// Example 10: Helper Function with Message Parameter
// =============================================================================

// HelperFunction processes a message.
// CONTRACT: Message must be non-nil.
func HelperFunction(msg *conversation.Message) error {
	// Using the correct accessor
	blocks := msg.GetOrderedBlocks()

	// Process blocks
	for i, block := range blocks {
		if block.Sequence != i+1 {
			return fmt.Errorf("block sequence mismatch")
		}
	}

	return nil
}

// =============================================================================
// Example 11: Multiple Violations in One Function
// =============================================================================

// BadMultipleViolations has multiple contract violations.
func BadMultipleViolations(msg1, msg2 *conversation.Message) {
	// VIOLATION 1
	_ = msg1.OrderedBlocks // want "CONTRACT VIOLATION.*GetOrderedBlocks"

	// VIOLATION 2
	_ = msg2.OrderedBlocks // want "CONTRACT VIOLATION.*GetOrderedBlocks"

	// CORRECT
	_ = msg1.GetOrderedBlocks()
	_ = msg2.GetOrderedBlocks()
}

// =============================================================================
// Example 12: Correct Builder Pattern
// =============================================================================

// UsingBuilderPattern demonstrates correct construction.
// Note: This is a conceptual example - actual builder would be in conversation package.
func UsingBuilderPattern() *conversation.Message {
	// Ideally use conversation.NewMessageBuilder() or similar
	msg := &conversation.Message{
		Role:    "user",
		Content: "hello",
	}

	// Set blocks through proper method
	// msg.AddBlock(conversation.MessageBlock{...})

	return msg
}
