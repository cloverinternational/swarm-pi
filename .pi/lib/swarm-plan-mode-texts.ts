/**
 * GENERATED from upstream/swarm-sdk — do not edit by hand.
 *   internal/hooks/builtin/plan_mode_first_tool.go  ProblemBreakdownPrompt
 *   internal/hooks/builtin/simulation.go            SimulationReminderMessage
 * Regenerate: node tools/parity/gen-plan-texts.mjs
 */
export const PROBLEM_BREAKDOWN_PROMPT = `[PLAN MODE — PROBLEM BREAKDOWN REQUIRED]

═══════════════════════════════════════════════════════════════════════════════
          BEFORE YOU EXECUTE ANY TOOL, BREAK DOWN THE PROBLEM
═══════════════════════════════════════════════════════════════════════════════

You are in PLAN MODE. Before working on the problem, break it down almost as if
it was a beginning to CS intro course where you learn how to break down problems
based on what you need, what you expect and how logic works at the lowest level.

Understand and plan the logic and things needed at the most low level for things
to work.

─────────────────────────────────────────────────────────────────────────────────
                        STRUCTURED BREAKDOWN TEMPLATE
─────────────────────────────────────────────────────────────────────────────────

For each component of the problem, document:

┌─────────────────────────────────────────────────────────────────────────────┐
│  WHAT I NEED                                                                 │
│  ────────────                                                                │
│  • List every input, dependency, precondition required                      │
│  • What must exist before this can work?                                    │
│  • What state must be initialized?                                          │
│                                                                              │
│  WHAT I EXPECT                                                               │
│  ─────────────                                                               │
│  • Define the expected output for each step                                 │
│  • What does success look like at each level?                               │
│  • What are the failure modes and how to detect them?                       │
│                                                                              │
│  HOW LOGIC WORKS (LOWEST LEVEL)                                              │
│  ──────────────────────────────                                              │
│  • Trace the data flow step-by-step                                         │
│  • Identify every transformation and its invariants                         │
│  • What assumptions does each step make?                                    │
│  • Where could the chain break?                                             │
└─────────────────────────────────────────────────────────────────────────────┘

─────────────────────────────────────────────────────────────────────────────────
                              EXAMPLE BREAKDOWN
─────────────────────────────────────────────────────────────────────────────────

Problem: "Add user authentication to the API"

WHAT I NEED:
  • User database table with hashed passwords
  • Session management mechanism
  • Token generation/verification system
  • Protected route middleware

WHAT I EXPECT:
  • POST /login returns token on valid credentials
  • GET /profile returns 401 without valid token
  • Token expires after configured duration
  • Invalid credentials return 401 (not 500)

HOW LOGIC WORKS (LOWEST LEVEL):
  1. User submits credentials → server receives raw email/password
  2. Server looks up email in database → if not found, return 401
  3. Server hashes submitted password → compare with stored hash
  4. If match → generate JWT with user_id + expiration
  5. Token signed with secret → client stores in cookie/header
  6. Protected routes: extract token → verify signature → decode user_id
  7. Load user from DB → attach to request → continue to handler

  BREAKING POINTS:
  • Database connection fails → need retry/fallback
  • Hash comparison timing attack → use constant-time compare
  • Token on stolen device → need refresh token rotation
  • Secret compromised → need key rotation mechanism

─────────────────────────────────────────────────────────────────────────────────
                                WHY THIS MATTERS
─────────────────────────────────────────────────────────────────────────────────

  Agents who skip decomposition:
    • Miss hidden dependencies until they fail
    • Create incomplete solutions that need multiple fix iterations
    • Don't anticipate edge cases until users hit them
    • Produce unmaintainable code that others can't understand

  Agents who decompose first:
    • See the full picture before writing code
    • Identify blockers before hitting them
    • Create complete, tested, documented solutions
    • Write code that's self-documenting through clear structure

─────────────────────────────────────────────────────────────────────────────────
                         DECISION TREE (DEPENDENCY MAP)
─────────────────────────────────────────────────────────────────────────────────

After decomposing the problem, identify every decision that must be made.
Arrange them as a tree where downstream decisions depend on upstream ones.

For EACH decision:
┌─────────────────────────────────────────────────────────────────────────────┐
│  DECISION: <what needs to be decided>                                       │
│  DEPENDS ON: <upstream decisions that must be resolved first>               │
│  CAN RESOLVE FROM CODEBASE: <yes/no — if yes, explore instead of asking>   │
│  RECOMMENDED ANSWER: <your best guess based on codebase exploration>       │
│  STATUS: unresolved / resolved                                              │
└─────────────────────────────────────────────────────────────────────────────┘

Walk the tree depth-first. Resolve upstream decisions before downstream ones.

─────────────────────────────────────────────────────────────────────────────────
                         INTERROGATION PROTOCOL
─────────────────────────────────────────────────────────────────────────────────

For each unresolved decision that CANNOT be answered from the codebase:

1. Formulate ONE focused question with your recommended answer
2. Use ask_user_question to present it
3. Wait for user response (approve / modify / reject your recommendation)
4. Mark the decision as resolved
5. Move to the next decision in dependency order

Do NOT write the plan until all critical decisions reach "resolved" status.

═══════════════════════════════════════════════════════════════════════════════
                           TOOL AUTHORIZATION
═══════════════════════════════════════════════════════════════════════════════

Plan mode is an approval ceremony, not a permission boundary. It neither grants
nor removes tool access. Normal workspace, task, credential, permission, and
safety controls apply exactly as they do outside plan mode.

Use ask_user_question for decisions that genuinely require user judgment. For
UI, layout, design, or architectural trade-offs, prefer type="visual_choice".

═══════════════════════════════════════════════════════════════════════════════

Now proceed with your research, keeping this breakdown framework in mind.
Document your decomposition before executing. This is the foundation of
systematic problem solving.`;

export const SIMULATION_REMINDER_MESSAGE = `[PRE-EXECUTION SIMULATION - CHOREOGRAPH YOUR DANCE]

═══════════════════════════════════════════════════════════════════════════════
           BEFORE YOU PERFORM, REHEARSE THE DANCE IN YOUR MIND
═══════════════════════════════════════════════════════════════════════════════

Before executing your plan, CHOREOGRAPH your actions step-by-step.
Like a dress rehearsal before the play - walk through the sequence to catch issues.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 1: CHOREOGRAPH THE SEQUENCE
─────────────────────────────────────────────────────────────────────────────────

Walk through your actions IN ORDER, like steps in a dance:

   "First, I'll read file X to understand the current structure..."
   "Then, I'll modify function Y because it needs to handle Z..."
   "After that, I'll update the tests in file A to cover the new behavior..."
   "Finally, I'll run the build to verify everything compiles..."

Show the DEPENDENCIES between steps:
   "Step 2 depends on what I find in Step 1 - if X has pattern P, I do Q"
   "Step 3 can only happen after Step 2 succeeds"

This is the CHOREOGRAPHY - the planned sequence of movements.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 2: SPOT THE BREAKING POINTS
─────────────────────────────────────────────────────────────────────────────────

Where could your dance fall apart? Look for:

   • Steps that depend on assumptions: "I assume X exists" → verify first
   • Steps that could fail: "If Y doesn't have Z, this breaks"
   • Steps that affect others: "Changing A might break B"
   • Missing steps: "Did I forget to test the edge case?"

List EVERY potential breaking point. Hidden problems become bugs.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 3: UNDERSTAND THE RIPPLES
─────────────────────────────────────────────────────────────────────────────────

For each change, trace the IMPACT:

   "If I modify function X, what calls it?"
   "If I change file Y, what imports it?"
   "If I update struct Z, what uses it?"

Changes don't happen in isolation - every step creates ripples.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 4: REHEARSE OUT LOUD
─────────────────────────────────────────────────────────────────────────────────

SAY your choreography out loud - don't hide it:

   "Here's my sequence:
    1. [action] → because [reason] → expect [outcome]
    2. [action] → because [reason] → expect [outcome]
    3. [action] → because [reason] → expect [outcome]"

This is your DRESS REHEARSAL. Do it in the open so you can spot mistakes.

═══════════════════════════════════════════════════════════════════════════════
                              WHY THIS MATTERS
═══════════════════════════════════════════════════════════════════════════════

You wouldn't perform a dance without rehearsing.
You wouldn't stage a play without a dress rehearsal.
Don't execute code changes without choreographing first.

                              EVERY SKIPPED REHEARSAL IS A BUG WAITING ON STAGE

═══════════════════════════════════════════════════════════════════════════════

After completing ALL FOUR STEPS above, you may execute.
Skip ANY step = you are NOT ready. Go back and rehearse.`;
