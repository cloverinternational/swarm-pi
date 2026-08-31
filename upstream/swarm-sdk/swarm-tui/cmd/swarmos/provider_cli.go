package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// runProviderCLI implements the `swarmos provider <subcommand>` family — a
// scriptable, non-interactive way to manage the same providers.json that the
// interactive TUI edits. All mutations go through commands.ConfigManager so the
// CLI and TUI can never drift.
//
//	swarmos provider list                       — list configured providers
//	swarmos provider presets                    — list built-in known providers
//	swarmos provider add <name> [flags]         — add/update a provider
//	swarmos provider remove <name>              — delete a provider
//	swarmos provider set-key <name> [flags]     — update a provider's API key
//	swarmos provider fetch-models <name>        — (re)fetch a provider's models
func runProviderCLI(args []string) error {
	if len(args) == 0 {
		printProviderUsage()
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "list", "ls":
		return providerList()
	case "presets":
		return providerPresets()
	case "add":
		return providerAdd(args[1:])
	case "remove", "rm", "delete":
		return providerRemove(args[1:])
	case "set-key":
		return providerSetKey(args[1:])
	case "fetch-models", "refresh":
		return providerFetchModels(args[1:])
	case "help", "-h", "--help":
		printProviderUsage()
		return nil
	default:
		printProviderUsage()
		return fmt.Errorf("unknown provider subcommand %q", args[0])
	}
}

func printProviderUsage() {
	fmt.Fprint(os.Stderr, `Usage: swarmos provider <subcommand> [args]

Subcommands:
  list                       List configured providers and model counts
  presets                    List built-in known providers (--preset values)
  add <name> [flags]         Add or update a provider
  remove <name>              Delete a provider
  set-key <name> [flags]     Update a provider's API key / base URL
  fetch-models <name>        Re-fetch a provider's model list

add / set-key flags:
  --preset <key>             Prefill base-url + api-type from a known provider
  --base-url <url>           API endpoint (e.g. https://api.groq.com/openai/v1)
  --api-type <type>          openai-compatible | openai | anthropic
  --display-name <name>      Human-readable label
  --api-key <key>            API key (AVOID: leaks into shell history)
  --api-key-env <VAR>        Read the API key from environment variable VAR
  --api-key-stdin            Read the API key from stdin
  --color <hex>              Accent color (e.g. #F55036)
  --fetch-models             Fetch the model list after saving (default for add)
  --no-fetch                 Do not fetch models

Examples:
  swarmos provider add groq --preset groq --api-key-env GROQ_API_KEY
  swarmos provider add mylocal --base-url http://localhost:1234/v1 --no-fetch
  swarmos provider set-key groq --api-key-stdin < key.txt
  swarmos provider fetch-models groq
`)
}

// providerFlags holds parsed add/set-key options.
type providerFlags struct {
	preset      string
	baseURL     string
	apiType     string
	displayName string
	apiKey      string
	apiKeyEnv   string
	apiKeyStdin bool
	color       string
	fetch       bool
	noFetch     bool
}

// parseProviderFlags parses the shared flag set for add/set-key. The first
// non-flag argument (the provider name) must already have been consumed by the
// caller; only flags remain in args.
func parseProviderFlags(args []string) (providerFlags, error) {
	var f providerFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--preset":
			f.preset, err = next()
		case "--base-url":
			f.baseURL, err = next()
		case "--api-type":
			f.apiType, err = next()
		case "--display-name":
			f.displayName, err = next()
		case "--api-key":
			f.apiKey, err = next()
		case "--api-key-env":
			f.apiKeyEnv, err = next()
		case "--api-key-stdin":
			f.apiKeyStdin = true
		case "--color":
			f.color, err = next()
		case "--fetch-models":
			f.fetch = true
		case "--no-fetch":
			f.noFetch = true
		default:
			return f, fmt.Errorf("unknown flag %q", a)
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

// resolveAPIKey returns the API key from whichever source the user chose,
// preferring the most explicit. Empty string means "not provided".
func (f providerFlags) resolveAPIKey() (string, error) {
	if f.apiKeyStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading api key from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	if f.apiKeyEnv != "" {
		v := strings.TrimSpace(os.Getenv(f.apiKeyEnv))
		if v == "" {
			return "", fmt.Errorf("environment variable %s is empty", f.apiKeyEnv)
		}
		return v, nil
	}
	return strings.TrimSpace(f.apiKey), nil
}

// coerceOpenAICompatible applies the same guard the TUI uses: only the real
// OpenAI/Codex providers may keep api_type=="openai"; every custom provider
// using the OpenAI contract must be stored as "openai-compatible".
func coerceOpenAICompatible(apiType, name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if apiType == "openai" && lower != "openai" && lower != "codex" {
		return "openai-compatible"
	}
	return apiType
}

func providerList() error {
	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tAPI TYPE\tAUTH\tMODELS\tAVAILABLE\tBASE URL")
	for _, p := range providers {
		avail := "no"
		if p.Available {
			avail = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			p.Name, p.APIType, p.Type, len(p.Models), avail, p.BaseURL)
	}
	return w.Flush()
}

func providerPresets() error {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PRESET\tDISPLAY NAME\tAPI TYPE\tBASE URL\tKEY ENV")
	for _, p := range commands.KnownProviders() {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Key, p.DisplayName, p.APIType, p.BaseURL, p.EnvKey)
	}
	return w.Flush()
}

func providerAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos provider add <name> [flags]")
	}
	name := args[0]
	f, err := parseProviderFlags(args[1:])
	if err != nil {
		return err
	}

	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}

	// Seed defaults from a preset if requested.
	displayName := f.displayName
	baseURL := f.baseURL
	apiType := f.apiType
	authType := "api_key"
	color := f.color
	var httpMaxRetries *int
	if f.preset != "" {
		kp, ok := commands.LookupKnownProvider(f.preset)
		if !ok {
			return fmt.Errorf("unknown preset %q — see `swarmos provider presets`", f.preset)
		}
		if displayName == "" {
			displayName = kp.DisplayName
		}
		if baseURL == "" {
			baseURL = kp.BaseURL
		}
		if apiType == "" {
			apiType = kp.APIType
		}
		if color == "" {
			color = kp.Color
		}
		authType = kp.AuthType
		httpMaxRetries = kp.HTTPMaxRetries
	}
	if apiType == "" {
		apiType = "openai-compatible"
	}
	apiType = coerceOpenAICompatible(apiType, name)
	if displayName == "" {
		displayName = name
	}
	if baseURL == "" && apiType != "anthropic" && apiType != "openai" {
		return fmt.Errorf("--base-url is required for api_type %q (or use --preset)", apiType)
	}

	apiKey, err := f.resolveAPIKey()
	if err != nil {
		return err
	}

	// Upsert the provider entry.
	idx := -1
	for i, p := range providers {
		if strings.EqualFold(p.Name, name) {
			idx = i
			break
		}
	}
	entry := commands.ProviderConfig{
		Name:           strings.ToLower(strings.ReplaceAll(name, " ", "_")),
		DisplayName:    displayName,
		Color:          color,
		Type:           authType,
		APIType:        apiType,
		BaseURL:        baseURL,
		HTTPMaxRetries: httpMaxRetries,
		APIKey:         apiKey,
		Source:         "user",
		Available:      apiKey != "",
		Models:         []commands.ModelConfig{},
	}
	if idx >= 0 {
		// Preserve existing models unless we refetch below.
		entry.Models = providers[idx].Models
		if entry.HTTPMaxRetries == nil {
			entry.HTTPMaxRetries = providers[idx].HTTPMaxRetries
		}
		providers[idx] = entry
		fmt.Printf("Updated provider %q\n", entry.Name)
	} else {
		providers = append(providers, entry)
		fmt.Printf("Added provider %q (%s)\n", entry.Name, entry.APIType)
	}
	if err := cm.SaveProviders(providers); err != nil {
		return err
	}

	// Decide whether to fetch models. Default: fetch on add unless --no-fetch,
	// but only when we have an endpoint + key to authenticate.
	shouldFetch := !f.noFetch
	if f.fetch {
		shouldFetch = true
	}
	if shouldFetch {
		if err := fetchAndStoreModels(cm, entry.Name, entry.APIType, entry.BaseURL, apiKey); err != nil {
			// Non-fatal: the provider is saved; models can be fetched later.
			fmt.Fprintf(os.Stderr, "warning: model fetch failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "         provider saved; run `swarmos provider fetch-models %s` to retry\n", entry.Name)
		}
	}
	return nil
}

func providerRemove(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos provider remove <name>")
	}
	name := args[0]
	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	out := providers[:0]
	removed := false
	for _, p := range providers {
		if strings.EqualFold(p.Name, name) {
			removed = true
			continue
		}
		out = append(out, p)
	}
	if !removed {
		return fmt.Errorf("provider %q not found", name)
	}
	if err := cm.SaveProviders(out); err != nil {
		return err
	}
	fmt.Printf("Removed provider %q\n", name)
	return nil
}

func providerSetKey(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos provider set-key <name> [flags]")
	}
	name := args[0]
	f, err := parseProviderFlags(args[1:])
	if err != nil {
		return err
	}
	apiKey, err := f.resolveAPIKey()
	if err != nil {
		return err
	}
	if apiKey == "" && f.baseURL == "" {
		return fmt.Errorf("nothing to set: provide --api-key/--api-key-env/--api-key-stdin and/or --base-url")
	}
	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	if err := cm.SaveCredentials(name, apiKey, f.baseURL); err != nil {
		return err
	}
	fmt.Printf("Updated credentials for %q\n", name)
	return nil
}

func providerFetchModels(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos provider fetch-models <name>")
	}
	name := args[0]
	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	for _, p := range providers {
		if strings.EqualFold(p.Name, name) {
			return fetchAndStoreModels(cm, p.Name, p.APIType, p.BaseURL, p.APIKey)
		}
	}
	return fmt.Errorf("provider %q not found", name)
}

// fetchAndStoreModels fetches the model list via the shared helper and writes it
// into the named provider's entry in providers.json.
func fetchAndStoreModels(cm *commands.ConfigManager, name, apiType, baseURL, apiKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	fmt.Printf("Fetching models for %q…\n", name)
	models, err := commands.FetchModelsForProvider(ctx, apiType, baseURL, apiKey)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return fmt.Errorf("provider returned no models")
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	found := false
	for i, p := range providers {
		if strings.EqualFold(p.Name, name) {
			providers[i].Models = models
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("provider %q disappeared before models could be stored", name)
	}
	if err := cm.SaveProviders(providers); err != nil {
		return err
	}
	fmt.Printf("Stored %s models for %q\n", strconv.Itoa(len(models)), name)
	return nil
}
