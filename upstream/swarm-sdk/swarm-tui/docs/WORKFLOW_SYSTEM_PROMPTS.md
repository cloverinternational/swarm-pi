# Workflow Agent System Prompts: Best Practices for Completion Signals

## Overview

When creating workflow agents, your **system prompt** is critical for ensuring agents properly signal work completion. This guide explains best practices for system prompts in workflow contexts.

## The Completion Signal Contract

The completion signal system works on this principle:

**Agent System Prompt → Encourages Clear Final Summary → Agent Returns Output → SDK Sets `IsWorkComplete = true` → Workflow Advances**

## Rule #1: Always Demand a Clear Final Summary

Your system prompt should **explicitly require** agents to provide a clear, final summary at the end of their work.

### ❌ Poor System Prompt (doesn't encourage completion)

```
You are a research assistant. Your job is to analyze information.
Please provide your findings.
```

**Problem:** Ambiguous about what "final" means. Agent might keep iterating or produce unclear output.

### ✅ Good System Prompt (encourages completion)

```
You are a research assistant. Analyze the provided information thoroughly.

IMPORTANT: At the END of your response, provide a clear 2-3 sentence FINAL SUMMARY
that captures your key findings. This summary marks your work as complete.

Format:
1. Detailed analysis
2. [blank line]
3. FINAL SUMMARY:
   - [Key finding 1]
   - [Key finding 2]
   - [Your recommendation]
```

**Why it works:**
- Explicitly requests "final summary"
- Clear formatting makes it obvious when work is done
- Uses "marks your work as complete" to signal the semantics

## Rule #2: Use Clear Section Markers

Help the system and downstream readers understand the workflow:

```
You are a financial analyst. Analyze the following investment opportunity.

Structure your response as follows:

## ANALYSIS SECTION
[Your detailed analysis here]

## EVALUATION SECTION
[Your evaluation and scoring]

## FINAL RECOMMENDATION
[Clear, actionable recommendation]

IMPORTANT: The FINAL RECOMMENDATION section marks the completion of your analysis.
Every response must conclude with this section.
```

## Rule #3: Avoid Open-Ended Tasks Without Clear Exit Criteria

### ❌ Bad (No clear completion point)

```
System: Keep analyzing and researching until you understand everything about this topic.
```

**Problem:** Agent doesn't know when to stop. Might timeout or keep iterating endlessly.

### ✅ Good (Clear completion criteria)

```
System: Research and analyze the market trends for cloud computing.
Provide an analysis covering:
1. Current market size
2. Growth trajectory
3. Key players
4. Risk factors
5. Future outlook

Conclude with your INVESTMENT THESIS in 1 paragraph.
Your investment thesis marks the end of your research.
```

## Rule #4: Be Explicit About Multi-Agent Coordination

When agents are part of a workflow group, make it clear they're in a collaborative context:

```
You are Agent 1 of a 3-agent analysis team.
- Agent 1 (you): Market Analysis
- Agent 2: Technical Analysis
- Agent 3: Risk Assessment

Your task: Provide market analysis.

IMPORTANT: Your output will be passed to other agents. Be clear and concise.
End with MARKET ANALYSIS SUMMARY: [your summary in 2-3 sentences]

This summary signals that your work is complete and ready for the next agent.
```

## Rule #5: Specify Output Format Precisely

When you know downstream systems will parse agent output, be explicit:

```
System: You are a data analyst. Analyze the provided dataset.

OUTPUT FORMAT (REQUIRED):
- Data Quality: [1-5 scale]
- Key Statistics: [specific numbers]
- Anomalies Found: [list]
- Recommendation: [one sentence]

Your response must follow this format exactly.
The Recommendation line marks work completion.
```

## Rule #6: Set Clear Success Criteria

```
System: Review the code and provide feedback.

Success criteria for your response:
✓ Identify 3-5 potential issues
✓ Rate code quality (1-5)
✓ Provide 1-2 improvement suggestions
✓ End with a QUALITY ASSESSMENT

Once you've completed all success criteria, your work is done.
QUALITY ASSESSMENT marks completion.
```

## Complete Example: Multi-Agent Workflow

### Scenario: Investment Analysis Workflow

```yaml
name: investment-analysis-workflow
config:
  execution: sequential  # Sequential so each agent uses previous output
  completion:
    type: all           # All agents must complete

groups:
  - name: analysis-pipeline
    agents:
      - id: market-analyst
        name: Market Analyst
        model: claude-3-5-sonnet
        system_prompt: |
          You are a market analyst evaluating investment opportunities.
          
          Your task is to analyze market conditions and sizing.
          
          Structure your response:
          1. Market Overview
          2. Market Size & Growth
          3. Competitive Landscape
          4. MARKET ANALYSIS CONCLUSION (2-3 sentences)
          
          The MARKET ANALYSIS CONCLUSION section marks work completion.
          
      - id: technical-analyst
        name: Technical Analyst
        model: claude-3-5-sonnet
        system_prompt: |
          You are a technical analyst. You'll receive the market analysis from the previous agent.
          
          Evaluate the technical and operational aspects.
          
          Structure your response:
          1. Technology Assessment
          2. Operational Capability
          3. Scalability Analysis
          4. TECHNICAL ASSESSMENT CONCLUSION (2-3 sentences)
          
          The TECHNICAL ASSESSMENT CONCLUSION marks work completion.
          
      - id: risk-assessor
        name: Risk Assessor
        model: claude-3-5-sonnet
        system_prompt: |
          You are a risk assessment specialist. You've received analyses from market and technical teams.
          
          Synthesize all analyses and provide comprehensive risk assessment.
          
          Structure your response:
          1. Primary Risks
          2. Mitigation Strategies
          3. Risk Rating (1-5)
          4. FINAL INVESTMENT RECOMMENDATION
          
          The FINAL INVESTMENT RECOMMENDATION marks work completion and workflow termination.
          Include: Yes/No recommendation, risk score, key reasoning.
```

**How completion signals flow:**
1. Market Analyst produces analysis ending with "MARKET ANALYSIS CONCLUSION"
   - ✅ `IsWorkComplete = true`
   - Output passed to Technical Analyst
2. Technical Analyst receives market analysis, produces own analysis ending with "TECHNICAL ASSESSMENT CONCLUSION"
   - ✅ `IsWorkComplete = true`
   - Output passed to Risk Assessor
3. Risk Assessor receives both previous analyses, produces recommendation ending with "FINAL INVESTMENT RECOMMENDATION"
   - ✅ `IsWorkComplete = true`
   - Workflow complete (all 3 agents completed)

## Template: Generic Workflow Agent Prompt

Copy and modify for your use case:

```yaml
system_prompt: |
  You are [AGENT_ROLE]. 
  You are part of a multi-agent analysis workflow.
  
  Your specific task:
  [DESCRIBE TASK IN 1-2 SENTENCES]
  
  Input context:
  [DESCRIBE WHAT INPUT YOU'LL RECEIVE]
  
  What to produce:
  1. [Requirement 1]
  2. [Requirement 2]
  3. [Requirement 3]
  
  Work completion signal:
  End your response with: [COMPLETION_MARKER]
  
  This [COMPLETION_MARKER] section signals that your work is complete
  and ready for [NEXT_STAGE_DESCRIPTION].
```

## Common Completion Markers

Choose one appropriate for your task:

| Task Type | Marker | Example |
|-----------|--------|---------|
| Analysis | `ANALYSIS COMPLETE:` | `MARKET ANALYSIS COMPLETE: XYZ is undervalued` |
| Review | `REVIEW CONCLUSION:` | `REVIEW CONCLUSION: Code quality is B+` |
| Recommendation | `RECOMMENDATION:` | `RECOMMENDATION: Approve with conditions` |
| Planning | `PLAN READY:` | `PLAN READY: Execute in 3 phases` |
| Research | `RESEARCH SUMMARY:` | `RESEARCH SUMMARY: Found X key insights` |
| Quality Check | `QA PASSED:` | `QA PASSED: All criteria met` |
| Consensus | `CONSENSUS:` | `CONSENSUS: Agreement reached on strategy` |

## Anti-Patterns to Avoid

### ❌ Anti-Pattern 1: Encouraging Further Investigation

```
System: "Keep digging deeper until you're 100% certain"
```

**Why it fails:** Agent doesn't know when to stop. Never reaches "100% certain" → Workflow hangs or times out.

**Fix:**
```
System: "Provide your analysis with 80% confidence. 
         If confident enough, conclude with FINAL ASSESSMENT.
         Don't spend more than 3 reasoning steps."
```

### ❌ Anti-Pattern 2: Ambiguous Success Criteria

```
System: "Do a thorough analysis of this topic"
```

**Why it fails:** "Thorough" is subjective. One agent thinks 2 pages is thorough, another thinks 20 pages.

**Fix:**
```
System: "Analyze this topic in 3 sections:
         1. Overview (1 paragraph)
         2. Key Points (3-5 bullets)
         3. Conclusion (1 sentence)
         When all sections are complete, your work is done."
```

### ❌ Anti-Pattern 3: Unclear Multi-Agent Coordination

```
System: "Analyze this and prepare your findings for the team"
```

**Why it fails:** Agent doesn't know what format the "team" (next agent) expects.

**Fix:**
```
System: "Provide your findings in this format:
         [Specific format for next agent to consume]
         
         The next agent will use these findings for their analysis.
         Structure matters for smooth handoff."
```

## Testing Your System Prompt

### Test 1: Single Run

Run your workflow with a simple prompt:
```bash
swarmos -f my-workflow.yaml "Quick test"
```

**Check:** Does agent output clearly signal completion? Is there an obvious "end" to the work?

### Test 2: Consistency

Run 3+ times with same prompt. 

**Check:** Does agent consistently hit the completion marker?

### Test 3: Format Validation

Parse agent output in a script.

```bash
if grep -q "MARKET ANALYSIS COMPLETE:" agent_output.txt; then
    echo "✅ Completion marker found"
else
    echo "❌ Completion marker missing"
fi
```

## Workflow Debugging with System Prompts

If your workflow fails "completion criteria not met":

1. **Check if agent produced output**
   - Yes → Proceed to step 2
   - No → Agent crashed (check logs)

2. **Check if output contains completion marker**
   - Yes → Agent did its job, coordinator issue
   - No → Improve system prompt to require marker

3. **Improve system prompt:**
   ```
   # BEFORE (no completion marker)
   Please analyze the market.
   
   # AFTER (with completion marker)
   Please analyze the market.
   End with: MARKET ANALYSIS COMPLETE: [your conclusion]
   ```

## Advanced: Conditional Completion

For very complex workflows, agents can conditionally mark completion:

```
System: "Analyze the data.

If the data quality is good (score 7+):
  End with: ANALYSIS APPROVED: [Your conclusion]
  
If the data quality is poor (score <7):
  End with: DATA QUALITY ISSUE: [Describe problem]
  DO NOT mark work as complete yet."
```

The coordinator can then route based on error vs completion.

## FAQ

### Q: Can I use emoji or special characters in completion markers?

**A:** It works but avoid it. Stick to ASCII letters, numbers, colons, hyphens. Some systems may have issues with special characters.

### Q: What if my agent naturally concludes without a marker?

**A:** That's still fine! The SDK sets `IsWorkComplete = true` by default on successful completion. The marker helps **humans** and **downstream tools** understand completion. The system will still work without explicit markers, but explicit markers are best practice.

### Q: Can different agents in a group use different completion markers?

**A:** Yes! Each agent can have its own marker. The coordinator only checks `IsWorkComplete` flag, not the text content.

### Q: What if an agent finishes early (doesn't reach completion marker)?

**A:** It still signals `IsWorkComplete = true`. The workflow advances. The coordinator doesn't require the marker text; it only checks the boolean flag.

### Q: How do I handle agents that need multiple turns?

**A:** Set `max_turns` in capabilities:
```yaml
agents:
  - id: researcher
    model: claude-3-5-sonnet
    capabilities:
      max_turns: 5  # Allow up to 5 turns
```

Agent will iterate internally, then exit with `IsWorkComplete = true` after max_turns or early termination.

---

## Summary

**System Prompt Best Practices:**
1. ✅ Explicitly require a final summary/conclusion
2. ✅ Use clear section markers
3. ✅ Set specific success criteria
4. ✅ Be clear about multi-agent coordination
5. ✅ Test that agents consistently hit completion markers
6. ✅ Use the same completion marker structure across related workflows

**Result:**
- Agents produce clear, final outputs
- System reliably detects completion via `IsWorkComplete` flag
- Workflows advance smoothly through multi-stage pipelines
- Humans can easily validate agent work quality

