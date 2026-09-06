package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
)

// ToolFailureClassification is the common interpretation of a terminal tool
// event. After-execute events with no failure evidence are successful.
type ToolFailureClassification struct {
	Failed      bool
	Success     bool
	ToolName    string
	Reason      string
	Fingerprint string
}

// ToolFrictionClassification describes a failed terminal tool execution that
// may be worth reporting as product friction.
type ToolFrictionClassification struct {
	Friction     bool
	CleanSuccess bool
	ToolName     string
	Category     string
	Fingerprint  string
}

var terminalFailureProse = regexp.MustCompile(`(?i)\b(context deadline exceeded|deadline exceeded|timed out|time(?:out| limit) exceeded|output limit (?:exceeded|reached)|maximum output (?:size )?(?:exceeded|reached)|exceeded (?:the )?maximum output|output exceeds (?:the )?(?:maximum|allowed) limit|output (?:is |was )?too large|response too large)\b`)

const (
	// maxProseScanBytes bounds how much of a tool's output the terminal
	// failure regex ever scans. Tool output is unbounded — a log read or a
	// history dump can be megabytes — and neither hashing nor regex-scanning
	// all of it on the hook path is acceptable.
	maxProseScanBytes = 8 << 10

	// proseSegmentBytes is the size of the head and the tail kept when an
	// output exceeds maxProseScanBytes. Both ends are preserved on purpose:
	// a terminal diagnostic ("context deadline exceeded") is emitted *after*
	// the output it truncates, so a head-only excerpt would drop exactly the
	// signal this regex exists to find.
	proseSegmentBytes = maxProseScanBytes / 2

	// maxFingerprintMaterialBytes bounds the text hashed into a failure
	// fingerprint, so a multi-megabyte error string cannot turn dedup into a
	// multi-megabyte hash on every failing tool call.
	maxFingerprintMaterialBytes = 8 << 10
)

// proseElision marks the gap between the head and tail excerpts. It contains
// no word characters, so it can never create or extend a regex match across
// the seam.
const proseElision = "\n[...]\n"

// boundedText returns text unchanged when it already fits within limit, and
// otherwise a head+tail excerpt of it. The second result reports whether the
// text was excerpted, which callers use to tell "this is the whole thing"
// apart from "this is the beginning and the end of something larger".
func boundedText(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	segment := limit / 2
	return text[:segment] + proseElision + text[len(text)-segment:], true
}

// ClassifyToolFailure classifies the terminal tool events emitted by all SDK
// adapters. It deliberately uses reflection only for Data["result"], because
// hooks cannot import tools without creating an import cycle.
func ClassifyToolFailure(event Event) ToolFailureClassification {
	c := ToolFailureClassification{ToolName: toolEventName(event)}
	if event.Type != EventToolAfterExecute && event.Type != EventToolExecutionFailed {
		return c
	}
	// Hook policy blocks and their sibling batch cancellations are structured
	// non-execution outcomes. They may still carry legacy error fields, but the
	// exact type is authoritative: the tool did not run, so this is neither a
	// failure nor a success. Do not infer this disposition from prose.
	if isPolicyNonExecution(event) {
		return c
	}

	var evidence []string
	if event.Type == EventToolExecutionFailed {
		evidence = append(evidence, "execution failed")
	}
	if reason, failed := outcomeFailure(event.ToolOutcome); failed {
		evidence = append(evidence, reason)
	}
	if event.Data != nil {
		if reason, failed := errorValue(event.Data["error"]); failed {
			evidence = append(evidence, "error: "+reason)
		}
		if reason, failed := structuredFailure(event.Data["tool_output"]); failed {
			evidence = append(evidence, "tool_output: "+reason)
		}
		if reason, failed := resultFailure(event.Data["result"]); failed {
			evidence = append(evidence, "result: "+reason)
		}
	}

	explicitSuccess := event.ToolOutcome.Succeeded()
	if exitCode, reported := event.ToolOutcome.ExitCodeValue(); reported && exitCode == 0 {
		explicitSuccess = true
	}
	if event.Data != nil {
		explicitSuccess = explicitSuccess || structuredSuccess(event.Data["tool_output"])
	}
	// Named tools with no structured outcome may be returning arbitrary source,
	// search, or log content. Do not interpret that content as the tool's own
	// failure report. Keep the narrow prose fallback only for unnamed legacy
	// events, where no tool identity is available to distinguish passthrough
	// content from a terminal diagnostic.
	if len(evidence) == 0 && !explicitSuccess && c.ToolName == "" {
		if prose := failureProse(event); prose != "" {
			evidence = append(evidence, "terminal output: "+prose)
		}
	}
	if len(evidence) == 0 {
		c.Success = event.Type == EventToolAfterExecute
		return c
	}

	c.Failed = true
	c.Reason = strings.Join(evidence, "; ")
	material := normalizeFailureText(c.Reason)
	if bounded, excerpted := boundedText(material, maxFingerprintMaterialBytes); excerpted {
		material = bounded + "\n[length=" + strconv.Itoa(len(material)) + "]"
	}
	sum := sha256.Sum256([]byte(material))
	c.Fingerprint = hex.EncodeToString(sum[:16])
	return c
}

func isPolicyNonExecution(event Event) bool {
	if event.Data == nil {
		return false
	}
	for _, value := range []any{
		event.Data["error_type"],
		reflectedValue(event.Data["tool_output"], "error_type"),
		reflectedValue(reflectedValue(event.Data["result"], "Error"), "Type"),
	} {
		errorType, ok := value.(string)
		if !ok {
			continue
		}
		switch strings.TrimSpace(errorType) {
		case "tool.batch_blocked", "tool.blocked_by_hook":
			return true
		}
	}
	return false
}

// ClassifyToolFriction classifies actual tool failures without interpreting
// arbitrary successful output prose. This keeps source code, documentation,
// and ordinary command output from creating false-positive annoyance nudges.
func ClassifyToolFriction(event Event) ToolFrictionClassification {
	failure := ClassifyToolFailure(event)
	classification := ToolFrictionClassification{ToolName: failure.ToolName}

	if event.Type != EventToolAfterExecute && event.Type != EventToolExecutionFailed {
		return classification
	}

	if failure.Failed {
		classification.Friction = true
		classification.Category = failureFrictionCategory(failure.Reason)
		classification.Fingerprint = frictionFingerprint(classification.Category, failure.Fingerprint)
		return classification
	}

	classification.CleanSuccess = failure.Success
	return classification
}

func failureFrictionCategory(reason string) string {
	normalized := normalizeFailureText(reason)
	switch {
	case strings.Contains(normalized, "timeout"), strings.Contains(normalized, "timed out"),
		strings.Contains(normalized, "deadline exceeded"), strings.Contains(normalized, "time limit exceeded"):
		return "timeout"
	case strings.Contains(normalized, "output limit"), strings.Contains(normalized, "maximum output"),
		strings.Contains(normalized, "output exceeds"), strings.Contains(normalized, "output is too large"),
		strings.Contains(normalized, "output was too large"), strings.Contains(normalized, "response too large"):
		return "output-limit"
	default:
		return "tool-failure"
	}
}

func frictionFingerprint(category, failureFingerprint string) string {
	material := category
	if failureFingerprint != "" {
		material += ":" + failureFingerprint
	}
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:16])
}

func toolEventName(event Event) string {
	if event.Data != nil {
		for _, key := range []string{"tool_name", "name"} {
			if name, ok := event.Data[key].(string); ok && strings.TrimSpace(name) != "" {
				return name
			}
		}
	}
	if event.ToolOutcome != nil {
		return event.ToolOutcome.Tool
	}
	return ""
}

func outcomeFailure(outcome *toolout.Outcome) (string, bool) {
	if outcome == nil {
		return "", false
	}
	if outcome.TimedOut {
		return "timeout", true
	}
	if exitCode, reported := outcome.ExitCodeValue(); reported && exitCode != 0 {
		return "exit code " + strconv.Itoa(exitCode), true
	}
	if outcome.Status == toolout.StatusFailure {
		return "outcome failure", true
	}
	return "", false
}

func structuredFailure(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	var evidence []string
	if success, ok := reflectedBool(value, "success"); ok && !success {
		evidence = append(evidence, "success=false")
	}
	if reason, failed := errorValue(reflectedValue(value, "error")); failed {
		evidence = append(evidence, "error: "+reason)
	}
	if status, ok := reflectedString(value, "status"); ok {
		switch normalizeFailureText(status) {
		case "failure", "failed", "error", "timeout", "timed out", "timed_out":
			evidence = append(evidence, "status="+normalizeFailureText(status))
		}
	}
	return strings.Join(evidence, ", "), len(evidence) > 0
}

func structuredSuccess(value any) bool {
	success, ok := reflectedBool(value, "success")
	return ok && success
}

func resultFailure(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	if reason, failed := outcomeFailureFromReflection(reflectedValue(value, "Outcome")); failed {
		return reason, true
	}
	if isError, ok := reflectedBool(value, "IsError"); ok && isError {
		if reason, failed := errorValue(reflectedValue(value, "Error")); failed {
			return "is_error: " + reason, true
		}
		return "is_error=true", true
	}
	if reason, failed := errorValue(reflectedValue(value, "Error")); failed {
		return "error: " + reason, true
	}
	return structuredFailure(value)
}

func outcomeFailureFromReflection(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	if timedOut, ok := reflectedBool(value, "TimedOut"); ok && timedOut {
		return "timeout", true
	}
	if status, ok := reflectedString(value, "Status"); ok && normalizeFailureText(status) == "failure" {
		return "outcome failure", true
	}
	return "", false
}

func errorValue(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	v := reflect.ValueOf(value)
	if (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) && v.IsNil() {
		return "", false
	}
	if err, ok := value.(error); ok {
		return err.Error(), true
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text), true
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.CanInterface() {
			return errorValue(v.Elem().Interface())
		}
	}
	return "", false
}

func reflectedValue(value any, name string) any {
	v := reflect.ValueOf(value)
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String {
		item := v.MapIndex(reflect.ValueOf(name).Convert(v.Type().Key()))
		if item.IsValid() && item.CanInterface() {
			return item.Interface()
		}
		// Event payload maps conventionally use lower-case JSON keys.
		item = v.MapIndex(reflect.ValueOf(strings.ToLower(name)).Convert(v.Type().Key()))
		if item.IsValid() && item.CanInterface() {
			return item.Interface()
		}
	}
	if v.Kind() == reflect.Struct {
		field := v.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			return field.Interface()
		}
	}
	return nil
}

func reflectedBool(value any, name string) (bool, bool) {
	v := reflectedValue(value, name)
	b, ok := v.(bool)
	return b, ok
}

func reflectedString(value any, name string) (string, bool) {
	v := reflectedValue(value, name)
	if text, ok := v.(string); ok {
		return text, true
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() && rv.Kind() == reflect.String {
		return rv.String(), true
	}
	return "", false
}

func failureProse(event Event) string {
	if event.Data == nil {
		return ""
	}
	for _, value := range []any{
		event.Data["output"],
		event.Data["tool_output"],
		reflectedValue(event.Data["tool_output"], "output"),
		reflectedValue(event.Data["result"], "Output"),
	} {
		if text, ok := value.(string); ok {
			bounded, _ := boundedText(text, maxProseScanBytes)
			if match := terminalFailureProse.FindString(bounded); match != "" {
				return match
			}
		}
	}
	return ""
}

func normalizeFailureText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
