package i18n

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

var catalog = struct {
	sync.RWMutex
	english map[string]string
	spanish map[string]string
}{
	english: make(map[string]string),
	spanish: make(map[string]string),
}

// Register adds a parity-matched English and Spanish catalog for an area.
// Invalid catalogs panic during initialization instead of leaving the UI with a
// partial or type-unsafe translation set.
func Register(area string, english, spanish map[string]string) {
	if strings.TrimSpace(area) == "" {
		panic("i18n: catalog area must not be empty")
	}
	if len(english) == 0 || len(spanish) == 0 {
		panic(fmt.Sprintf("i18n: area %q must provide non-empty English and Spanish catalogs", area))
	}
	if len(english) != len(spanish) {
		panic(fmt.Sprintf("i18n: area %q has unequal catalog sizes", area))
	}

	for messageID, englishTemplate := range english {
		if strings.TrimSpace(messageID) == "" {
			panic(fmt.Sprintf("i18n: area %q contains an empty message ID", area))
		}
		spanishTemplate, ok := spanish[messageID]
		if !ok {
			panic(fmt.Sprintf("i18n: area %q is missing Spanish message %q", area, messageID))
		}
		englishFormat, err := formatSignature(englishTemplate)
		if err != nil {
			panic(fmt.Sprintf("i18n: invalid English format for %q: %v", messageID, err))
		}
		spanishFormat, err := formatSignature(spanishTemplate)
		if err != nil {
			panic(fmt.Sprintf("i18n: invalid Spanish format for %q: %v", messageID, err))
		}
		if !reflect.DeepEqual(englishFormat, spanishFormat) {
			panic(fmt.Sprintf("i18n: incompatible formatting for message %q", messageID))
		}
	}
	for messageID := range spanish {
		if _, ok := english[messageID]; !ok {
			panic(fmt.Sprintf("i18n: area %q is missing English message %q", area, messageID))
		}
	}

	catalog.Lock()
	defer catalog.Unlock()
	for messageID, englishTemplate := range english {
		if existing, ok := catalog.english[messageID]; ok {
			if existing != englishTemplate || catalog.spanish[messageID] != spanish[messageID] {
				panic(fmt.Sprintf("i18n: conflicting registration for message %q", messageID))
			}
		}
	}
	for messageID, englishTemplate := range english {
		catalog.english[messageID] = englishTemplate
		catalog.spanish[messageID] = spanish[messageID]
	}
}

func lookup(language Language, messageID string) (string, bool) {
	catalog.RLock()
	defer catalog.RUnlock()

	if language == LanguageSpanish {
		if value, ok := catalog.spanish[messageID]; ok {
			return value, true
		}
	}
	if value, ok := catalog.english[messageID]; ok {
		return value, true
	}
	return messageID, false
}

// formatSignature returns the format operations performed on each argument.
// Tracking argument indexes permits translations to reorder explicitly indexed
// operands while rejecting templates that would format an operand differently.
func formatSignature(format string) (map[int][]string, error) {
	signature := make(map[int][]string)
	nextArgument := 1

	for offset := 0; offset < len(format); {
		if format[offset] != '%' {
			_, size := utf8.DecodeRuneInString(format[offset:])
			if size == 0 {
				size = 1
			}
			offset += size
			continue
		}
		offset++
		if offset >= len(format) {
			return nil, fmt.Errorf("trailing %%")
		}
		if format[offset] == '%' {
			offset++
			continue
		}

		offset = skipFormatFlags(format, offset)
		if index, end, found, err := parseFormatIndex(format, offset); err != nil {
			return nil, err
		} else if found {
			nextArgument = index
			offset = skipFormatFlags(format, end)
		}

		if offset < len(format) && format[offset] == '*' {
			signature[nextArgument] = append(signature[nextArgument], "width")
			nextArgument++
			offset++
		} else {
			for offset < len(format) && format[offset] >= '0' && format[offset] <= '9' {
				offset++
			}
		}

		if offset < len(format) && format[offset] == '.' {
			offset++
			if index, end, found, err := parseFormatIndex(format, offset); err != nil {
				return nil, err
			} else if found {
				nextArgument = index
				offset = end
			}
			if offset < len(format) && format[offset] == '*' {
				signature[nextArgument] = append(signature[nextArgument], "precision")
				nextArgument++
				offset++
			} else {
				for offset < len(format) && format[offset] >= '0' && format[offset] <= '9' {
					offset++
				}
			}
		}

		if index, end, found, err := parseFormatIndex(format, offset); err != nil {
			return nil, err
		} else if found {
			nextArgument = index
			offset = end
		}
		if offset >= len(format) {
			return nil, fmt.Errorf("missing formatting verb")
		}
		verb, size := utf8.DecodeRuneInString(format[offset:])
		if verb == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("invalid UTF-8 formatting verb")
		}
		signature[nextArgument] = append(signature[nextArgument], "verb:"+string(verb))
		nextArgument++
		offset += size
	}

	for _, operations := range signature {
		sort.Strings(operations)
	}
	return signature, nil
}

func skipFormatFlags(format string, offset int) int {
	for offset < len(format) && strings.ContainsRune("#0+- ", rune(format[offset])) {
		offset++
	}
	return offset
}

func parseFormatIndex(format string, offset int) (index, end int, found bool, err error) {
	if offset >= len(format) || format[offset] != '[' {
		return 0, offset, false, nil
	}
	closeOffset := strings.IndexByte(format[offset+1:], ']')
	if closeOffset < 0 {
		return 0, offset, false, fmt.Errorf("unterminated argument index")
	}
	closeOffset += offset + 1
	rawIndex := format[offset+1 : closeOffset]
	parsed, parseErr := strconv.Atoi(rawIndex)
	if parseErr != nil || parsed < 1 {
		return 0, offset, false, fmt.Errorf("invalid argument index %q", rawIndex)
	}
	return parsed, closeOffset + 1, true, nil
}
