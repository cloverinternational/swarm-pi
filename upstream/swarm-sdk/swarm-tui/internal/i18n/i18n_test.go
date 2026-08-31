package i18n

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var testCatalogSequence atomic.Uint64

func registerTestMessage(t *testing.T, english, spanish string) string {
	t.Helper()
	id := fmt.Sprintf("i18n-test.%d.message", testCatalogSequence.Add(1))
	Register("i18n-test", map[string]string{id: english}, map[string]string{id: spanish})
	return id
}

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		value string
		want  Language
	}{
		{"en", LanguageEnglish},
		{"EN", LanguageEnglish},
		{" en ", LanguageEnglish},
		{"es", LanguageSpanish},
		{"ES", LanguageSpanish},
		{"\tes\n", LanguageSpanish},
		{"", LanguageEnglish},
		{"fr", LanguageEnglish},
		{"es-MX", LanguageEnglish},
	}
	for _, test := range tests {
		if got := NormalizeLanguage(test.value); got != test.want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestLanguageSwitchingAndFallback(t *testing.T) {
	t.Cleanup(func() { SetLanguage("en") })
	id := registerTestMessage(t, "Ready", "Listo")

	if got := SetLanguage("es"); got != LanguageSpanish {
		t.Fatalf("SetLanguage(es) = %q", got)
	}
	if got := CurrentLanguage(); got != LanguageSpanish {
		t.Fatalf("CurrentLanguage() = %q", got)
	}
	if got := T(id); got != "Listo" {
		t.Fatalf("Spanish T() = %q", got)
	}

	if got := SetLanguage("unsupported"); got != LanguageEnglish {
		t.Fatalf("SetLanguage(unsupported) = %q", got)
	}
	if got := CurrentLanguage(); got != LanguageEnglish {
		t.Fatalf("CurrentLanguage() = %q", got)
	}
	if got := T(id); got != "Ready" {
		t.Fatalf("fallback T() = %q", got)
	}

	SetLanguage("es")
	const unknown = "i18n-test.unknown"
	if got := T(unknown); got != unknown {
		t.Fatalf("unknown T() = %q, want unchanged ID", got)
	}
	if got := T(unknown, "unused"); got != unknown {
		t.Fatalf("unknown formatted T() = %q, want unchanged ID", got)
	}
}

func TestFormatting(t *testing.T) {
	t.Cleanup(func() { SetLanguage("en") })
	id := registerTestMessage(t, "%s has %d tasks (100%%)", "%[2]d tareas para %[1]s (100%%)")

	SetLanguage("en")
	if got := T(id, "Ada", 3); got != "Ada has 3 tasks (100%)" {
		t.Fatalf("English formatted T() = %q", got)
	}
	SetLanguage("es")
	if got := T(id, "Ada", 3); got != "3 tareas para Ada (100%)" {
		t.Fatalf("Spanish formatted T() = %q", got)
	}
}

func TestRegisterValidation(t *testing.T) {
	assertPanics := func(t *testing.T, contains string, fn func()) {
		t.Helper()
		defer func() {
			recovered := recover()
			if recovered == nil {
				t.Fatal("expected Register to panic")
			}
			if message := fmt.Sprint(recovered); !strings.Contains(message, contains) {
				t.Fatalf("panic %q does not contain %q", message, contains)
			}
		}()
		fn()
	}

	t.Run("empty area", func(t *testing.T) {
		assertPanics(t, "area", func() {
			Register("", map[string]string{"x": "x"}, map[string]string{"x": "x"})
		})
	})
	t.Run("empty catalogs", func(t *testing.T) {
		assertPanics(t, "non-empty", func() { Register("empty", nil, nil) })
	})
	t.Run("missing Spanish key", func(t *testing.T) {
		assertPanics(t, "unequal", func() {
			Register(
				"parity",
				map[string]string{"one": "one", "two": "two"},
				map[string]string{"one": "uno"},
			)
		})
	})
	t.Run("different equal-sized keys", func(t *testing.T) {
		assertPanics(t, "missing Spanish", func() {
			Register("parity", map[string]string{"one": "one"}, map[string]string{"two": "dos"})
		})
	})
	t.Run("incompatible verb", func(t *testing.T) {
		assertPanics(t, "incompatible formatting", func() {
			Register("format", map[string]string{"format.value": "%d"}, map[string]string{"format.value": "%s"})
		})
	})
	t.Run("incompatible argument", func(t *testing.T) {
		assertPanics(t, "incompatible formatting", func() {
			Register("format", map[string]string{"format.value": "%s %d"}, map[string]string{"format.value": "%d %s"})
		})
	})
	t.Run("malformed format", func(t *testing.T) {
		assertPanics(t, "invalid English format", func() {
			Register("format", map[string]string{"format.value": "%"}, map[string]string{"format.value": "%"})
		})
	})
	t.Run("conflicting duplicate", func(t *testing.T) {
		id := registerTestMessage(t, "first", "primero")
		assertPanics(t, "conflicting registration", func() {
			Register("duplicate", map[string]string{id: "second"}, map[string]string{id: "segundo"})
		})
	})
	t.Run("identical duplicate is idempotent", func(t *testing.T) {
		id := registerTestMessage(t, "same", "igual")
		Register("duplicate", map[string]string{id: "same"}, map[string]string{id: "igual"})
	})
}

func TestConcurrentReadsAndSwitches(t *testing.T) {
	t.Cleanup(func() { SetLanguage("en") })
	id := registerTestMessage(t, "English", "Español")

	const (
		readers    = 32
		iterations = 2000
	)
	start := make(chan struct{})
	errs := make(chan string, readers)
	var workers sync.WaitGroup
	workers.Add(readers + 1)

	go func() {
		defer workers.Done()
		<-start
		for index := 0; index < iterations; index++ {
			if index%2 == 0 {
				SetLanguage("en")
			} else {
				SetLanguage("es")
			}
		}
	}()
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer workers.Done()
			<-start
			for index := 0; index < iterations; index++ {
				switch got := T(id); got {
				case "English", "Español":
				default:
					errs <- got
					return
				}
			}
		}()
	}

	close(start)
	workers.Wait()
	close(errs)
	for got := range errs {
		t.Errorf("concurrent T() returned %q", got)
	}
}
