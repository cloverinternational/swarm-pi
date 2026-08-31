package skills

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseLoopInput parses the loop command input using the 3-priority rule system:
// 1. Leading token matching ^\d+[smhd]$ => that's interval, rest is prompt
// 2. Trailing "every <N><unit>" (unit in [s,m,h,d] OR word minutes/hours/days/seconds) => that interval, strip from prompt
// 3. Default interval "10m", whole input is prompt
// Only treat trailing "every X" as an interval when X is a TIME expression.
func ParseLoopInput(input string) (interval string, prompt string) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "10m", ""
	}

	// Rule 1: Leading token
	tokens := strings.Fields(trimmed)
	if len(tokens) > 0 {
		firstToken := tokens[0]
		if match := regexp.MustCompile(`^(\d+)([smhd])$`).FindStringSubmatch(firstToken); match != nil {
			interval = firstToken
			prompt = strings.TrimSpace(strings.TrimPrefix(trimmed, firstToken))
			return interval, prompt
		}
	}

	// Rule 2: Trailing "every" clause
	// Match "every Nm", "every N m", "every N minutes", etc.
	patterns := []string{
		`\s+every\s+(\d+)([smhd])$`,                            // "every 5m"
		`\s+every\s+(\d+)\s+(seconds?|minutes?|hours?|days?)$`, // "every 5 minutes"
		`\s+every\s+(\d+)\s+(s|m|h|d)$`,                        // "every 5 m"
	}

	for _, pattern := range patterns {
		if match := regexp.MustCompile(pattern).FindStringSubmatch(trimmed); match != nil {
			n := match[1]
			unit := match[2]

			// Normalize word units to single letters
			switch unit {
			case "second", "seconds":
				unit = "s"
			case "minute", "minutes":
				unit = "m"
			case "hour", "hours":
				unit = "h"
			case "day", "days":
				unit = "d"
			}

			interval = n + unit
			prompt = strings.TrimSpace(trimmed[:len(trimmed)-len(match[0])])
			return interval, prompt
		}
	}

	// Rule 3: Default
	return "10m", trimmed
}

// IntervalToCron converts an interval string (e.g., "5m", "2h") to a cron expression.
// Uses this exact table:
// - Nm where N <= 59 => "*/N * * * *"
// - Nm where N >= 60 => "0 */H * * *" (H=N/60, must divide 24 else error)
// - Nh where N <= 23 => "0 */N * * *"
// - Nd => "0 0 */N * *"
// - Ns => ceil(N/60) minutes (min 1) => "*/M * * * *"
func IntervalToCron(interval string) (string, error) {
	match := regexp.MustCompile(`^(\d+)([smhd])$`).FindStringSubmatch(interval)
	if match == nil {
		return "", fmt.Errorf("invalid interval format: %s", interval)
	}

	n, _ := strconv.Atoi(match[1])
	unit := match[2]

	switch unit {
	case "s":
		// Round up to nearest minute, min 1
		minutes := (n + 59) / 60
		if minutes == 0 {
			minutes = 1
		}
		if minutes > 59 {
			return "", fmt.Errorf("seconds interval too large, use minutes or hours")
		}
		return fmt.Sprintf("*/%d * * * *", minutes), nil

	case "m":
		if n <= 59 {
			return fmt.Sprintf("*/%d * * * *", n), nil
		}
		// Round to hours
		hours := n / 60
		if n%60 != 0 || 24%hours != 0 {
			return "", fmt.Errorf("interval %dm doesn't divide evenly into hours or 24 hours", n)
		}
		return fmt.Sprintf("0 */%d * * *", hours), nil

	case "h":
		if n > 23 {
			return "", fmt.Errorf("hours interval must be <= 23")
		}
		return fmt.Sprintf("0 */%d * * *", n), nil

	case "d":
		return fmt.Sprintf("0 0 */%d * *", n), nil

	default:
		return "", fmt.Errorf("unknown unit: %s", unit)
	}
}

// LoopSkillInstructions returns the instructions text for the loop builtin skill.
// This is used by TUI slash commands to inject the skill prompt into the chat.
func LoopSkillInstructions() string {
	return loopSkill().Instructions
}

// loopSkill returns the builtin loop skill.
func loopSkill() *Skill {
	return &Skill{
		Metadata: SkillMetadata{
			Name:          "loop",
			Description:   "Run a prompt or slash command on a recurring interval (e.g. /loop 5m /foo, defaults to 10m)",
			WhenToUse:     "When the user wants a recurring task / poll / repeat on an interval. Not for one-off tasks.",
			UserInvocable: true,
			Version:       "1.0.0",
		},
		Instructions: `Parse the user's input using ParseLoopInput to extract the interval and prompt.

If the prompt is empty, show this usage message:
Usage: /loop [interval] <prompt>

Run a prompt or slash command on a recurring interval.

Intervals: Ns, Nm, Nh, Nd (e.g. 5m, 30m, 2h, 1d). Minimum granularity is 1 minute.
If no interval is specified, defaults to 10m.

Examples:
  /loop 5m /babysit-prs
  /loop 30m check the deploy
  /loop 1h /standup 1
  /loop check the deploy          (defaults to 10m)
  /loop check the deploy every 20m

Otherwise:
1. Convert the interval to a cron expression using IntervalToCron
2. Call the CronCreate tool with:
   - cron: the cron expression
   - prompt: the parsed prompt
   - recurring: true
3. Execute the prompt immediately (don't wait for the first cron fire)
4. Confirm to the user with the job ID and that recurring tasks auto-expire after 7 days

If no interval was given and the model wants to self-pace, use the ScheduleWakeup tool instead, passing the same input verbatim and the sentinel <<autonomous-loop-dynamic>>.`,
		Path:          "builtin:loop",
		LoadedAt:      time.Now(),
		ContentLoaded: true,
		Source:        "builtin",
		LoadedFrom:    "builtin",
	}
}
