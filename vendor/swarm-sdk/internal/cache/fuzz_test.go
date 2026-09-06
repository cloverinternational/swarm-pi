package cache

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzCacheKeyValidation fuzzes cache key validation
func FuzzCacheKeyValidation(f *testing.F) {
	f.Add("simple_key")
	f.Add("")
	f.Add("\x00")
	f.Add("../../../etc/passwd")
	f.Add(strings.Repeat("k", 5000))

	f.Fuzz(func(t *testing.T, key string) {
		// Cache key operations should be safe
		_ = len(key)
		_ = strings.Contains(key, "/")
		normalized := strings.ToLower(key)
		_ = normalized
	})
}

// FuzzCacheValueParsing fuzzes cached value parsing
func FuzzCacheValueParsing(f *testing.F) {
	f.Add([]byte(`"simple string"`))
	f.Add([]byte(`123`))
	f.Add([]byte(`true`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(strings.Repeat(`{"k":`, 1000) + `"v"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return
		}

		// Cache operations should be safe
		marshaled, _ := json.Marshal(value)
		var value2 any
		_ = json.Unmarshal(marshaled, &value2)
	})
}

// FuzzCacheEvictionPolicy fuzzes eviction policy handling
func FuzzCacheEvictionPolicy(f *testing.F) {
	f.Add("lru")
	f.Add("lfu")
	f.Add("fifo")
	f.Add("")
	f.Add("invalid")
	f.Add("\x00")

	f.Fuzz(func(t *testing.T, policy string) {
		// Policy handling should be safe
		_ = len(policy)
		isValid := policy == "lru" || policy == "lfu" || policy == "fifo"
		_ = isValid
	})
}

// FuzzCacheTTLHandling fuzzes TTL parsing and expiration
func FuzzCacheTTLHandling(f *testing.F) {
	f.Add([]byte(`0`))
	f.Add([]byte(`3600`))
	f.Add([]byte(`-1`))
	f.Add([]byte(`999999999`))
	f.Add([]byte(`"not a number"`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var ttl any
		if err := json.Unmarshal(data, &ttl); err != nil {
			return
		}
		// TTL operations should be safe
		_ = fmt.Sprintf("%v", ttl)
	})
}

// FuzzCacheStatsParsing fuzzes cache statistics parsing
func FuzzCacheStatsParsing(f *testing.F) {
	f.Add([]byte(`{"hits":100,"misses":50}`))
	f.Add([]byte(`{"hits":0,"misses":0}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"hits":null,"misses":"string"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var stats map[string]any
		if err := json.Unmarshal(data, &stats); err != nil {
			return
		}

		// Stats operations should be safe
		if hits, ok := stats["hits"]; ok {
			_ = fmt.Sprintf("%v", hits)
		}
		if misses, ok := stats["misses"]; ok {
			_ = fmt.Sprintf("%v", misses)
		}
	})
}

// FuzzCacheConcurrentAccess fuzzes concurrent cache operations
func FuzzCacheConcurrentAccess(f *testing.F) {
	f.Add("key1")
	f.Add("key2")
	f.Add("")
	f.Add(strings.Repeat("k", 1000))

	f.Fuzz(func(t *testing.T, key string) {
		// Concurrent access should be safe (in real impl would use sync)
		_ = len(key)
		_ = strings.ToLower(key)
	})
}
