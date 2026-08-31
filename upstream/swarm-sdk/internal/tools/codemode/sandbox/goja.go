// Package sandbox provides JavaScript execution environments for code mode.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja_nodejs/eventloop"
)

// GojaSandbox implements Sandbox using github.com/dop251/goja.
//
// It provides a JavaScript runtime with:
//   - Promise/async-await support via goja_nodejs/eventloop
//   - Persistent global state across Eval calls (REPL semantics)
//   - console.log capture
//   - Last-expression value capture
//   - Timeout/cancellation via runtime.Interrupt
//
// Thread safety: NOT safe for concurrent Eval. One per conversation.
type GojaSandbox struct {
	// mu protects state during Eval.
	mu sync.Mutex

	// closed indicates the sandbox has been shut down.
	closed atomic.Bool

	// printed accumulates console.log output during Eval.
	printed strings.Builder

	// printedMu protects printed from concurrent writes during async execution.
	printedMu sync.Mutex

	// toolStubs is the current set of tool stubs for this Eval.
	toolStubs []ToolStub

	// dispatch is the callback for tool execution.
	dispatch DispatchFn

	// callCounter generates unique call IDs within an Eval.
	callCounter int64

	// toolCalls collects nested call metadata for tracing.
	toolCalls map[string]ToolCallMeta

	// toolReturns collects nested return metadata for tracing.
	toolReturns map[string]ToolReturnMeta

	// parentCallID is the outer run_code call ID for nesting.
	parentCallID string

	// globals holds persisted global variables across Eval calls.
	// Key is the variable name, value is the JSON-exported form.
	globals map[string]any

	// loop is the current event loop (valid only during Eval).
	loop *eventloop.EventLoop

	// vm is the current runtime (valid only during Eval callback).
	vm *goja.Runtime
	// evalCtx is the context from the current Eval call, used to propagate
	// cancellation to in-flight async tool dispatches.
	evalCtx context.Context
}

// captureLastExpr makes a trailing bare expression in user code behave as the
// returned value of the async IIFE wrapper in Eval. Without this, code like
// `({a:1})` evaluates but is discarded (an arrow function body does not return
// its last expression), so run_code reported "(empty)".
//
// It parses the code INSIDE an async function (so top-level `await` — the common
// case for tool calls — parses correctly) and, only when the final top-level
// statement is an ExpressionStatement, splices an assignment to a reserved
// capture variable around that expression in place (preserving the original
// source byte range, including any surrounding parentheses) and appends a
// `return` of it. Code that already ends in return/throw/loop/declaration/block
// is returned verbatim. On any parse error the original code is returned
// unchanged so the normal runtime error path surfaces the real syntax error.
func captureLastExpr(code string) string {
	// Parse within an async function body so `await` is legal at top level.
	const prefix = "async function __cm_wrap__(){\n"
	prog, err := parser.ParseFile(nil, "", prefix+code+"\n}", 0)
	if err != nil || prog == nil || len(prog.Body) == 0 {
		return code
	}
	fd, ok := prog.Body[0].(*ast.FunctionDeclaration)
	if !ok || fd.Function == nil || fd.Function.Body == nil {
		return code
	}
	list := fd.Function.Body.List
	if len(list) == 0 {
		return code
	}
	es, ok := list[len(list)-1].(*ast.ExpressionStatement)
	if !ok || es.Expression == nil {
		return code
	}
	// file.Idx is 1-based over the parsed (prefixed) source; subtract the
	// prefix length to map back into the original code.
	start := int(es.Expression.Idx0()) - 1 - len(prefix)
	end := int(es.Expression.Idx1()) - 1 - len(prefix)
	if start < 0 || end > len(code) || start >= end {
		return code
	}
	const capVar = "__cm_last__"
	expr := code[start:end]
	// Wrap the expression in its own parens so object literals and comma
	// expressions are preserved, assign to the capture var, and return it.
	rewritten := code[:start] + "(" + capVar + " = (" + expr + "))" + code[end:]
	return "let " + capVar + ";\n" + rewritten + "\nreturn " + capVar + ";"
}

// NewGojaSandbox creates a fresh goja-based sandbox.
func NewGojaSandbox() *GojaSandbox {
	return &GojaSandbox{
		toolCalls:   make(map[string]ToolCallMeta),
		toolReturns: make(map[string]ToolReturnMeta),
		globals:     make(map[string]any),
	}
}

// Eval executes JavaScript code in the sandbox.
func (s *GojaSandbox) Eval(ctx context.Context, code string, stubs []ToolStub, dispatch DispatchFn) (EvalResult, error) {
	if s.closed.Load() {
		return EvalResult{}, fmt.Errorf("sandbox closed")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Reset per-Eval state
	s.printed.Reset()
	s.toolStubs = stubs
	s.dispatch = dispatch
	s.evalCtx = ctx
	s.callCounter = 0
	s.toolCalls = make(map[string]ToolCallMeta)
	s.toolReturns = make(map[string]ToolReturnMeta)

	// Channel for receiving the result - must be buffered
	resultCh := make(chan evalOutcome, 1)

	// Create event loop
	s.loop = eventloop.NewEventLoop()

	// Start the event loop - this runs in a background goroutine
	// and keeps processing events until Stop() is called
	s.loop.Start()

	// Schedule code execution on the event loop
	s.loop.RunOnLoop(func(vm *goja.Runtime) {
		s.vm = vm
		s.setupConsole(vm)
		s.setupGlobals(vm)
		s.injectToolFunctions(vm)
		s.restoreGlobals(vm)

		// Wrap code in async IIFE to support top-level await. captureLastExpr
		// rewrites a trailing bare expression (e.g. `({a:1})`) into a returned
		// value so the advertised "last expression is captured" contract holds
		// even without an explicit `return`.
		wrappedCode := fmt.Sprintf(`
(async () => {
%s
})()
`, captureLastExpr(code))

		val, err := vm.RunString(wrappedCode)
		if err != nil {
			resultCh <- evalOutcome{runErr: err, isError: true}
			return
		}

		// Handle Promise result - poll for resolution ON the event loop.
		if promise, ok := val.Export().(*goja.Promise); ok {
			// goja Promises are NOT goroutine-safe, so we must read State()
			// only from the loop thread. Schedule the poll on the loop itself
			// rather than a separate goroutine (which would race the loop as
			// it settles the promise).
			s.pollPromise(promise, resultCh)
		} else {
			// Not a promise - immediate result
			resultCh <- evalOutcome{value: s.exportValue(val)}
		}
	})

	// Wait for result or timeout
	deadline, hasDeadline := ctx.Deadline()
	var timeoutCh <-chan time.Time
	if hasDeadline {
		timeoutCh = time.After(time.Until(deadline))
	} else {
		timeoutCh = time.After(60 * time.Second)
	}

	select {
	case outcome := <-resultCh:
		s.loop.Stop()
		s.saveGlobals(s.vm)
		return s.buildResult(outcome), nil
	case <-timeoutCh:
		s.loop.Stop()
		return EvalResult{
			IsError:      true,
			ErrorMessage: "execution timeout",
			Printed:      strings.TrimSuffix(s.printed.String(), "\n"),
		}, nil
	case <-ctx.Done():
		s.loop.Stop()
		return EvalResult{
			IsError:      true,
			ErrorMessage: ctx.Err().Error(),
			Printed:      strings.TrimSuffix(s.printed.String(), "\n"),
		}, nil
	}
}

// pollPromise waits for a Promise to settle and sends the result.
//
// It schedules itself on the event loop via SetTimeout so that promise.State()
// and promise.Result() are only ever read from the loop goroutine. goja
// Promises are not goroutine-safe, so polling from a separate goroutine (as a
// previous version did) raced the loop as it settled the promise. The overall
// deadline is enforced by the caller's select on ctx/timeout in Eval, and by
// vm.Interrupt on timeout; here we bound the number of re-schedules with a
// wall-clock check so a never-settling promise still terminates.
func (s *GojaSandbox) pollPromise(promise *goja.Promise, resultCh chan<- evalOutcome) {
	deadline := time.Now().Add(55 * time.Second)

	var poll func(vm *goja.Runtime)
	poll = func(vm *goja.Runtime) {
		state := promise.State()
		switch state {
		case goja.PromiseStateFulfilled:
			resultCh <- evalOutcome{value: s.exportValue(promise.Result())}
			return
		case goja.PromiseStateRejected:
			reason := "promise rejected"
			if promise.Result() != nil {
				reason = fmt.Sprintf("%v", s.exportValue(promise.Result()))
			}
			resultCh <- evalOutcome{isError: true, runErr: fmt.Errorf("%s", reason)}
			return
		default:
			if time.Now().After(deadline) {
				resultCh <- evalOutcome{isError: true, runErr: fmt.Errorf("promise resolution timeout")}
				return
			}
			// Re-check on the next loop tick. SetTimeout runs fn on the loop
			// goroutine, so State()/Result() stay loop-confined.
			s.loop.SetTimeout(poll, 5*time.Millisecond)
		}
	}

	// Kick off the first poll on the loop.
	s.loop.RunOnLoop(poll)
}

// evalOutcome captures the result of code execution.
type evalOutcome struct {
	value   any
	runErr  error
	isError bool
}

// buildResult constructs an EvalResult from the execution outcome.
func (s *GojaSandbox) buildResult(outcome evalOutcome) EvalResult {
	result := EvalResult{
		Printed:     strings.TrimSuffix(s.printed.String(), "\n"),
		ToolCalls:   s.toolCalls,
		ToolReturns: s.toolReturns,
	}

	if outcome.isError {
		result.IsError = true
		if outcome.runErr != nil {
			result.ErrorMessage = outcome.runErr.Error()
			if jsErr, ok := outcome.runErr.(*goja.Exception); ok {
				result.StackTrace = jsErr.String()
			}
		}
	} else {
		result.Value = outcome.value
	}

	return result
}

// setupConsole injects a console object that captures logs.
func (s *GojaSandbox) setupConsole(vm *goja.Runtime) {
	console := vm.NewObject()
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		s.logToConsole(call)
		return goja.Undefined()
	})
	_ = console.Set("error", func(call goja.FunctionCall) goja.Value {
		s.logToConsole(call)
		return goja.Undefined()
	})
	_ = console.Set("warn", func(call goja.FunctionCall) goja.Value {
		s.logToConsole(call)
		return goja.Undefined()
	})
	_ = vm.Set("console", console)
}

// logToConsole writes arguments to the printed buffer.
func (s *GojaSandbox) logToConsole(call goja.FunctionCall) {
	s.printedMu.Lock()
	defer s.printedMu.Unlock()
	for i, arg := range call.Arguments {
		if i > 0 {
			s.printed.WriteString(" ")
		}
		s.printed.WriteString(s.valueToString(arg))
	}
	s.printed.WriteString("\n")
}

// valueToString converts a goja value to a string for console output.
// Objects and arrays are JSON-encoded so console.log produces readable
// JavaScript-like output instead of Go's map[key:val] / [a b] syntax.
func (s *GojaSandbox) valueToString(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	exported := v.Export()
	// Strings pass through untouched (this also covers JSON.stringify output).
	if str, ok := exported.(string); ok {
		return str
	}
	// JSON-encode structured values (maps/slices) for readable output.
	switch exported.(type) {
	case map[string]any, []any:
		if b, err := json.Marshal(exported); err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("%v", exported)
}

// setupGlobals injects harmless stdlib-like functions.
func (s *GojaSandbox) setupGlobals(vm *goja.Runtime) {
	// JSON is built into goja

	// Math helpers (subset of Math.* for convenience)
	math := vm.NewObject()
	_ = math.Set("floor", func(x float64) float64 { return float64(int64(x)) })
	_ = math.Set("ceil", func(x float64) float64 { return float64(int64(x + 0.999999)) })
	_ = math.Set("round", func(x float64) float64 { return float64(int64(x + 0.5)) })
	_ = math.Set("abs", func(x float64) float64 {
		if x < 0 {
			return -x
		}
		return x
	})
	_ = math.Set("min", func(args ...float64) float64 {
		if len(args) == 0 {
			return 0
		}
		min := args[0]
		for _, x := range args[1:] {
			if x < min {
				min = x
			}
		}
		return min
	})
	_ = math.Set("max", func(args ...float64) float64 {
		if len(args) == 0 {
			return 0
		}
		max := args[0]
		for _, x := range args[1:] {
			if x > max {
				max = x
			}
		}
		return max
	})
	_ = vm.Set("Math", math)

	// Object.assign helper
	objectObj := vm.NewObject()
	_ = objectObj.Set("assign", func(target, source *goja.Object) *goja.Object {
		for _, key := range source.Keys() {
			_ = target.Set(key, source.Get(key))
		}
		return target
	})
	_ = vm.Set("Object", objectObj)
}

// injectToolFunctions binds tool stubs as callable functions in the VM.
func (s *GojaSandbox) injectToolFunctions(vm *goja.Runtime) {
	for _, stub := range s.toolStubs {
		if stub.IsAsync {
			// Async tool: return a Promise
			_ = vm.Set(stub.Name, s.createAsyncToolFunc(vm, stub.OriginalName))
		} else {
			// Sync tool: call directly
			_ = vm.Set(stub.Name, s.createSyncToolFunc(vm, stub.OriginalName))
		}
	}
}

// createAsyncToolFunc creates an async tool function that returns a Promise.
func (s *GojaSandbox) createAsyncToolFunc(vm *goja.Runtime, toolName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		args := s.extractArgs(call)
		callID := s.nextCallID()

		// Record the call for tracing
		s.toolCalls[callID] = ToolCallMeta{
			ToolName: toolName,
			Args:     args,
			CallID:   callID,
		}

		// Create a promise
		promise, resolve, reject := vm.NewPromise()

		// Schedule the tool call on a goroutine.
		// Use the eval context (stored on the sandbox) so cancellation propagates
		// to in-flight tool calls when the sandbox times out.
		evalCtx := s.evalCtx
		go func() {
			start := time.Now()
			result, err := s.dispatch(evalCtx, toolName, args)
			duration := time.Since(start)

			// Schedule resolution on the event loop
			s.loop.RunOnLoop(func(vm *goja.Runtime) {
				if err != nil {
					s.toolReturns[callID] = ToolReturnMeta{
						ToolName:   toolName,
						CallID:     callID,
						IsError:    true,
						DurationMS: duration.Milliseconds(),
					}
					reject(vm.NewGoError(err))
				} else {
					s.toolReturns[callID] = ToolReturnMeta{
						ToolName:   toolName,
						CallID:     callID,
						IsError:    false,
						DurationMS: duration.Milliseconds(),
					}
					resolve(vm.ToValue(result))
				}
			})
		}()

		return vm.ToValue(promise)
	}
}

// createSyncToolFunc creates a sync tool function that calls dispatch directly.
func (s *GojaSandbox) createSyncToolFunc(vm *goja.Runtime, toolName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		args := s.extractArgs(call)
		callID := s.nextCallID()
		start := time.Now()

		// Record the call
		s.toolCalls[callID] = ToolCallMeta{
			ToolName: toolName,
			Args:     args,
			CallID:   callID,
		}

		result, err := s.dispatch(s.evalCtx, toolName, args)
		duration := time.Since(start)

		// Record the return
		s.toolReturns[callID] = ToolReturnMeta{
			ToolName:   toolName,
			CallID:     callID,
			IsError:    err != nil,
			DurationMS: duration.Milliseconds(),
		}

		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(result)
	}
}

// extractArgs extracts keyword arguments from a function call.
func (s *GojaSandbox) extractArgs(call goja.FunctionCall) map[string]any {
	if len(call.Arguments) == 0 {
		return map[string]any{}
	}
	first := call.Arguments[0]
	if goja.IsUndefined(first) || goja.IsNull(first) {
		return map[string]any{}
	}
	exported := first.Export()
	if m, ok := exported.(map[string]any); ok {
		return m
	}
	if obj, ok := first.(*goja.Object); ok {
		m := make(map[string]any)
		for _, key := range obj.Keys() {
			v := obj.Get(key)
			m[key] = s.exportValue(v)
		}
		return m
	}
	return map[string]any{}
}

// nextCallID generates a unique call ID for tracing.
func (s *GojaSandbox) nextCallID() string {
	id := atomic.AddInt64(&s.callCounter, 1)
	if s.parentCallID != "" {
		return fmt.Sprintf("%s__%d", s.parentCallID, id)
	}
	return fmt.Sprintf("codemode_%d", id)
}

// exportValue safely exports a goja value to a Go value.
func (s *GojaSandbox) exportValue(v goja.Value) any {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return v.Export()
}

// saveGlobals saves user-defined globals for REPL persistence.
func (s *GojaSandbox) saveGlobals(vm *goja.Runtime) {
	// Get all global keys (excluding built-ins we injected)
	builtins := map[string]bool{
		"console": true, "Math": true, "Object": true,
		"JSON": true, "Array": true, "String": true, "Number": true,
		"Boolean": true, "Date": true, "RegExp": true, "Error": true,
		"Promise": true, "Symbol": true, "Map": true, "Set": true,
		"WeakMap": true, "WeakSet": true, "Proxy": true, "Reflect": true,
	}

	for _, key := range vm.GlobalObject().Keys() {
		if builtins[key] {
			continue
		}
		// Also skip tool names
		isTool := false
		for _, stub := range s.toolStubs {
			if stub.Name == key {
				isTool = true
				break
			}
		}
		if isTool {
			continue
		}

		val := vm.GlobalObject().Get(key)
		if val != nil && !goja.IsUndefined(val) {
			s.globals[key] = s.exportValue(val)
		}
	}
}

// restoreGlobals restores previously saved globals into a new VM.
func (s *GojaSandbox) restoreGlobals(vm *goja.Runtime) {
	for key, val := range s.globals {
		_ = vm.Set(key, val)
	}
}

// Restart clears the sandbox's global state.
func (s *GojaSandbox) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed.Load() {
		return fmt.Errorf("sandbox closed")
	}

	s.printed.Reset()
	s.toolCalls = make(map[string]ToolCallMeta)
	s.toolReturns = make(map[string]ToolReturnMeta)
	s.callCounter = 0
	s.globals = make(map[string]any)

	return nil
}

// Close releases the sandbox's resources.
func (s *GojaSandbox) Close() error {
	s.closed.Store(true)
	return nil
}

// SetParentCallID sets the outer run_code call ID for nested tracing.
func (s *GojaSandbox) SetParentCallID(id string) {
	s.parentCallID = id
}
