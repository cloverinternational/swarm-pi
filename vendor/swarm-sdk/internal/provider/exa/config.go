// Package exa implements the Exa search provider for the Swarm SDK.
//
// Exa (https://exa.ai) is an AI-powered search API. Unlike LLM providers this
// package does not provide text generation — it is a *search-only* provider.
// When used as a provider.Provider the Chat() method treats the last user
// message as a search query and returns formatted search results as the
// assistant response.
//
// # Authentication
//
// Set the EXA_API_KEY environment variable, or pass the key explicitly:
//
//	p, err := exa.New(provider.Config{
//	    Name:   "exa",
//	    APIKey: "exa-...",
//	})
//
// # Search types
//
// The search type is controlled via Config.Custom["search_type"]:
//   - "auto"    (default) — Exa picks neural or keyword automatically
//   - "neural"  — semantic/embedding-based
//   - "keyword" — traditional keyword search
//
// Set Config.Custom["use_answer"] = true to use the /answer endpoint instead
// of /search (returns a single AI-generated answer with citations).
package exa

import (
	"fmt"
	"os"
	"time"
)

const (
	// DefaultBaseURL is the canonical Exa API base URL.
	DefaultBaseURL = "https://api.exa.ai"

	// DefaultNumResults is the default number of search results to return.
	DefaultNumResults = 10

	// DefaultSearchType is the default Exa search mode.
	DefaultSearchType = "auto"

	// DefaultTimeout is the default HTTP client timeout.
	DefaultTimeout = 30 * time.Second
)

// Config holds Exa-specific configuration.
type Config struct {
	// APIKey is the Exa API key (required).
	// Falls back to the EXA_API_KEY environment variable when empty.
	APIKey string

	// BaseURL is the Exa API base URL. Defaults to DefaultBaseURL.
	BaseURL string

	// NumResults is the default number of results to return (default 10, max 20).
	NumResults int

	// SearchType controls the Exa search mode: "auto", "neural", or "keyword".
	SearchType string

	// UseAnswer instructs the provider to call the /answer endpoint instead of
	// /search, returning a single AI-generated answer with citations.
	UseAnswer bool

	// Timeout for individual HTTP requests. Defaults to DefaultTimeout.
	Timeout time.Duration
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		APIKey:     os.Getenv("EXA_API_KEY"),
		BaseURL:    DefaultBaseURL,
		NumResults: DefaultNumResults,
		SearchType: DefaultSearchType,
		UseAnswer:  false,
		Timeout:    DefaultTimeout,
	}
}

// validate checks that required fields are present and fills in defaults.
func (c *Config) validate() error {
	if c.APIKey == "" {
		c.APIKey = os.Getenv("EXA_API_KEY")
	}
	if c.APIKey == "" {
		return fmt.Errorf("exa: API key is required (set EXA_API_KEY or provide APIKey)")
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.NumResults <= 0 {
		c.NumResults = DefaultNumResults
	}
	if c.NumResults > 20 {
		c.NumResults = 20
	}
	if c.SearchType == "" {
		c.SearchType = DefaultSearchType
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	return nil
}
