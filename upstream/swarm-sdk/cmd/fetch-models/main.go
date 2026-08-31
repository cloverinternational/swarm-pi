// Package main provides a CLI tool to fetch available models from OpenAI API
// and update test configuration files.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

func main() {
	apiKey := flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key")
	baseURL := flag.String("base-url", "https://api.openai.com/v1", "OpenAI API base URL")
	format := flag.String("format", "json", "Output format: json, list, toml")
	flag.Parse()

	if *apiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: API key required (use -api-key or OPENAI_API_KEY env var)\n")
		os.Exit(1)
	}

	// Fetch models
	models, err := openai.FetchAndSaveModels(*apiKey, *baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching models: %v\n", err)
		os.Exit(1)
	}

	// Output in requested format
	switch *format {
	case "json":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.Encode(models)

	case "list":
		for _, model := range models {
			fmt.Println(model)
		}

	case "toml":
		fmt.Println("models = [")
		for _, model := range models {
			fmt.Printf("    %q,\n", model)
		}
		fmt.Println("]")

	default:
		fmt.Fprintf(os.Stderr, "Unknown format: %s\n", *format)
		os.Exit(1)
	}
}
