package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/version"
)

const (
	defaultAnnoyedBodyChars = 60_000
	maxAnnoyedTitleChars    = 120
)

var annoyedRepositoryPattern = regexp.MustCompile(
	`^[A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?/[A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?$`,
)

type annoyedProvenance struct {
	Truncated      bool
	RedactionCount int
}

type annoyedRedactor struct {
	structured *analytics.Redactor
	count      int
}

func newAnnoyedRedactor() *annoyedRedactor {
	return &annoyedRedactor{structured: analytics.NewRedactor()}
}

func (r *annoyedRedactor) redact(value any) any {
	result := r.structured.Redact(value)
	r.count += len(result.Flags)
	return result.Value
}

func (r *annoyedRedactor) text(value string) string {
	if value == "" {
		return ""
	}
	if redacted, ok := r.redact(value).(string); ok {
		return redacted
	}
	return "[REDACTED]"
}

func annoyedRepository(configured, environment string) (string, error) {
	repository := strings.TrimSpace(configured)
	if repository == "" {
		repository = strings.TrimSpace(environment)
	}
	if repository == "" {
		repository = defaultAnnoyedRepository
	}
	if !annoyedRepositoryPattern.MatchString(repository) {
		return "", fmt.Errorf("annoyed: invalid repository %q; expected owner/name", repository)
	}
	return repository, nil
}

func annoyedIssueTitle(issue string) string {
	title := strings.Join(strings.Fields(issue), " ")
	if title == "" {
		return "Agent feedback"
	}
	runes := []rune(title)
	if len(runes) > maxAnnoyedTitleChars {
		title = string(runes[:maxAnnoyedTitleChars-1]) + "…"
	}
	return title
}

func annoyedPublicTitle(issue, severity string) string {
	structured := newAnnoyedRedactor().text(issue)
	safe, _ := vault.NewOutputRedactor().RedactAll(structured, nil)
	if severity != "" {
		safe = "[" + strings.ToUpper(severity) + "] " + safe
	}
	return annoyedIssueTitle(safe)
}

func buildAnnoyedIssueBody(
	ctx context.Context,
	conv *conversation.Conversation,
	historyErr error,
	issue, severity string,
	maxChars int,
) (string, annoyedProvenance) {
	if maxChars <= 0 {
		maxChars = defaultAnnoyedBodyChars
	}
	redactor := newAnnoyedRedactor()
	var body strings.Builder
	sourceTruncated := false
	body.WriteString("## Agent complaint\n\n")
	body.WriteString(redactor.text(issue))
	body.WriteString("\n\n## Invocation\n\n")
	writeMetadataLine(&body, "Severity", severity)
	writeMetadataLine(&body, "Conversation", tools.OwnerConversationID(ctx))
	writeMetadataLine(&body, "Agent", tools.OwnerAgentID(ctx))
	writeMetadataLine(&body, "Tool call", tools.ToolCallID(ctx))
	writeMetadataLine(&body, "Workspace", redactor.text(tools.WorkspacePathFromContext(ctx)))
	writeAnnoyedBuildProvenance(&body)

	body.WriteString("\n## Conversation transcript\n\n")
	switch {
	case historyErr != nil:
		body.WriteString("_History unavailable: ")
		body.WriteString(redactor.text(historyErr.Error()))
		body.WriteString("_\n")
	case conv == nil:
		body.WriteString("_History unavailable: conversation not found._\n")
	default:
		writeMetadataLine(&body, "Title", redactor.text(conv.Title))
		writeMetadataLine(&body, "Created", formatAnnoyedTime(conv.CreatedAt))
		writeMetadataLine(&body, "Updated", formatAnnoyedTime(conv.UpdatedAt))
		for _, message := range conv.Messages {
			if ctx.Err() != nil {
				sourceTruncated = true
				body.WriteString("\n_Transcript stopped because the invocation was canceled._\n")
				break
			}
			appendAnnoyedMessage(ctx, &body, redactor, message, maxChars)
			if body.Len() > maxChars*2 {
				sourceTruncated = true
				prefix := safeUTF8Prefix(body.String(), maxChars)
				body.Reset()
				body.WriteString(prefix)
				body.WriteString("\n\n… [earlier transcript bounded before publication] …\n")
				if len(conv.Messages) > 0 {
					var tail strings.Builder
					appendAnnoyedMessage(ctx, &tail, redactor, conv.Messages[len(conv.Messages)-1], maxChars/2)
					tailText, _ := truncateAnnoyedBody(tail.String(), maxChars/2)
					body.WriteString(tailText)
				}
				break
			}
		}
	}

	body.WriteString("\n## Current annoyed invocation\n\n")
	body.WriteString("- Issue: ")
	body.WriteString(redactor.text(issue))
	body.WriteString("\n")
	if severity != "" {
		body.WriteString("- Severity: ")
		body.WriteString(severity)
		body.WriteString("\n")
	}
	body.WriteString("\n_Transcript contains visible messages and tool execution records. Hidden reasoning, base system prompts, binary content, token accounting, and agent memory are intentionally excluded._\n")

	entropyRedactor := vault.NewOutputRedactor()
	redacted, count := entropyRedactor.RedactAll(body.String(), nil)
	truncated, didTruncate := truncateAnnoyedBody(redacted, maxChars)
	return truncated, annoyedProvenance{Truncated: sourceTruncated || didTruncate, RedactionCount: redactor.count + count}
}

func appendAnnoyedMessage(
	ctx context.Context,
	body *strings.Builder,
	redactor *annoyedRedactor,
	message *conversation.Message,
	maxChars int,
) {
	if message == nil || ctx.Err() != nil {
		return
	}
	if maxChars <= 0 {
		maxChars = defaultAnnoyedBodyChars
	}
	body.WriteString("\n### ")
	body.WriteString(string(message.Role))
	if timestamp := formatAnnoyedTime(message.Timestamp); timestamp != "" {
		body.WriteString(" · ")
		body.WriteString(timestamp)
	}
	body.WriteString("\n\n")
	if message.Content != "" {
		budget := annoyedRecordBudget(body, maxChars)
		if budget <= 0 {
			return
		}
		content, _ := truncateAnnoyedBody(message.Content, budget)
		body.WriteString(redactor.text(content))
		body.WriteString("\n")
	}
	for _, call := range message.ToolCalls {
		if ctx.Err() != nil {
			return
		}
		budget := annoyedRecordBudget(body, maxChars)
		if budget <= 128 {
			body.WriteString("\n… [additional tool records omitted] …\n")
			return
		}
		body.WriteString("\nTool call `")
		body.WriteString(redactor.text(call.Name))
		body.WriteString("`:\n\n")
		budget = annoyedRecordBudget(body, maxChars)
		limited := newAnnoyedValueLimiter(ctx, budget).limit(call.Parameters)
		appendIndentedJSON(body, redactor.redact(limited), budget)
	}
	for _, result := range message.ToolResults {
		if ctx.Err() != nil {
			return
		}
		budget := annoyedRecordBudget(body, maxChars)
		if budget <= 128 {
			body.WriteString("\n… [additional tool records omitted] …\n")
			return
		}
		output, _ := truncateAnnoyedBody(result.Output, budget/2)
		record := map[string]any{
			"call_id": result.CallID,
			"name":    result.Name,
			"output":  output,
		}
		if result.Error != nil {
			errorMessage, _ := truncateAnnoyedBody(result.Error.Message, budget/4)
			record["error"] = map[string]any{"type": result.Error.Type, "message": errorMessage}
		}
		if len(result.Content) > 0 {
			blockCount := len(result.Content)
			if blockCount > 100 {
				blockCount = 100
			}
			blocks := make([]any, 0, blockCount+1)
			for _, block := range result.Content[:blockCount] {
				text, _ := truncateAnnoyedBody(block.Text, budget/4)
				blocks = append(blocks, map[string]any{
					"type": block.Type, "text": text, "mime_type": block.MimeType,
					"uri": block.URI, "name": block.Name, "description": block.Description, "size": block.Size,
					"binary_bytes_omitted": len(block.Data),
				})
			}
			if blockCount < len(result.Content) {
				blocks = append(blocks, map[string]any{"content_blocks_omitted": len(result.Content) - blockCount})
			}
			record["content"] = blocks
		}
		body.WriteString("\nTool result")
		if result.Name != "" {
			body.WriteString(" `")
			body.WriteString(redactor.text(result.Name))
			body.WriteString("`")
		}
		body.WriteString(":\n\n")
		budget = annoyedRecordBudget(body, maxChars)
		limited := newAnnoyedValueLimiter(ctx, budget).limit(record)
		appendIndentedJSON(body, redactor.redact(limited), budget)
	}
}

func annoyedRecordBudget(body *strings.Builder, maxChars int) int {
	remaining := maxChars*2 - body.Len()
	if remaining <= 0 {
		return 0
	}
	if remaining > maxChars/2 {
		return maxChars / 2
	}
	return remaining
}

type annoyedValueLimiter struct {
	ctx       context.Context
	remaining int
	items     int
}

func newAnnoyedValueLimiter(ctx context.Context, budget int) *annoyedValueLimiter {
	if budget <= 0 {
		budget = defaultAnnoyedBodyChars / 2
	}
	return &annoyedValueLimiter{ctx: ctx, remaining: budget}
}

func (l *annoyedValueLimiter) limit(value any) any {
	if l.ctx.Err() != nil {
		return "[CANCELED]"
	}
	if l.remaining <= 0 || l.items >= 1000 {
		return "[TRUNCATED]"
	}
	l.items++
	switch typed := value.(type) {
	case string:
		limited, truncated := truncateAnnoyedBody(typed, l.remaining)
		l.remaining -= len(limited)
		if truncated {
			l.remaining = 0
		}
		return limited
	case []byte:
		l.remaining -= 16
		return fmt.Sprintf("[%d binary bytes omitted]", len(typed))
	case map[string]any:
		keyCapacity := len(typed)
		if keyCapacity > 100 {
			keyCapacity = 100
		}
		keys := make([]string, 0, keyCapacity)
		for key := range typed {
			if l.ctx.Err() != nil || len(keys) >= 100 {
				break
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make(map[string]any, len(keys)+1)
		for _, key := range keys {
			if l.ctx.Err() != nil || l.remaining <= 0 {
				break
			}
			l.remaining -= len(key)
			result[key] = l.limit(typed[key])
		}
		if len(keys) < len(typed) {
			result["_omitted_entries"] = len(typed) - len(keys)
		}
		return result
	case []any:
		count := len(typed)
		if count > 100 {
			count = 100
		}
		result := make([]any, 0, count+1)
		for _, item := range typed[:count] {
			if l.ctx.Err() != nil || l.remaining <= 0 {
				break
			}
			result = append(result, l.limit(item))
		}
		if count < len(typed) {
			result = append(result, map[string]any{"_omitted_items": len(typed) - count})
		}
		return result
	default:
		return l.limitReflect(reflect.ValueOf(value), 0)
	}
}

func (l *annoyedValueLimiter) limitReflect(value reflect.Value, depth int) any {
	if !value.IsValid() {
		return nil
	}
	if depth >= 32 || l.ctx.Err() != nil || l.remaining <= 0 || l.items >= 1000 {
		return "[TRUNCATED]"
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
		depth++
		if depth >= 32 {
			return "[TRUNCATED]"
		}
	}
	l.items++
	switch value.Kind() {
	case reflect.String:
		return l.limit(value.String())
	case reflect.Bool:
		l.remaining -= 8
		return value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		l.remaining -= 16
		return value.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		l.remaining -= 16
		return value.Uint()
	case reflect.Float32, reflect.Float64:
		l.remaining -= 16
		return value.Float()
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			l.remaining -= 16
			return fmt.Sprintf("[%d binary bytes omitted]", value.Len())
		}
		count := min(value.Len(), 100)
		result := make([]any, 0, count+1)
		for index := 0; index < count && l.remaining > 0; index++ {
			result = append(result, l.limitReflect(value.Index(index), depth+1))
		}
		if count < value.Len() {
			result = append(result, map[string]any{"_omitted_items": value.Len() - count})
		}
		return result
	case reflect.Map:
		type entry struct {
			key   string
			value reflect.Value
		}
		entries := make([]entry, 0, min(value.Len(), 100))
		iterator := value.MapRange()
		for iterator.Next() && len(entries) < 100 {
			key := iterator.Key()
			keyText := ""
			if key.Kind() == reflect.String {
				keyText = key.String()
			} else if key.CanInterface() {
				keyText = fmt.Sprint(key.Interface())
			}
			keyText, _ = truncateAnnoyedBody(keyText, 256)
			entries = append(entries, entry{key: keyText, value: iterator.Value()})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		result := make(map[string]any, len(entries)+1)
		for _, item := range entries {
			if l.remaining <= 0 {
				break
			}
			l.remaining -= len(item.key)
			result[item.key] = l.limitReflect(item.value, depth+1)
		}
		if len(entries) < value.Len() {
			result["_omitted_entries"] = value.Len() - len(entries)
		}
		return result
	case reflect.Struct:
		result := make(map[string]any)
		valueType := value.Type()
		for index := 0; index < value.NumField() && len(result) < 100 && l.remaining > 0; index++ {
			fieldType := valueType.Field(index)
			fieldValue := value.Field(index)
			if fieldType.PkgPath != "" || !fieldValue.CanInterface() {
				continue
			}
			name := fieldType.Name
			if tag := strings.Split(fieldType.Tag.Get("json"), ",")[0]; tag == "-" {
				continue
			} else if tag != "" {
				name = tag
			}
			l.remaining -= len(name)
			result[name] = l.limitReflect(fieldValue, depth+1)
		}
		return result
	default:
		l.remaining -= 16
		if value.CanInterface() {
			return fmt.Sprint(value.Interface())
		}
		return "[UNSUPPORTED]"
	}
}

func appendIndentedJSON(body *strings.Builder, value any, budget int) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		body.WriteString("    [unserializable tool record]\n")
		return
	}
	encodedText, _ := truncateAnnoyedBody(string(encoded), budget)
	for _, line := range strings.Split(encodedText, "\n") {
		if budget <= 0 {
			break
		}
		lineBudget := budget - 5
		if lineBudget <= 0 {
			break
		}
		line = safeUTF8Prefix(line, lineBudget)
		body.WriteString("    ")
		body.WriteString(line)
		body.WriteString("\n")
		budget -= len(line) + 5
	}
}

func writeMetadataLine(body *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	body.WriteString("- ")
	body.WriteString(label)
	body.WriteString(": ")
	body.WriteString(value)
	body.WriteString("\n")
}

// annoyedCommitDisplayLen is the number of hex characters kept from the build
// commit. It is NOT a cosmetic choice.
//
// The whole body is passed through vault.OutputRedactor.RedactAll before it is
// published. That redactor replaces any token matching [A-Za-z0-9+/=_-]{16,}
// whose Shannon entropy exceeds 3.5 with "[REDACTED-ENTROPY]". A full 40-char
// git SHA measures ~3.73 bits/char, and "<sha>-dirty" ~3.68 — BOTH exceed the
// threshold, so emitting either would publish "Commit: [REDACTED-ENTROPY]" and
// silently defeat the entire point of this stamp while still looking correct
// in the source.
//
// 12 hex characters stay under the redactor's 16-char floor, so the token is
// never even considered, and 12 is still collision-safe for `git show` on a
// repository of this size. For the same reason the dirty marker is emitted as
// a separate " (dirty)" suffix rather than "-dirty": the space and parenthesis
// are outside the token character class, so they break the run instead of
// extending it past 16 characters.
const annoyedCommitDisplayLen = 12

// writeAnnoyedBuildProvenance stamps the identity of the binary that produced
// this report into the Invocation block.
//
// Without it, triage cannot distinguish "this defect is still live" from "this
// was filed against a build that predates the fix", and the only way to tell
// is per-issue `git log -S` archaeology. That is not hypothetical: a triage
// pass over this backlog found 13 of 14 issues already fixed on main, and an
// earlier pass found a 14-issue cluster (25% of the backlog at the time) in
// the same state. Both cost hours to disprove.
//
// Emitted immediately after the invocation metadata and before the transcript,
// so it lands in the head slice that truncateAnnoyedBody always preserves.
func writeAnnoyedBuildProvenance(body *strings.Builder) {
	// Product version is resolver-supplied and legitimately absent in SDK-only
	// embeddings; writeMetadataLine drops empty values, so the line simply does
	// not appear rather than publishing a misleading "unknown".
	writeMetadataLine(body, "Version", version.ProductVersion())
	writeMetadataLine(body, "SDK", version.Version)

	commit := version.EffectiveGitCommit()
	if commit != "" && commit != "unknown" {
		// EffectiveGitCommit already appends "-dirty"; re-express it as a
		// separate marker so the hex run stays below the redactor's threshold
		// (see annoyedCommitDisplayLen).
		dirty := strings.HasSuffix(commit, "-dirty")
		commit = strings.TrimSuffix(commit, "-dirty")
		if len(commit) > annoyedCommitDisplayLen {
			commit = commit[:annoyedCommitDisplayLen]
		}
		if dirty || version.EffectiveBuildDirty() {
			// A dirty build is not a clean-build report: whatever is on disk
			// may not correspond to any commit a maintainer can check out.
			commit += " (dirty)"
		}
		writeMetadataLine(body, "Commit", commit)
	}

	if built := version.EffectiveBuildTime(); built != "" && built != "unknown" {
		writeMetadataLine(body, "Built", built)
	}
	writeMetadataLine(body, "Go/Platform", fmt.Sprintf("%s %s/%s",
		runtime.Version(), runtime.GOOS, runtime.GOARCH))
}

func formatAnnoyedTime(value interface {
	IsZero() bool
	Format(string) string
}) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02T15:04:05Z07:00")
}

func redactedAnalyticsText(redactor *analytics.Redactor, value string) string {
	result := redactor.Redact(value)
	if redacted, ok := result.Value.(string); ok {
		return redacted
	}
	return "[REDACTED]"
}

func truncateAnnoyedBody(body string, limit int) (string, bool) {
	if limit <= 0 || len(body) <= limit {
		return body, false
	}
	const markerTemplate = "\n\n… [transcript truncated: %d bytes omitted] …\n\n"
	marker := fmt.Sprintf(markerTemplate, len(body)-limit)
	available := limit - len(marker)
	if available <= 0 {
		return safeUTF8Prefix(body, limit), true
	}
	headBytes := available / 3
	tailBytes := available - headBytes
	head := safeUTF8Prefix(body, headBytes)
	tail := safeUTF8Suffix(body, tailBytes)
	omitted := len(body) - len(head) - len(tail)
	marker = fmt.Sprintf(markerTemplate, omitted)
	for len(head)+len(marker)+len(tail) > limit && len(tail) > 0 {
		tail = safeUTF8Suffix(tail, len(tail)-1)
	}
	return head + marker + tail, true
}

func safeUTF8Prefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func safeUTF8Suffix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	start := len(value) - limit
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func sanitizeAnnoyedError(value string) string {
	redactor := analytics.NewRedactor()
	redacted := redactedAnalyticsText(redactor, strings.TrimSpace(value))
	entropyRedactor := vault.NewOutputRedactor()
	redacted, _ = entropyRedactor.RedactAll(redacted, nil)
	return redacted
}
