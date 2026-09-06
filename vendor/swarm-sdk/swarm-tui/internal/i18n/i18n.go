package i18n

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Language identifies one of the UI languages supported by the TUI.
type Language string

const (
	LanguageEnglish Language = "en"
	LanguageSpanish Language = "es"
)

const (
	englishState uint32 = iota
	spanishState
)

var currentLanguage atomic.Uint32

// NormalizeLanguage converts a supported language name to its canonical form.
// Unsupported and empty values deterministically select English.
func NormalizeLanguage(value string) Language {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(LanguageSpanish):
		return LanguageSpanish
	case string(LanguageEnglish):
		return LanguageEnglish
	default:
		return LanguageEnglish
	}
}

// SetLanguage selects the current UI language and returns its canonical value.
func SetLanguage(value string) Language {
	language := NormalizeLanguage(value)
	if language == LanguageSpanish {
		currentLanguage.Store(spanishState)
	} else {
		currentLanguage.Store(englishState)
	}
	return language
}

// CurrentLanguage returns the current UI language.
func CurrentLanguage() Language {
	if currentLanguage.Load() == spanishState {
		return LanguageSpanish
	}
	return LanguageEnglish
}

// T looks up and optionally formats a localized message. An unknown message ID
// is returned unchanged, which keeps missing translations visible and stable.
func T(messageID string, args ...any) string {
	template, found := lookup(CurrentLanguage(), messageID)
	if !found || len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}
