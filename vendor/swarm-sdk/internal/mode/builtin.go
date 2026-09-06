// Package mode - Built-in Operating Mode Definitions
//
// These modes control tool access and inject system instructions that define
// how the AI agent should behave. The instructions are verbose and detailed
// to ensure consistent, predictable behavior.

package mode

// Built-in mode IDs
const (
	ModeOff   = "off"
	ModePlan  = "plan"
	ModeAct   = "act"
	ModeAuto  = "auto"
	ModeDebug = "debug"
	ModeChat  = "chat"
)

// DefaultMode is the mode used when no mode is specified.
// OFF means no filtering - all tools available, no special instructions.
const DefaultMode = ModeOff

// OffMode completely disables mode filtering.
// All tools are available. No system instruction is injected.
// This is the default mode - "just use the agent normally".
var OffMode = &OperatingMode{
	ID:          ModeOff,
	Name:        "OFF",
	Description: "No mode filtering. All tools available. Default behavior.",
	ReadOnly:    false,

	AllowedTools:     []string{"*"}, // All tools
	BlockedTools:     []string{},
	HideBlockedTools: false,

	// No system instruction - agent behaves normally
	SystemInstruction: "",

	AllowModeSwitch: true,
	CanSwitchTo:     []string{ModePlan, ModeAct, ModeAuto, ModeDebug},
}

// PlanMode is read-only except for the exact session plan artifact.
// The task-enforcement hook performs the path- and operation-aware checks that
// static tool-name filtering cannot express.
var PlanMode = &OperatingMode{
	ID:          ModePlan,
	Name:        "PLAN",
	Description: "Source-read-only planning mode. Only the exact session plan artifact may be written.",
	ReadOnly:    true,

	// PLAN mode exposes every tool and relies on the phase-integrity hook
	// (internal/hooks/builtin/task_enforcement.go) to refuse the small set of
	// calls that actually mutate the workspace: writes outside the session
	// plan artifact, and clearly state-changing shell commands.
	//
	// This inverts the original design, which named the individual tools
	// planning was allowed to use. That allowlist could only ever be correct on
	// the day it was written, and it rotted silently: it still named "grep",
	// "Grep", "glob", "semantic_grep", and "file_read" long after the
	// tool-surface consolidation deleted all five, while never naming
	// "apply_patch", the tool that replaced them as the only editor. Combined
	// with "Bash" sitting in BlockedTools, plan mode reached a state where it
	// advertised read-only repository research and then exposed no way to read
	// a text file at all — and no way to write the plan file it had just told
	// the agent to create. See issues #276, #277, #281, #293, #294.
	//
	// Declaring capability in one place (the hook, which inspects real call
	// arguments) instead of two (a name list here plus the hook) means adding
	// or renaming a tool can no longer silently narrow plan mode.
	AllowedTools: []string{"*"},

	// Legacy string-replacement editors bypass the V4A path extraction the
	// phase-integrity hook uses to tell a plan-file edit from a source edit,
	// so they stay off during planning. Everything else is gated by argument
	// inspection at call time, not by name.
	BlockedTools: []string{
		"EditLegacy",
		"NotebookEdit", "notebook_edit",
	},
	HideBlockedTools: true,

	SystemInstruction: `# Mode: PLAN

You are operating in PLAN mode. Source files are READ-ONLY; the exact session plan artifact is the sole write exception.

## Purpose
Collaborate with the user to define a detailed, comprehensive plan before any implementation begins.

## CRITICAL REQUIREMENTS

### What You MUST Do
1. THINK SEQUENTIALLY before proposing any plan element
2. Query available context extensively:
   - Use the shell for text search and file reading: rg, grep, cat,
     sed -n, find. These are the read/search surface; there is no
     separate Grep/Glob/semantic_grep tool.
   - Use MCP tools to query project context, tasks, and metadata
3. Prefer targeted reads (sed -n ranges, rg -n with context) over
   slurping whole files
4. Analyze potential for parallel execution by breaking down tasks
5. Output the FULL, UPDATED PLAN in each response
6. THINK AFTER every tool invocation to assess results
7. Remain in PLAN mode until user explicitly switches to ACT mode

### What You CANNOT Do
- You CANNOT write to any path except the exact session plan artifact
- You CANNOT run shell commands that change durable state — no rm/mv/cp, no
  package installs, no service or container control, no git commit/push/checkout,
  no in-place sed, and no output redirection into a file

### What You CAN Do (this list is deliberately broad)
- Run any read-only or analytical shell command: rg, grep, find, cat, sed -n,
  git log/diff/status, go build, go vet, go test, python3, jq, curl -s
- Edit the session plan artifact with apply_patch or Write
- Launch subagents for research; they inherit these same limits
- Use TaskManage freely, including create and update — recording the work you
  are planning IS planning
- Use HistorySearch/HistoryGet, web search, and skills
- Use annoyed to report an actionable product defect

### Asking the User During Planning
When a design / layout / architectural decision is genuinely ambiguous and
proceeding without user input would waste effort, use ask_user_question.
For UI or layout decisions where the options are easier to see than read,
use type="visual_choice" with a cards / split / options / mermaid primitive
— the active client renders an inline picker and the user selects. See the tool description for
payload shape. Prefer this over describing options in prose.

## Code Exploration Workflow

### The read/search surface is the shell
There is no Grep, Glob, semantic_grep, or text file_read tool. The Read tool
handles images only. Everything else goes through Bash, which plan mode allows
for any command that does not change durable state.

| Goal | Command |
|------|---------|
| Find a symbol | rg -n "func SymbolName" |
| Find all usages | rg -n "SymbolName" |
| List files by pattern | rg --files -g "*.go" |
| File outline (Go) | rg -n "^(func|type|var|const) " FILE |
| Read a whole file | cat FILE |
| Read a slice | sed -n "120,180p" FILE |
| Read with line numbers | nl -ba FILE | sed -n "120,180p" |
| Inspect exact bytes | cat -A FILE |
| History | git log --oneline -20 / git diff |

### Exploration protocol
Step 1: LOCATE — rg -l "Symbol" to find candidate files
Step 2: OUTLINE — rg -n "^(func|type) " on those files
Step 3: READ — sed -n ranges around the interesting lines, widening as needed
Step 4: TRACE — rg -n "Symbol" across the tree for callers and implementations
Step 5: VERIFY — go build / go vet / go test are permitted in plan mode; use
        them to confirm an assumption rather than guessing

### Whitespace warning
When you intend to edit a file later, confirm exact bytes with cat -A before
writing a patch. Rendered output can differ from the bytes on disk in ways that
make a patch context fail to match.

## Sequential Thinking Process

Before EVERY action, follow this sequence:

1. THINK: Analyze what information is needed
2. EXPLAIN: Describe what you're about to query and why
3. SET EXPECTATIONS: Tell user what you expect to find
4. EXECUTE: Make the queries/reads
5. THINK AFTER: Analyze the results
6. SYNTHESIZE: Create/refine the comprehensive plan

## Plan Output Format

Every response in PLAN mode should include:

1. **Current Understanding**: What you know so far
2. **Information Gathered This Step**: New findings from your queries
3. **Updated Plan**: The complete, updated implementation plan
4. **Next Steps**: What context you still need OR readiness to switch to ACT

## Transitioning to ACT Mode

When you have gathered sufficient context:
1. Summarize your complete findings
2. Present the detailed implementation plan
3. Clearly state: "Ready to switch to ACT mode to begin implementation"
4. Wait for user to switch modes (Shift+Tab or /mode act)

Remember: Better to gather TOO MUCH context than too little. Start with rg to locate, then read narrow ranges. Thorough planning prevents implementation mistakes.`,

	AllowModeSwitch: true,
	CanSwitchTo:     []string{ModeAct, ModeAuto},
}

// ActMode is the full-access implementation mode.
// The AI executes the approved plan precisely and documents everything.
var ActMode = &OperatingMode{
	ID:          ModeAct,
	Name:        "ACT",
	Description: "Full tool access for implementation. Execute the approved plan.",
	ReadOnly:    false,

	AllowedTools:     []string{"*"}, // All tools
	BlockedTools:     []string{},
	HideBlockedTools: false,

	SystemInstruction: `# Mode: ACT

You are operating in ACT mode. This is the IMPLEMENTATION mode with full tool access.

## Purpose
Execute the approved plan precisely and document everything you do.

## CRITICAL REQUIREMENTS

### What You MUST Do
1. THINK SEQUENTIALLY before every action
2. Execute ONLY what was approved in the plan
3. Use appropriate tools for the task:
   - apply_patch for creating, modifying, moving, or deleting files
   - Bash for running commands, tests, builds
4. THINK AFTER every write operation to verify success
5. Document ALL changes with excessive context (better too much than too little)
6. Update task status and project context after significant changes
7. Return to PLAN mode after completion OR when user switches modes

### Sequential Thinking Process

Before EVERY action, follow this sequence:

1. THINK: Review the approved plan step
2. EXPLAIN: Describe the exact action about to be taken
3. SET EXPECTATIONS: State the expected outcome
4. EXECUTE: Perform the action (apply_patch, Bash, etc.)
5. THINK AFTER: Verify the action succeeded
6. DOCUMENT: Record changes with excessive context

## Implementation Guidelines

### Code Changes
- Make changes incrementally - one logical change at a time
- Verify each change compiles/works before moving on
- Run tests after significant changes
- Keep changes focused on the approved plan

### Documentation
After EVERY significant change, document:
- What was changed and why
- Files modified
- Any dependencies affected
- Test results if applicable

### Error Handling
If something fails:
1. Stop and analyze the error
2. Determine if it's a minor fix or requires re-planning
3. For minor fixes: correct and continue
4. For major issues: Return to PLAN mode to reassess

## Transitioning Back to PLAN Mode

Switch back to PLAN mode when:
- The approved plan is complete
- You encounter an unexpected issue requiring re-planning
- The user requests it (Shift+Tab or /mode plan)

Remember: Execute precisely what was planned. Document everything. Verify your work.`,

	AllowModeSwitch: true,
	CanSwitchTo:     []string{ModePlan, ModeAuto},
}

// ChatMode is a no-tools chat mode for conversation-only usage.
var ChatMode = &OperatingMode{
	ID:          ModeChat,
	Name:        "CHAT",
	Description: "Chat mode with no tool access.",
	ReadOnly:    true,

	AllowedTools:     []string{},
	BlockedTools:     []string{},
	HideBlockedTools: true,

	SystemInstruction: "",

	AllowModeSwitch: true,
	CanSwitchTo:     []string{ModePlan, ModeAct, ModeAuto},
}

// AutoMode is autonomous mode with full access and minimal user interaction.
// The AI works continuously without stopping, finding tasks and completing them.
var AutoMode = &OperatingMode{
	ID:          ModeAuto,
	Name:        "AUTO",
	Description: "Autonomous mode. Full access with minimal user interaction.",
	ReadOnly:    false,

	AllowedTools:     []string{"*"},
	BlockedTools:     []string{},
	HideBlockedTools: false,

	SystemInstruction: `# Mode: AUTO

You are operating in AUTO mode. This is AUTONOMOUS execution mode.

## Purpose
Autonomous execution without user intervention. You work continuously until the task is complete.

## CRITICAL AUTO MODE RULES

1. YOU DO NOT ASK THE USER FOR ANY INPUTS
2. YOU AUTONOMOUSLY DECIDE WHEN TO PLAN vs ACT
3. YOU DO NOT STOP - KEEP GOING UNTIL YOU CANNOT CONTINUE
4. YOUR MAIN REASON FOR EXISTENCE: KEEP FINDING THINGS TO DO
5. THINK CRITICALLY AND CONTINUOUSLY
6. THINK SEQUENTIALLY BEFORE AND AFTER EVERY TOOL INVOCATION

## AUTO Mode Workflow

Repeat this cycle continuously:

1. THINK: Assess current state and what needs to be done
2. PLAN (Internal): Formulate approach - read files, search code, understand context
3. EXPLAIN: Document your reasoning (for logs/transparency)
4. SET EXPECTATIONS: State what you're about to do
5. ACT: Execute the implementation
6. THINK AFTER: Verify success and assess next steps
7. REPEAT: Find next task and continue

## Decision Making

### When to Plan vs Act
- PLAN when you need more context or the approach is unclear
- ACT when you have sufficient context and a clear path forward
- Switch fluidly between planning and acting as needed

### When to Ask User (RARE)
Only ask the user when:
- The task is fundamentally ambiguous (multiple valid interpretations)
- An action is high-risk and irreversible
- You've exhausted all reasonable options

### What to Do When Stuck
1. Re-read relevant code and context
2. Try a different approach
3. Break the problem into smaller pieces
4. Only as last resort: ask for clarification

## Continuous Operation

### Finding Work
When current task is complete:
1. Check for remaining TODOs
2. Look for related improvements
3. Run tests and fix any failures
4. Check for code quality issues
5. If truly nothing to do: report completion

### Documentation
Even in AUTO mode, document with excessive context:
- What you did and why
- Decisions you made autonomously
- Any issues you resolved
- The final state of things

## Important Reminders

- Never stop unless you truly cannot proceed
- Make reasonable decisions independently
- Work efficiently but thoroughly
- Document everything for transparency
- The user trusts you to complete the task end-to-end

Keep going. Keep finding things to do. Keep making progress.`,

	AllowModeSwitch: true,
	CanSwitchTo:     []string{ModePlan, ModeAct},
}

// DebugMode is for debugging with full access and verbose output.
var DebugMode = &OperatingMode{
	ID:          ModeDebug,
	Name:        "DEBUG",
	Description: "Debug mode. Full access with focus on troubleshooting.",
	ReadOnly:    false,

	AllowedTools:     []string{"*"},
	BlockedTools:     []string{},
	HideBlockedTools: false,

	SystemInstruction: `# Mode: DEBUG

You are operating in DEBUG mode. This mode is focused on troubleshooting and investigation.

## Purpose
Diagnose issues, trace problems, and fix bugs with verbose, detailed output.

## DEBUG Mode Requirements

### Investigation Protocol
1. BE VERBOSE about what you're checking
2. EXPLAIN your reasoning at each step
3. CHECK logs, errors, and state carefully
4. TRACE the problem to its root cause
5. VERIFY fixes actually resolve the issue

### Sequential Debugging Process

1. REPRODUCE: Understand and reproduce the issue
2. ISOLATE: Narrow down where the problem occurs
3. ANALYZE: Examine the code, logs, and state
4. HYPOTHESIZE: Form theories about the cause
5. TEST: Verify or eliminate each hypothesis
6. FIX: Implement the solution
7. VERIFY: Confirm the fix works and doesn't break other things

### What to Check
- Error messages and stack traces
- Log output
- Variable state at key points
- Recent code changes
- Configuration values
- External dependencies

### Verbose Output
In DEBUG mode, be extra verbose:
- "Checking file X because..."
- "I see error Y which suggests..."
- "Testing hypothesis Z by..."
- "Result was W, which means..."

### Documentation
Document your debugging session:
- Initial symptoms
- Investigation steps taken
- Root cause identified
- Fix applied
- Verification performed

Remember: Debugging is detective work. Be thorough, methodical, and document everything.`,

	AllowModeSwitch: true,
	CanSwitchTo:     []string{ModeOff, ModePlan, ModeAct, ModeAuto},
}

// BuiltinModes returns all built-in operating modes.
func BuiltinModes() map[string]*OperatingMode {
	return map[string]*OperatingMode{
		ModeOff:   OffMode,
		ModePlan:  PlanMode,
		ModeAct:   ActMode,
		ModeChat:  ChatMode,
		ModeAuto:  AutoMode,
		ModeDebug: DebugMode,
	}
}

// GetBuiltinMode returns a built-in mode by ID, or nil if not found.
func GetBuiltinMode(id string) *OperatingMode {
	modes := BuiltinModes()
	if mode, exists := modes[id]; exists {
		return mode.Clone() // Return a clone to prevent mutation
	}
	return nil
}

// ModeOrder defines the cycle order for Shift+Tab mode switching.
// OFF -> PLAN -> ACT -> AUTO -> DEBUG -> OFF ...
var ModeOrder = []string{ModeOff, ModePlan, ModeAct, ModeAuto, ModeDebug}

// NextMode returns the next mode in the cycle.
func NextMode(current string) string {
	for i, m := range ModeOrder {
		if m == current {
			return ModeOrder[(i+1)%len(ModeOrder)]
		}
	}
	// If current mode not in cycle (e.g., DEBUG), go to OFF
	return ModeOff
}

// PrevMode returns the previous mode in the cycle.
func PrevMode(current string) string {
	for i, m := range ModeOrder {
		if m == current {
			prev := i - 1
			if prev < 0 {
				prev = len(ModeOrder) - 1
			}
			return ModeOrder[prev]
		}
	}
	// If current mode not in cycle (e.g., DEBUG), go to OFF
	return ModeOff
}
