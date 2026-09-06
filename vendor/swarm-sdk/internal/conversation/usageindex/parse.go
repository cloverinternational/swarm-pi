package usageindex

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	jp "github.com/buger/jsonparser"
)

// streamingThreshold is the file size above which the whole-file read is
// replaced by a compacting pass. Conversation stores contain rare
// multi-hundred-megabyte files, and reading one of those into a single []byte
// spikes the TUI's heap by exactly its size.
const streamingThreshold = 32 << 20

// maxRetainedStringBytes is the longest string value the compactor keeps.
// Everything the index reads — ids, timestamps, roles, token numbers — is far
// shorter than this; the bytes that make conversation files enormous are
// message content, thinking blocks and tool payloads, which are dropped.
const maxRetainedStringBytes = 512

type parsedConversation struct {
	ID          string
	ParentID    string
	TotalTokens int64
	Responses   []Response
}

// parseConversationFile extracts only usage-bearing fields from one persisted
// conversation. It deliberately does NOT run json.Valid first: validating every
// byte of the store cost more than all other parsing combined, and both parsers
// below already fail on structurally broken input.
func parseConversationFile(path string, size int64) (parsedConversation, error) {
	if size > streamingThreshold {
		return parseConversationStream(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return parsedConversation{}, err
	}
	return parseConversationBytes(data)
}

// parseConversationStream handles files too large to hold in memory. It streams
// the document through a compactor that preserves structure, keys and numbers
// exactly while dropping long string values, then parses the result with the
// same extractor used everywhere else. Peak memory becomes proportional to the
// file's structure rather than to its size: a 1.66 GB conversation compacts to
// a few megabytes because almost all of it is message text.
func parseConversationStream(path string) (parsedConversation, error) {
	file, err := os.Open(path)
	if err != nil {
		return parsedConversation{}, err
	}
	defer file.Close()
	compacted, err := compactJSON(bufio.NewReaderSize(file, 1<<20))
	if err != nil {
		return parsedConversation{}, err
	}
	return parseConversationBytes(compacted)
}

// compactJSON copies a JSON document, replacing any string value longer than
// maxRetainedStringBytes with an empty string. Escape sequences are tracked so
// string boundaries are found correctly; every other byte is passed through, so
// the output remains valid JSON with identical structure and numbers.
func compactJSON(reader io.Reader) ([]byte, error) {
	var out bytes.Buffer
	scratch := make([]byte, 0, maxRetainedStringBytes+1)
	buffer := make([]byte, 64<<10)
	inString := false
	escaped := false
	elided := false

	for {
		read, err := reader.Read(buffer)
		for index := 0; index < read; index++ {
			character := buffer[index]
			if !inString {
				out.WriteByte(character)
				if character == '"' {
					inString, escaped, elided = true, false, false
					scratch = scratch[:0]
				}
				continue
			}
			if escaped {
				escaped = false
				appendScratch(&scratch, character, &elided)
				continue
			}
			switch character {
			case '\\':
				escaped = true
				appendScratch(&scratch, character, &elided)
			case '"':
				if !elided {
					out.Write(scratch)
				}
				out.WriteByte('"')
				inString = false
			default:
				appendScratch(&scratch, character, &elided)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if inString {
		return nil, errors.New("conversation file ended inside a string")
	}
	return out.Bytes(), nil
}

// appendScratch buffers string content up to the retention limit. Once the
// limit is passed the string is marked elided and no further bytes are kept, so
// a single gigabyte-long value costs a few hundred bytes.
func appendScratch(scratch *[]byte, character byte, elided *bool) {
	if *elided {
		return
	}
	if len(*scratch) >= maxRetainedStringBytes {
		*elided = true
		*scratch = (*scratch)[:0]
		return
	}
	*scratch = append(*scratch, character)
}

func parseConversationBytes(data []byte) (parsedConversation, error) {
	var parsed parsedConversation
	trimmed := bytes.TrimSpace(data)
	// Truncated writes are the dominant corruption mode, and they are caught by
	// two byte comparisons instead of a full-document validation pass.
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return parsed, errors.New("conversation file is not a complete JSON object")
	}
	parsed.ID, _ = jp.GetString(data, "id")
	forked, _ := jp.GetString(data, "metadata", "custom", "forked_from")
	compacted, _ := jp.GetString(data, "metadata", "custom", "compacted_from")
	parsed.ParentID = firstNonEmpty(forked, compacted)

	messages, valueType, _, err := jp.Get(data, "messages")
	if errors.Is(err, jp.KeyPathNotFoundError) {
		parsed.finalize()
		return parsed, nil
	}
	if err != nil {
		return parsed, err
	}
	if valueType != jp.Array {
		return parsed, fmt.Errorf("messages field is %v, want array", valueType)
	}

	var callbackErr error
	_, arrayErr := jp.ArrayEach(messages, func(value []byte, _ jp.ValueType, _ int, iterErr error) {
		if iterErr != nil {
			callbackErr = iterErr
			return
		}
		if message, ok := extractMessage(value); ok {
			parsed.appendResponse(message.id, message.timestamp, message.tokens, message.cache)
		}
	})
	if callbackErr != nil {
		return parsed, callbackErr
	}
	if arrayErr != nil {
		return parsed, arrayErr
	}
	parsed.finalize()
	return parsed, nil
}

// extractedMessage is the only part of a persisted message the index needs.
type extractedMessage struct {
	id        string
	timestamp string
	tokens    rawTokens
	cache     rawCacheMetrics
}

// extractMessage pulls usage fields straight out of the raw message bytes.
// Reading with jsonparser rather than unmarshalling into a struct matters on
// large conversations: message content, thinking blocks and tool payloads are
// skipped in place instead of being allocated as Go strings and thrown away.
func extractMessage(value []byte) (extractedMessage, bool) {
	role, err := jp.GetString(value, "role")
	if err != nil || role != "assistant" {
		return extractedMessage{}, false
	}
	tokenBytes, tokenType, _, err := jp.Get(value, "tokens")
	if err != nil || tokenType != jp.Object {
		return extractedMessage{}, false
	}
	message := extractedMessage{
		tokens: rawTokens{
			Input:         jsonInt(tokenBytes, "input"),
			Output:        jsonInt(tokenBytes, "output"),
			Total:         jsonInt(tokenBytes, "total"),
			CacheCreation: jsonInt(tokenBytes, "cache_creation"),
			Creation5m:    jsonInt(tokenBytes, "cache_creation_5m"),
			Creation1h:    jsonInt(tokenBytes, "cache_creation_1h"),
			CacheRead:     jsonInt(tokenBytes, "cache_read"),
		},
	}
	if cacheBytes, cacheType, _, err := jp.Get(value, "metadata", "cache_metrics"); err == nil && cacheType == jp.Object {
		message.cache = rawCacheMetrics{
			Creation:   jsonInt(cacheBytes, "cache_creation_tokens"),
			Creation5m: jsonInt(cacheBytes, "cache_creation_5m_tokens"),
			Creation1h: jsonInt(cacheBytes, "cache_creation_1h_tokens"),
			Read:       jsonInt(cacheBytes, "cache_read_tokens"),
		}
	}
	message.id, _ = jp.GetString(value, "id")
	message.timestamp, _ = jp.GetString(value, "timestamp")
	return message, true
}

func (p *parsedConversation) appendResponse(id, timestamp string, tokens rawTokens, cache rawCacheMetrics) {
	response := normalize(tokens, cache)
	if response.Total <= 0 {
		return
	}
	response.Seq = len(p.Responses)
	response.Timestamp = parseTimestamp(timestamp)
	key, lineageScoped := dedupKey(id, timestamp, "", response.Seq, tokens)
	if strings.HasPrefix(key, "conversation:") {
		// The conversation ID may not have been read yet (it can appear after
		// the messages array), so these keys are completed in finalize.
		key = ""
	}
	response.DedupKey = key
	response.LineageScoped = lineageScoped
	p.Responses = append(p.Responses, response)
	p.TotalTokens += response.Total
}

// finalize stamps the conversation identity onto every response, including the
// fallback dedup keys that could not be built until the ID was known.
func (p *parsedConversation) finalize() {
	for index := range p.Responses {
		p.Responses[index].ConversationID = p.ID
		if p.Responses[index].DedupKey == "" {
			p.Responses[index].DedupKey = "conversation:" + p.ID +
				"|index:" + strconv.Itoa(p.Responses[index].Seq)
		}
	}
}

func jsonInt(data []byte, key string) int64 {
	value, err := jp.GetInt(data, key)
	if err != nil {
		return 0
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
