package chat

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
	json "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

const maxToolParameterValueBytes = searchindex.MaxSegmentTextBytes / 4

// conversationSegments preserves the stored traversal order: message prose,
// tool calls, then tool results for each message. The cap is a prefix cap on
// that order; it never drops arbitrary segments from the middle.
func conversationSegments(conv *conversation.Conversation) []searchindex.Segment {
	if conv == nil {
		return nil
	}
	segments := make([]searchindex.Segment, 0, min(len(conv.Messages), searchindex.MaxSegmentsPerConversation))
	for _, message := range conv.Messages {
		if appendMessageSegments(&segments, conv.ID, conv.WorkspacePath, message) {
			break
		}
	}
	return segments
}

// streamingConversationSegments decodes one message at a time. SourceFile
// supplies identity because workspace_path is serialized after messages in the
// current Conversation layout, while the message array can be stopped as soon
// as the segment cap is reached.
func streamingConversationSegments(ctx context.Context, file searchindex.SourceFile) ([]searchindex.Segment, error) {
	handle, err := os.Open(file.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close() }()

	decoder := jsontext.NewDecoder(handle,
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
	)
	token, err := decoder.ReadToken()
	if err != nil {
		return nil, fmt.Errorf("decode conversation start: %w", err)
	}
	if token.Kind() != '{' {
		return nil, fmt.Errorf("decode conversation: expected object")
	}

	conversationID := file.ID
	workspacePath := file.WorkspacePath
	segments := make([]searchindex.Segment, 0, min(256, searchindex.MaxSegmentsPerConversation))
	for decoder.PeekKind() != '}' {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		keyToken, err := decoder.ReadToken()
		if err != nil {
			return nil, fmt.Errorf("decode conversation key: %w", err)
		}
		if keyToken.Kind() != '"' {
			return nil, fmt.Errorf("decode conversation: non-string object key")
		}
		key := keyToken.String()
		switch key {
		case "id":
			if err := unmarshalHistoryValue(decoder, &conversationID); err != nil {
				return nil, fmt.Errorf("decode conversation id: %w", err)
			}
		case "workspace_path":
			if err := unmarshalHistoryValue(decoder, &workspacePath); err != nil {
				return nil, fmt.Errorf("decode conversation workspace: %w", err)
			}
		case "messages":
			token, err := decoder.ReadToken()
			if err != nil {
				return nil, fmt.Errorf("decode messages start: %w", err)
			}
			if token.Kind() != '[' {
				return nil, fmt.Errorf("decode conversation: messages is not an array")
			}
			for decoder.PeekKind() != ']' {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				var message *conversation.Message
				if err := unmarshalHistoryValue(decoder, &message); err != nil {
					return nil, fmt.Errorf("decode conversation message: %w", err)
				}
				if appendMessageSegments(&segments, conversationID, workspacePath, message) {
					return segments, nil
				}
			}
			if _, err := decoder.ReadToken(); err != nil {
				return nil, fmt.Errorf("decode messages end: %w", err)
			}
		default:
			if err := decoder.SkipValue(); err != nil {
				return nil, fmt.Errorf("skip conversation field %q: %w", key, err)
			}
		}
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, fmt.Errorf("decode conversation end: %w", err)
	}
	return segments, nil
}

func unmarshalHistoryValue(decoder *jsontext.Decoder, value any) error {
	return json.UnmarshalDecode(decoder, value, json.MatchCaseInsensitiveNames(true))
}

// appendMessageSegments returns true when the conversation segment cap is full.
func appendMessageSegments(segments *[]searchindex.Segment, conversationID, workspacePath string, message *conversation.Message) bool {
	if message == nil {
		return len(*segments) >= searchindex.MaxSegmentsPerConversation
	}
	base := searchindex.Segment{
		ConversationID: conversationID,
		WorkspacePath:  workspacePath,
		MessageID:      message.ID,
		Role:           string(message.Role),
		Timestamp:      message.Timestamp,
	}
	appendSegment := func(segment searchindex.Segment) bool {
		if len(*segments) >= searchindex.MaxSegmentsPerConversation {
			return false
		}
		segment.Ordinal = len(*segments)
		segment.Text = truncateUTF8(segment.Text, searchindex.MaxSegmentTextBytes)
		*segments = append(*segments, segment)
		return true
	}

	if message.Role == conversation.RoleUser || message.Role == conversation.RoleAssistant {
		if cleaned := historytools.CleanText(message.Content); cleaned != "" {
			segment := base
			segment.Kind = searchindex.SegmentMessage
			segment.Text = cleaned
			segment.Runtime = message.Role == conversation.RoleUser &&
				historytools.FirstSubstantiveUserText([]*conversation.Message{message}) == ""
			if !appendSegment(segment) {
				return true
			}
		}
	}
	for _, call := range message.ToolCalls {
		segment := base
		segment.Kind = searchindex.SegmentToolCall
		segment.ToolName = call.Name
		segment.CallID = call.ID
		segment.Text = searchableToolCall(call)
		if !appendSegment(segment) {
			return true
		}
	}
	for _, result := range message.ToolResults {
		segment := base
		segment.Kind = searchindex.SegmentToolResult
		segment.ToolName = result.Name
		segment.CallID = result.CallID
		segment.Failed = result.Error != nil
		segment.Text = searchableToolResult(result)
		if !appendSegment(segment) {
			return true
		}
	}
	return len(*segments) >= searchindex.MaxSegmentsPerConversation
}

func searchableToolCall(call conversation.ToolCall) string {
	var text boundedTextBuilder
	text.append(call.Name)
	keys := make([]string, 0, len(call.Parameters))
	for key := range call.Parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if text.len() != 0 {
			text.append(" ")
		}
		text.append(key)
		text.append("=")
		text.append(renderParameterValue(call.Parameters[key]))
	}
	return strings.TrimSpace(text.String())
}

func searchableToolResult(result conversation.ToolResult) string {
	var text boundedTextBuilder
	text.append(result.Output)
	for _, block := range result.Content {
		// Data, annotations, URI metadata, and other non-text fields are
		// intentionally never rendered into the index.
		if block.Text == "" {
			continue
		}
		if text.len() != 0 {
			text.append("\n")
		}
		text.append(block.Text)
	}
	return strings.TrimSpace(text.String())
}

func renderParameterValue(value any) string {
	var text boundedTextBuilder
	text.limit = maxToolParameterValueBytes
	renderSearchValue(&text, value, 0)
	return text.String()
}

func renderSearchValue(text *boundedTextBuilder, value any, depth int) {
	if text.full() {
		return
	}
	if depth >= 8 {
		text.append("…")
		return
	}
	switch typed := value.(type) {
	case nil:
		text.append("null")
	case string:
		text.append(typed)
	case []byte:
		text.append("[binary omitted]")
	case stdjson.RawMessage:
		text.append("[raw data omitted]")
	case bool:
		text.append(strconv.FormatBool(typed))
	case float64:
		text.append(strconv.FormatFloat(typed, 'g', -1, 64))
	case float32:
		text.append(strconv.FormatFloat(float64(typed), 'g', -1, 32))
	case int:
		text.append(strconv.Itoa(typed))
	case int8:
		text.append(strconv.FormatInt(int64(typed), 10))
	case int16:
		text.append(strconv.FormatInt(int64(typed), 10))
	case int32:
		text.append(strconv.FormatInt(int64(typed), 10))
	case int64:
		text.append(strconv.FormatInt(typed, 10))
	case uint:
		text.append(strconv.FormatUint(uint64(typed), 10))
	case uint8:
		text.append(strconv.FormatUint(uint64(typed), 10))
	case uint16:
		text.append(strconv.FormatUint(uint64(typed), 10))
	case uint32:
		text.append(strconv.FormatUint(uint64(typed), 10))
	case uint64:
		text.append(strconv.FormatUint(typed, 10))
	case []any:
		text.append("[")
		for index, item := range typed {
			if index != 0 {
				text.append(" ")
			}
			renderSearchValue(text, item, depth+1)
			if text.full() {
				break
			}
		}
		text.append("]")
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		text.append("{")
		for index, key := range keys {
			if index != 0 {
				text.append(" ")
			}
			text.append(key)
			text.append("=")
			renderSearchValue(text, typed[key], depth+1)
			if text.full() {
				break
			}
		}
		text.append("}")
	default:
		// JSON-decoded parameters use only the cases above. Retain searchable
		// scalar text for programmatically constructed calls while the builder
		// still enforces the per-value bound.
		text.append(fmt.Sprint(typed))
	}
}

type boundedTextBuilder struct {
	builder strings.Builder
	limit   int
}

func (b *boundedTextBuilder) len() int {
	return b.builder.Len()
}

func (b *boundedTextBuilder) full() bool {
	return b.len() >= b.max()
}

func (b *boundedTextBuilder) max() int {
	if b.limit > 0 {
		return b.limit
	}
	return searchindex.MaxSegmentTextBytes
}

func (b *boundedTextBuilder) append(value string) {
	remaining := b.max() - b.len()
	if remaining <= 0 || value == "" {
		return
	}
	b.builder.WriteString(truncateUTF8(value, remaining))
}

func (b *boundedTextBuilder) String() string {
	return b.builder.String()
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes && utf8.ValidString(value) {
		return value
	}
	if maxBytes > len(value) {
		maxBytes = len(value)
	}
	for maxBytes > 0 && !utf8.ValidString(value[:maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}
