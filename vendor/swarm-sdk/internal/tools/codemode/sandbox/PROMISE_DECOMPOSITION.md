# Promise Resolution Architecture Decomposition

## ISOLATED TEST FINDINGS

A working isolated test showed:
```
Promise created, state: 1
Poll 0: state=1
Result: 42 (type goja.valueInt)
```

This proves `loop.Start()` + `loop.RunOnLoop()` + polling goroutine DOES work for Promise resolution.

## 1. WHAT I NEED (Preconditions for Promise Resolution)

### Event Loop Initialization
- `eventloop.NewEventLoop()` - creates loop (NO VM parameter needed!)
- `loop.Start()` - starts background goroutine that processes events
- The loop creates its own `goja.Runtime` internally during callbacks

### Working Pattern (from isolated test)
```go
loop := eventloop.NewEventLoop()
loop.Start()
defer loop.Stop()

loop.RunOnLoop(func(vm *goja.Runtime) {
    val, _ := vm.RunString("(async () => { return 42; })()")
    promise := val.Export().(*goja.Promise)
    
    go func() {
        for {
            if promise.State() == goja.PromiseStateFulfilled {
                resultCh <- promise.Result()
                return
            }
            time.Sleep(10 * time.Millisecond)
        }
    }()
})
```

### Key Dependencies
- No VM passed to `NewEventLoop()`
- Callback receives VM as parameter
- Polling goroutine runs independently
- Result channel is buffered

## 2. WHAT I EXPECT (Signals for Promise Resolution)

### Promise States
- `PromiseStatePending` = 1 (observed in test)
- `PromiseStateFulfilled` = 2 (resolved)
- `PromiseStateRejected` = 3 (error)

### Resolution Signal
- `promise.State() == goja.PromiseStateFulfilled`
- `promise.Result()` contains resolved value
- Type: `goja.valueInt` for integers

### Microtask Queue Behavior
- Event loop processes microtasks automatically
- Polling doesn't block - it just checks state
- Resolution happens in parallel with polling

## 3. HOW LOGIC WORKS (Data Flow Trace)

### Working Flow (Isolated Test)
```
1. loop.Start() - background goroutine starts
2. loop.RunOnLoop(fn) - schedules fn on event loop
3. Event loop executes fn:
   a. vm.RunString() creates Promise (state=1)
   b. Starts polling goroutine
   c. fn returns (event loop continues running)
4. Polling goroutine:
   a. Checks promise.State() every 10ms
   b. Event loop processes microtasks in parallel
   c. Promise state changes to fulfilled (state=2)
   d. Polling sees change, sends result
5. Main goroutine receives result from channel
```

### Comparison with sandbox/goja.go

**Working Pattern:**
- `loop.Start()` starts loop
- `RunOnLoop()` schedules code
- Polling goroutine checks state
- Event loop processes in parallel

**Current sandbox/goja.go Pattern:**
- `loop.Start()` starts loop ✓
- `RunOnLoop()` schedules code ✓
- Polling goroutine checks state ✓
- **SAME PATTERN!**

### Why It Should Work
Looking at the code, the pattern IS the same! The issue might be:
1. Test assertions (type comparison)
2. Async tool dispatch timing
3. Context cancellation

## ACTION: Verify Production Code Matches Working Pattern

The sandbox implementation matches the working isolated test pattern. The Promise resolution IS working (test shows `got 42`). The issue is test assertion types, not the Promise architecture.

## NEXT STEPS
1. Fix test type assertions (JS numbers → Go int64/float64)
2. Run full test suite
3. Verify async tools work correctly
