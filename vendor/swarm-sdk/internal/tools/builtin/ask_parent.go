package builtin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// DelegateQuestion holds a question from a delegate waiting for a parent answer.
type DelegateQuestion struct {
	ID       string
	Question string
	Context  string
	AskedAt  time.Time
	replyCh  chan string // delegate goroutine blocks on this
}

// DelegateQuestionChannel is shared state between the delegate agent and the
// parent's DelegateOutputTool. It holds the currently pending question (if any)
// and serialises ask→answer exchanges.
//
// Only one question can be in-flight at a time — sequential by design.
type DelegateQuestionChannel struct {
	mu      sync.Mutex
	pending *DelegateQuestion
}

// Ask blocks the calling goroutine (inside the delegate agent) until the parent
// calls Answer() or the context is cancelled. Returns the parent's answer.
func (c *DelegateQuestionChannel) Ask(ctx context.Context, questionID, question, questionCtx string) (string, error) {
	replyCh := make(chan string, 1)
	q := &DelegateQuestion{
		ID:       questionID,
		Question: question,
		Context:  questionCtx,
		AskedAt:  time.Now(),
		replyCh:  replyCh,
	}

	c.mu.Lock()
	// If there's already a pending question, wait for it to be cleared.
	// This serialises concurrent ask calls (shouldn't happen in practice
	// since the delegate is single-threaded).
	for c.pending != nil {
		existing := c.pending
		c.mu.Unlock()
		select {
		case <-existing.replyCh:
			// previous question was answered; re-acquire lock and try again
		case <-ctx.Done():
			return "", ctx.Err()
		}
		c.mu.Lock()
	}
	c.pending = q
	c.mu.Unlock()

	// Wait for parent to answer (or context to cancel)
	select {
	case answer, ok := <-replyCh:
		if !ok {
			return "", fmt.Errorf("delegate question channel closed without answer")
		}
		return answer, nil
	case <-ctx.Done():
		// Remove pending question so parent doesn't try to answer a cancelled one
		c.mu.Lock()
		if c.pending != nil && c.pending.ID == questionID {
			c.pending = nil
		}
		c.mu.Unlock()
		return "", fmt.Errorf("ask_parent timed out: parent did not answer within deadline")
	}
}

// PendingQuestion returns the current pending question (nil if none).
// Used by DelegateOutputTool when parent polls for questions.
func (c *DelegateQuestionChannel) PendingQuestion() *DelegateQuestion {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending
}

// Answer delivers the parent's answer to the waiting delegate goroutine.
// Returns false if no matching question is pending (idempotent).
func (c *DelegateQuestionChannel) Answer(questionID, answer string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil || c.pending.ID != questionID {
		return false
	}
	c.pending.replyCh <- answer
	c.pending = nil
	return true
}

// AnswerAny delivers an answer to whatever question is currently pending,
// regardless of ID. Useful when the parent doesn't track question IDs.
func (c *DelegateQuestionChannel) AnswerAny(answer string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		return false
	}
	c.pending.replyCh <- answer
	c.pending = nil
	return true
}

// ───────────────────────────────────────────────────────────────────────────
// AskParentTool — injected into delegate agents only, never registered globally
// ───────────────────────────────────────────────────────────────────────────

// AskParentParams are the typed parameters for the ask_parent tool.
type AskParentParams struct {
	Question string `json:"question" description:"The question to ask the parent agent. Be specific — the parent cannot see your conversation context." required:"true"`
	Context  string `json:"context,omitempty" description:"Why you need this information. Helps the parent give a precise answer."`
}

// AskParentTool allows a delegate agent to ask its parent agent a question.
// It is NOT registered in the global tool registry — only injected into delegates.
type AskParentTool struct {
	tools.BaseTool
	questionID string
	channel    *DelegateQuestionChannel
}

// newAskParentTool creates an injected ask_parent tool backed by the given channel.
func newAskParentTool(questionID string, ch *DelegateQuestionChannel) tools.Tool {
	t := &AskParentTool{
		questionID: questionID,
		channel:    ch,
	}
	return tools.Typed[AskParentParams](t)
}

func (t *AskParentTool) Name() string { return "ask_parent" }

func (t *AskParentTool) Description() string {
	return `Ask your parent agent a question and wait for their answer.

Use this when you encounter ambiguity that you cannot resolve on your own:
- Which of these options should I choose?
- Is this the correct interpretation of the requirement?
- Do you want me to proceed with X or Y?

The parent agent will see your question and provide an answer.
You will be blocked until the answer arrives (up to 5 minutes).

ONLY use this when truly necessary — prefer making reasonable decisions
independently. Reserve ask_parent for genuine decision points where
guessing wrong would waste significant work.`
}

func (t *AskParentTool) Parameters() any {
	return tools.SchemaFor[AskParentParams]()
}

func (t *AskParentTool) IsIdempotent() bool                     { return false }
func (t *AskParentTool) RequiresPermission() []tools.Permission { return nil }
func (t *AskParentTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Run blocks the delegate until the parent answers (via DelegateOutput).
func (t *AskParentTool) Run(ctx context.Context, p AskParentParams) (*tools.ToolResult, error) {
	if p.Question == "" {
		return &tools.ToolResult{
			Output:  "Error: question cannot be empty",
			IsError: true,
		}, nil
	}

	// Add a 5-minute deadline so the delegate can't block forever
	askCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	answer, err := t.channel.Ask(askCtx, t.questionID, p.Question, p.Context)
	if err != nil {
		return &tools.ToolResult{
			Output:  fmt.Sprintf("[ask_parent] No answer received: %v\nProceeding with your best judgment.", err),
			IsError: false, // don't hard-fail — let the delegate decide what to do
		}, nil
	}

	return &tools.ToolResult{
		Output: fmt.Sprintf("[Parent's answer]\n%s", answer),
	}, nil
}
