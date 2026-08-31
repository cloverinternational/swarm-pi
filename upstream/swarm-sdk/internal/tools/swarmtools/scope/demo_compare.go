//go:build ignore

package main

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/swarmtools/scope"
)

// Sample Go file — ORIGINAL (agent reads this)
var original = `package handler

import "fmt"

type Server struct {
	port int
}

func (s *Server) Start() error {
	fmt.Println("starting")
	if s.port == 0 {
		return fmt.Errorf("no port")
	}
	return nil
}

func (s *Server) Stop() {
	fmt.Println("stopping")
	if s.port > 0 {
		s.port = 0
	}
}`

// Same file — AFTER LINTER/AGENT B adds 5 imports + a new function above Start()
var modified = `package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

type Server struct {
	port int
}

func NewServer(port int) *Server {
	return &Server{port: port}
}

func (s *Server) Start() error {
	fmt.Println("starting")
	if s.port == 0 {
		return fmt.Errorf("no port")
	}
	return nil
}

func (s *Server) Stop() {
	fmt.Println("stopping")
	if s.port > 0 {
		s.port = 0
	}
}`

func main() {
	det := &scope.GoDetector{}

	origLines := strings.Split(original, "\n")
	modLines := strings.Split(modified, "\n")

	// Use HashFileLines which includes ordinal disambiguation
	origHashed := scope.HashFileLines(origLines, det)
	modHashed := scope.HashFileLines(modLines, det)

	fmt.Println(strings.Repeat("=", 120))
	fmt.Println("ORIGINAL FILE (what agent read)")
	fmt.Println(strings.Repeat("=", 120))
	fmt.Printf("%-4s %-6s %-4s %-35s | %s\n", "LINE", "HASH", "ORD", "SCOPE CHAIN", "CONTENT")
	fmt.Println(strings.Repeat("-", 120))
	for _, hl := range origHashed {
		ch := hl.Scope.String()
		if ch == "" {
			ch = "(top-level)"
		}
		fmt.Printf("%-4d %-6s %-4d %-35s | %s\n", hl.Number, hl.Hash, hl.Ordinal, ch, hl.Content)
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 120))
	fmt.Println("MODIFIED FILE (after linter/Agent B added imports + NewServer func)")
	fmt.Println(strings.Repeat("=", 120))
	fmt.Printf("%-4s %-6s %-4s %-35s | %s\n", "LINE", "HASH", "ORD", "SCOPE CHAIN", "CONTENT")
	fmt.Println(strings.Repeat("-", 120))
	for _, hl := range modHashed {
		ch := hl.Scope.String()
		if ch == "" {
			ch = "(top-level)"
		}
		fmt.Printf("%-4d %-6s %-4d %-35s | %s\n", hl.Number, hl.Hash, hl.Ordinal, ch, hl.Content)
	}

	// Stability comparison
	fmt.Println()
	fmt.Println(strings.Repeat("=", 120))
	fmt.Println("STABILITY — Lines present in BOTH files (matched by content)")
	fmt.Println(strings.Repeat("=", 120))
	fmt.Printf("%-40s | %-5s %-6s | %-5s %-6s | %-7s | %s\n",
		"CONTENT", "ORIG#", "ORIG-H", "MOD#", "MOD-H", "SHIFT", "STATUS")
	fmt.Println(strings.Repeat("-", 120))

	// Build lookup: content → []HashedLine for modified file
	type modEntry struct {
		hl   scope.HashedLine
		used bool
	}
	modByContent := make(map[string][]*modEntry)
	for _, hl := range modHashed {
		modByContent[hl.Content] = append(modByContent[hl.Content], &modEntry{hl: hl})
	}

	stableCount := 0
	brokenCount := 0
	totalMatched := 0

	for _, origHL := range origHashed {
		entries := modByContent[origHL.Content]
		// Find best match: same hash first, then same ordinal, then first unused
		var best *modEntry
		for _, e := range entries {
			if !e.used && e.hl.Hash == origHL.Hash {
				best = e
				break
			}
		}
		if best == nil {
			for _, e := range entries {
				if !e.used && e.hl.Ordinal == origHL.Ordinal {
					best = e
					break
				}
			}
		}
		if best == nil {
			for _, e := range entries {
				if !e.used {
					best = e
					break
				}
			}
		}
		if best == nil {
			continue
		}
		best.used = true
		totalMatched++

		status := "\033[32mSTABLE\033[0m"
		if origHL.Hash != best.hl.Hash {
			status = "\033[31mBROKEN\033[0m"
			brokenCount++
		} else {
			stableCount++
		}

		shift := best.hl.Number - origHL.Number
		shiftStr := fmt.Sprintf("%+d", shift)
		if shift == 0 {
			shiftStr = "0"
		}

		content := origHL.Content
		if len(content) > 38 {
			content = content[:38]
		}

		fmt.Printf("%-40s | %-5d %-6s | %-5d %-6s | %-7s | %s\n",
			content, origHL.Number, origHL.Hash,
			best.hl.Number, best.hl.Hash, shiftStr, status)
	}

	// Summary
	fmt.Println()
	fmt.Println(strings.Repeat("=", 120))
	fmt.Printf("\033[1mSCOPE-AWARE + ORDINAL:  %d/%d lines STABLE  (%d broken)\033[0m\n",
		stableCount, totalMatched, brokenCount)
	fmt.Printf("LINE-NUMBER SYSTEM:     0/%d lines would be stable (ALL shifted = ALL broken)\n", totalMatched)

	// Verify uniqueness
	hashSet := make(map[string]int)
	for _, hl := range origHashed {
		hashSet[hl.Hash]++
	}
	dupes := 0
	for _, count := range hashSet {
		if count > 1 {
			dupes += count
		}
	}
	fmt.Printf("HASH UNIQUENESS:        %d/%d unique in original (%d duplicates)\n",
		len(hashSet), len(origHashed), dupes)

	hashSet2 := make(map[string]int)
	for _, hl := range modHashed {
		hashSet2[hl.Hash]++
	}
	dupes2 := 0
	for _, count := range hashSet2 {
		if count > 1 {
			dupes2 += count
		}
	}
	fmt.Printf("                        %d/%d unique in modified (%d duplicates)\n",
		len(hashSet2), len(modHashed), dupes2)
}
