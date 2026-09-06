// Command deepwiki-direct generates wiki documentation directly using the Swarm SDK
// deepwiki engine - no bridge API required.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/deepwiki"
)

func main() {
	// Parse flags
	fs := flag.NewFlagSet("deepwiki-direct", flag.ExitOnError)
	provider := fs.String("provider", "", "LLM provider (anthropic, openai, ollama, zhipu)")
	model := fs.String("model", "", "Model name (provider-specific)")
	language := fs.String("language", "English", "Output language")
	maxPages := fs.Int("max-pages", 0, "Maximum pages to generate (0 = auto)")
	workers := fs.Int("workers", 4, "Parallel workers")
	exclude := fs.String("exclude", "", "Comma-separated dirs to exclude")
	focus := fs.String("focus", "", "Comma-separated dirs to focus on")
	output := fs.String("output", "", "Output directory for wiki files (default: <repo>/.wiki)")
	format := fs.String("format", "markdown", "Output format: markdown, json")
	verbose := fs.Bool("v", false, "Verbose output")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Handle help
	if os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		printUsage()
		os.Exit(0)
	}

	// Find the repo path (first non-flag argument)
	var repoPath string
	var flagArgs []string
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			// If this flag takes a value and the next arg isn't a flag, include it
			if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				// Check if this flag expects a value
				if arg == "--provider" || arg == "--model" || arg == "--language" ||
					arg == "--max-pages" || arg == "--workers" || arg == "--exclude" ||
					arg == "--focus" || arg == "--output" || arg == "--format" {
					i++
					flagArgs = append(flagArgs, os.Args[i])
				}
			}
		} else if repoPath == "" {
			repoPath = arg
		} else {
			// Additional positional args - could be an error or ignored
		}
	}

	if repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: repository path required\n")
		printUsage()
		os.Exit(1)
	}

	// Parse flags
	if err := fs.Parse(flagArgs); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	// Resolve absolute path
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	// Validate provider
	if *provider == "" {
		// Try to auto-detect from environment
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			*provider = "anthropic"
			*model = "claude-3-5-sonnet-latest"
		} else if os.Getenv("OPENAI_API_KEY") != "" {
			*provider = "openai"
			*model = "gpt-4o"
		} else if os.Getenv("ZHIPU_API_KEY") != "" {
			*provider = "zhipu"
			*model = "glm-4-plus"
		} else {
			*provider = "ollama"
			*model = "llama3"
		}
		log.Printf("[deepwiki] Auto-detected provider: %s/%s", *provider, *model)
	}

	// Build options
	var opts []deepwiki.Option
	opts = append(opts, deepwiki.WithProvider(*provider, *model))

	if *exclude != "" {
		excludeDirs := strings.Split(*exclude, ",")
		for i, d := range excludeDirs {
			excludeDirs[i] = strings.TrimSpace(d)
		}
		opts = append(opts, deepwiki.WithExcludeDirs(excludeDirs...))
	}

	if *focus != "" {
		focusDirs := strings.Split(*focus, ",")
		for i, d := range focusDirs {
			focusDirs[i] = strings.TrimSpace(d)
		}
		opts = append(opts, deepwiki.WithFocusDirs(focusDirs...))
	}

	// Print config
	fmt.Println("┌─ DEEPWIKI DIRECT ─────────────────────────────────────────────────┐")
	fmt.Printf("  Repository:    %s\n", absPath)
	fmt.Printf("  Provider:      %s\n", *provider)
	fmt.Printf("  Model:         %s\n", *model)
	fmt.Printf("  Workers:       %d\n", *workers)
	fmt.Printf("  Language:      %s\n", *language)
	if *maxPages > 0 {
		fmt.Printf("  Max Pages:     %d\n", *maxPages)
	}
	if *exclude != "" {
		fmt.Printf("  Exclude:       %s\n", *exclude)
	}
	if *focus != "" {
		fmt.Printf("  Focus:         %s\n", *focus)
	}
	if *output != "" {
		fmt.Printf("  Output:        %s\n", *output)
	}
	fmt.Println()

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nCancellation requested...")
		cancel()
	}()

	// Create engine
	fmt.Println("┌─ ANALYZING ───────────────────────────────────────────────────────┐")
	startTime := time.Now()

	eng, err := deepwiki.NewEngine(absPath, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating engine: %v\n", err)
		os.Exit(1)
	}

	// Analyze repository
	graph, err := eng.Analyze(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing repository: %v\n", err)
		os.Exit(1)
	}

	elapsed := time.Since(startTime).Round(time.Second)
	fmt.Printf("  ✓ Analyzed %d files, %d entities, %d chunks [%s]\n",
		graph.TotalFiles, len(graph.Entities), len(graph.Chunks), elapsed)
	fmt.Println()

	// Generate wiki
	fmt.Println("┌─ GENERATING ──────────────────────────────────────────────────────┐")
	startTime = time.Now()

	req := deepwiki.GenerateRequest{
		RepoPath: absPath,
		Provider: *provider,
		Model:    *model,
		Language: *language,
		MaxPages: *maxPages,
		Workers:  *workers,
	}

	// Track progress with PageCallback
	progressCb := func(page *deepwiki.WikiPage, current, total int) error {
		pct := float64(0)
		if total > 0 {
			pct = float64(current) / float64(total) * 100
		}
		title := page.Title
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		elapsed := time.Since(startTime).Round(time.Second)
		fmt.Printf("\r  [%d/%d] (%.0f%%) %s [%s]    ",
			current, total, pct, title, elapsed)
		return nil
	}

	// Generate with callback
	wikiCache, err := eng.GenerateWikiWithCallback(ctx, req, progressCb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError generating wiki: %v\n", err)
		os.Exit(1)
	}

	elapsed = time.Since(startTime).Round(time.Second)
	fmt.Printf("\n  ✓ Generated %d pages [%s]\n", len(wikiCache.GeneratedPages), elapsed)
	fmt.Println()

	// Output results
	outputDir := *output
	if outputDir == "" {
		outputDir = filepath.Join(absPath, ".wiki")
	}

	fmt.Println("┌─ OUTPUT ───────────────────────────────────────────────────────────┐")

	switch *format {
	case "json":
		if err := outputJSON(wikiCache, outputDir, *verbose); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing JSON: %v\n", err)
			os.Exit(1)
		}
	default:
		if err := outputMarkdown(wikiCache, outputDir, *verbose); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing markdown: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("\n└─ DONE ────────────────────────────────────────────────────────────┘")
}

func printUsage() {
	fmt.Print(`DeepWiki Direct - Generate wikis using the Swarm SDK engine directly
No bridge API required!

Usage:
  deepwiki-direct <repo> [options]

Options:
  --provider <name>    LLM provider (anthropic, openai, ollama, zhipu)
                       Auto-detected from env vars if not specified
  --model <name>       Model name (provider-specific)
  --language <lang>    Output language (default: English)
  --max-pages <n>      Maximum pages to generate (0 = auto)
  --workers <n>        Parallel workers (default: 4)
  --exclude <dirs>     Comma-separated dirs to exclude
  --focus <dirs>       Comma-separated dirs to focus on
  --output <dir>       Output directory (default: <repo>/.wiki)
  --format <fmt>       Output format: markdown, json (default: markdown)
  -v                   Verbose output

Environment Variables:
  ANTHROPIC_API_KEY    Use Anthropic Claude models
  OPENAI_API_KEY       Use OpenAI GPT models
  ZHIPU_API_KEY        Use Zhipu GLM models
  (none)               Falls back to Ollama (requires local server)

Examples:
  # Generate wiki using Anthropic Claude (auto-detected from env)
  deepwiki-direct ./my-repo

  # Use OpenAI with a specific model
  deepwiki-direct ./my-repo --provider openai --model gpt-4o

  # Use local Ollama
  deepwiki-direct ./my-repo --provider ollama --model llama3

  # Focus on specific directories
  deepwiki-direct ./my-repo --focus "src,lib" --exclude "tests,vendor"
`)
}

func outputMarkdown(wikiCache *deepwiki.WikiCache, outputDir string, verbose bool) error {
	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	fmt.Printf("  Writing markdown to %s ...\n", outputDir)

	structure := wikiCache.Structure

	// Write Home.md
	homePath := filepath.Join(outputDir, "Home.md")
	var homeContent strings.Builder
	homeContent.WriteString(fmt.Sprintf("# %s\n\n%s\n\n", structure.Title, structure.Description))

	// Add table of contents
	homeContent.WriteString("## Contents\n\n")
	for _, section := range structure.Sections {
		homeContent.WriteString(fmt.Sprintf("### %s\n", section.Title))
		for _, pageID := range section.PageIDs {
			if page, ok := wikiCache.GeneratedPages[pageID]; ok {
				homeContent.WriteString(fmt.Sprintf("- [[%s]]\n", page.Title))
			}
		}
		homeContent.WriteString("\n")
	}

	if err := os.WriteFile(homePath, []byte(homeContent.String()), 0644); err != nil {
		return fmt.Errorf("write Home.md: %w", err)
	}

	// Write each page
	for _, page := range wikiCache.GeneratedPages {
		// Sanitize filename
		filename := strings.ReplaceAll(page.Title, "/", "-")
		filename = strings.ReplaceAll(filename, " ", "-")
		filename = strings.ReplaceAll(filename, ":", "")
		filename = strings.ToLower(filename) + ".md"

		pagePath := filepath.Join(outputDir, filename)
		if err := os.WriteFile(pagePath, []byte(page.Content), 0644); err != nil {
			if verbose {
				log.Printf("Warning: could not write %s: %v", pagePath, err)
			}
			continue
		}
		if verbose {
			fmt.Printf("    Written: %s\n", filename)
		}
	}

	fmt.Printf("  ✓ Written %d pages to %s\n", len(wikiCache.GeneratedPages)+1, outputDir)
	return nil
}

func outputJSON(wikiCache *deepwiki.WikiCache, outputDir string, verbose bool) error {
	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	fmt.Printf("  Writing JSON to %s ...\n", outputDir)

	// Write wiki.json
	jsonPath := filepath.Join(outputDir, "wiki.json")
	data, err := json.MarshalIndent(wikiCache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wiki: %w", err)
	}

	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return fmt.Errorf("write wiki.json: %w", err)
	}

	fmt.Printf("  ✓ Written wiki.json to %s\n", outputDir)
	return nil
}
