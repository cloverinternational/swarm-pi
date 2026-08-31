package main

import (
	"regexp"
	"sync"
	"testing"
)

func TestValidatePeerHandle(t *testing.T) {
	for _, handle := range []string{"", "worker-1", "host.name_2"} {
		if err := validatePeerHandle(handle); err != nil {
			t.Errorf("validatePeerHandle(%q) = %v", handle, err)
		}
	}
	for _, handle := range []string{".", "..", "../escape", "/absolute", `back\\slash`, "has space", string(make([]byte, 65))} {
		if err := validatePeerHandle(handle); err == nil {
			t.Errorf("validatePeerHandle(%q) unexpectedly succeeded", handle)
		}
	}
}

func TestNewPeerHandleSuffixUniqueConcurrent(t *testing.T) {
	const count = 1000
	results := make(chan string, count)
	var workers sync.WaitGroup
	workers.Add(count)
	for range count {
		go func() {
			defer workers.Done()
			results <- newPeerHandleSuffix()
		}()
	}
	workers.Wait()
	close(results)

	hexSuffix := regexp.MustCompile(`^[0-9a-f]{12}$`)
	seen := make(map[string]struct{}, count)
	for suffix := range results {
		if !hexSuffix.MatchString(suffix) {
			t.Fatalf("suffix %q is not 48 bits of hexadecimal entropy", suffix)
		}
		if _, exists := seen[suffix]; exists {
			t.Fatalf("duplicate suffix %q", suffix)
		}
		seen[suffix] = struct{}{}
	}
	if len(seen) != count {
		t.Fatalf("generated %d unique suffixes, want %d", len(seen), count)
	}
}
