package compaction

import (
	"regexp"
	"strings"
)

// CompactionStrategy defines the level of detail in compaction
type CompactionStrategy string

const (
	// StrategyMinimal - Just the summary, no files
	StrategyMinimal CompactionStrategy = "minimal"

	// StrategyStandard - Summary + intelligently selected files (default)
	StrategyStandard CompactionStrategy = "standard"

	// StrategyComprehensive - Summary + more files + more context
	StrategyComprehensive CompactionStrategy = "comprehensive"
)

// SummarizationSystemPrompt is the system prompt for the summarization model.
// Matches Claude Code's simple summarization persona — deliberately minimal
// to avoid biasing the summary with coding-specific instructions.
const SummarizationSystemPrompt = `You are a helpful AI assistant tasked with summarizing conversations.`

// noToolsPreamble is prepended to every compaction prompt.
// Mirrors Claude Code's NO_TOOLS_PREAMBLE: prevents tool calls that would
// waste the model's only turn and produce no text output (	 empty summary).
const noToolsPreamble = `CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.

- Do NOT use Read, Bash, Grep, Glob, Edit, Write, or ANY other tool.
- You already have all the context you need in the conversation above.
- Tool calls will be REJECTED and will waste your only turn — you will fail the task.
- Your entire response must be plain text: an <analysis> block followed by a <summary> block.

`

// noToolsTrailer is appended to every compaction prompt.
// Mirrors Claude Code's NO_TOOLS_TRAILER for belt-and-suspenders enforcement.
const noToolsTrailer = `

REMINDER: Do NOT call any tools. Respond with plain text only — ` +
	`an <analysis> block followed by a <summary> block. ` +
	`Tool calls will be rejected and you will fail the task.`

// detailedAnalysisInstruction mirrors CC's DETAILED_ANALYSIS_INSTRUCTION_BASE.
const detailedAnalysisInstruction = `Before providing your final summary, wrap your analysis in <analysis> tags to organize your thoughts and ensure you've covered all necessary points. In your analysis process:

1. Chronologically analyze each message and section of the conversation. For each section thoroughly identify:
   - The user's explicit requests and intents
   - Your approach to addressing the user's requests
   - Key decisions, technical concepts and code patterns
   - Specific details like:
     - file names
     - full code snippets
     - function signatures
     - file edits
   - Errors that you ran into and how you fixed them
   - Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
2. Double-check for technical accuracy and completeness, addressing each required element thoroughly.`

// baseCompactPrompt is the core summarisation prompt body.
// Mirrors Claude Code's BASE_COMPACT_PROMPT with its 9-section structure and
// <summary>…</summary> wrapper (which formatCompactSummary later extracts).
const baseCompactPrompt = `Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions.
This summary should be thorough in capturing technical details, code patterns, and architectural decisions that would be essential for continuing development work without losing context.

` + detailedAnalysisInstruction + `

Your summary should include the following sections:

1. Primary Request and Intent: Capture all of the user's explicit requests and intents in detail
2. Key Technical Concepts: List all important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Pay special attention to the most recent messages and include full code snippets where applicable and include a summary of why this file read or edit is important.
4. Errors and fixes: List all errors that you ran into, and how you fixed them. Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages that are not tool results. These are critical for understanding the users' feedback and changing intent.
7. Pending Tasks: Outline any pending tasks that you have explicitly been asked to work on.
8. Current Work: Describe in detail precisely what was being worked on immediately before this summary request, paying special attention to the most recent messages from both user and assistant. Include file names and code snippets where applicable.
9. Optional Next Step: List the next step that you will take that is related to the most recent work you were doing. IMPORTANT: ensure that this step is DIRECTLY in line with the user's most recent explicit requests, and the task you were working on immediately before this summary request. If your last task was concluded, then only list next steps if they are explicitly in line with the users request. Do not start on tangential requests or really old requests that were already completed without confirming with the user first.
                       If there is a next step, include direct quotes from the most recent conversation showing exactly what task you were working on and where you left off. This should be verbatim to ensure there's no drift in task interpretation.

Here's an example of how your output should be structured:

<example>
<analysis>
[Your thought process, ensuring all points are covered thoroughly and accurately]
</analysis>

<summary>
1. Primary Request and Intent:
   [Detailed description]

2. Key Technical Concepts:
   - [Concept 1]
   - [Concept 2]
   - [...]

3. Files and Code Sections:
   - [File Name 1]
      - [Summary of why this file is important]
      - [Summary of the changes made to this file, if any]
      - [Important Code Snippet]
   - [File Name 2]
      - [Important Code Snippet]
   - [...]

4. Errors and fixes:
    - [Detailed description of error 1]:
      - [How you fixed the error]
      - [User feedback on the error if any]
    - [...]

5. Problem Solving:
   [Description of solved problems and ongoing troubleshooting]

6. All user messages:
    - [Detailed non tool use user message]
    - [...]

7. Pending Tasks:
   - [Task 1]
   - [Task 2]
   - [...]

8. Current Work:
   [Precise description of current work]

9. Optional Next Step:
   [Optional Next step to take]

</summary>
</example>

Please provide your summary based on the conversation so far, following this structure and ensuring precision and thoroughness in your response.

There may be additional summarization instructions provided in the included context. If so, remember to follow these instructions when creating the above summary. Examples of instructions include:
<example>
## Compact Instructions
When summarizing the conversation focus on typescript code changes and also remember the mistakes you made and how you fixed them.
</example>

<example>
# Summary instructions
When you are using compact - please focus on test output and code changes. Include file reads verbatim.
</example>
`

// CompressionPrompt is the full compaction prompt sent to the summarisation model.
// Mirrors Claude Code's getCompactPrompt():
//   - NO_TOOLS_PREAMBLE at the top prevents wasted tool-call turns
//   - 9-section structure with <summary> wrapper for structured extraction
//   - NO_TOOLS_TRAILER at the bottom reinforces the no-tools constraint
const CompressionPrompt = noToolsPreamble + baseCompactPrompt + noToolsTrailer

// DefaultSummaryMaxTokens is the default max_tokens cap for the summarization LLM call.
// Set to 20000 to allow detailed summaries. Can be overridden via
// CompactionConfig.SummaryMaxTokens for models with larger output windows.
// For models with 200k+ context windows, DefaultSummaryMaxTokensHighContext (30000) is used instead.
const DefaultSummaryMaxTokens = 20000

// DefaultSummaryMaxTokensHighContext is used for models with large context windows (200k+).
// These models can handle more detailed summaries without impacting the remaining context.
const DefaultSummaryMaxTokensHighContext = 30000

// HighContextThreshold is the minimum context window size to use the high-context token limit.
const HighContextThreshold = 200000

// SummaryPrefix is prepended to the summary in the new conversation.
// Matches Claude Code's getCompactUserSummaryMessage() prefix exactly.
const SummaryPrefix = `This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.
`

// MaxUserMessageTokens is the maximum tokens of recent user messages to include
const MaxUserMessageTokens = 20000

// analysisTagRe matches <analysis>…</analysis> blocks (may span newlines).
var analysisTagRe = regexp.MustCompile(`(?s)<analysis>.*?</analysis>`)

// summaryTagRe matches <summary>…</summary> blocks (may span newlines).
var summaryTagRe = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)

// FormatCompactSummary mirrors Claude Code's formatCompactSummary():
//  1. Strip <analysis> scratchpad (chain-of-thought for quality, not output)
//  2. Extract <summary> content and prefix with "Summary:\n"
//  3. Collapse redundant blank lines
//
// If no <summary> tags are present (e.g. model didn't follow the format) the
// raw text is returned after analysis-stripping so we never lose the content.
func FormatCompactSummary(summary string) string {
	// 1. Strip analysis section
	formatted := analysisTagRe.ReplaceAllString(summary, "")

	// 2. Extract and reformat summary section
	if m := summaryTagRe.FindStringSubmatch(formatted); len(m) == 2 {
		content := strings.TrimSpace(m[1])
		formatted = summaryTagRe.ReplaceAllString(formatted, "Summary:\n"+content)
	}

	// 3. Collapse 3+ consecutive blank lines 	 2
	formatted = regexp.MustCompile(`\n{3,}`).ReplaceAllString(formatted, "\n\n")

	return strings.TrimSpace(formatted)
}
