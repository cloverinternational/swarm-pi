package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// runModelCLI implements `swarmos model <subcommand>` for managing the models
// inside a provider entry — useful for providers that do not expose a
// /v1/models endpoint (where `provider fetch-models` cannot help).
//
//	swarmos model list <provider>                      — list a provider's models
//	swarmos model add <provider> <model-id> [flags]    — add a model manually
//	swarmos model remove <provider> <model-id>         — delete a model
func runModelCLI(args []string) error {
	if len(args) == 0 {
		printModelUsage()
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "list", "ls":
		return modelList(args[1:])
	case "add":
		return modelAdd(args[1:])
	case "remove", "rm", "delete":
		return modelRemove(args[1:])
	case "help", "-h", "--help":
		printModelUsage()
		return nil
	default:
		printModelUsage()
		return fmt.Errorf("unknown model subcommand %q", args[0])
	}
}

func printModelUsage() {
	fmt.Fprint(os.Stderr, `Usage: swarmos model <subcommand> [args]

Subcommands:
  list <provider>                    List a provider's models
  add <provider> <model-id> [flags]  Add a model manually
  remove <provider> <model-id>       Delete a model

add flags:
  --display-name <name>   Human-readable label (default: model id)
  --context-window <n>    Context window in tokens
  --description <text>    Short description

Examples:
  swarmos model add mylocal llama-3.1-70b --context-window 131072
  swarmos model list mylocal
`)
}

func findProviderIndex(providers []commands.ProviderConfig, name string) int {
	for i, p := range providers {
		if strings.EqualFold(p.Name, name) {
			return i
		}
	}
	return -1
}

func modelList(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos model list <provider>")
	}
	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	idx := findProviderIndex(providers, args[0])
	if idx < 0 {
		return fmt.Errorf("provider %q not found", args[0])
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "MODEL ID\tDISPLAY NAME\tCONTEXT\tDESCRIPTION")
	for _, m := range providers[idx].Models {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.ID, m.DisplayName, m.Context, m.Description)
	}
	return w.Flush()
}

func modelAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: swarmos model add <provider> <model-id> [flags]")
	}
	providerName := args[0]
	modelID := args[1]

	var displayName, description string
	var contextWindow int
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		next := func() (string, error) {
			if i+1 >= len(rest) {
				return "", fmt.Errorf("flag %s requires a value", a)
			}
			i++
			return rest[i], nil
		}
		var err error
		switch a {
		case "--display-name":
			displayName, err = next()
		case "--description":
			description, err = next()
		case "--context-window":
			var v string
			if v, err = next(); err == nil {
				_, err = fmt.Sscanf(v, "%d", &contextWindow)
			}
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
		if err != nil {
			return err
		}
	}
	if displayName == "" {
		displayName = modelID
	}

	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	idx := findProviderIndex(providers, providerName)
	if idx < 0 {
		return fmt.Errorf("provider %q not found — add it first with `swarmos provider add`", providerName)
	}
	// Upsert the model by ID.
	for _, m := range providers[idx].Models {
		if m.ID == modelID {
			return fmt.Errorf("model %q already exists on provider %q", modelID, providerName)
		}
	}
	providers[idx].Models = append(providers[idx].Models, commands.ModelConfig{
		ID:            modelID,
		DisplayName:   displayName,
		Description:   description,
		ContextWindow: contextWindow,
		Context:       formatContextForCLI(contextWindow),
	})
	if err := cm.SaveProviders(providers); err != nil {
		return err
	}
	fmt.Printf("Added model %q to provider %q\n", modelID, providerName)
	return nil
}

func modelRemove(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: swarmos model remove <provider> <model-id>")
	}
	providerName := args[0]
	modelID := args[1]
	cm, err := commands.NewConfigManager()
	if err != nil {
		return err
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	idx := findProviderIndex(providers, providerName)
	if idx < 0 {
		return fmt.Errorf("provider %q not found", providerName)
	}
	models := providers[idx].Models
	out := models[:0]
	removed := false
	for _, m := range models {
		if m.ID == modelID {
			removed = true
			continue
		}
		out = append(out, m)
	}
	if !removed {
		return fmt.Errorf("model %q not found on provider %q", modelID, providerName)
	}
	providers[idx].Models = out
	if err := cm.SaveProviders(providers); err != nil {
		return err
	}
	fmt.Printf("Removed model %q from provider %q\n", modelID, providerName)
	return nil
}

// formatContextForCLI renders a token count like "128K" for display, matching
// the style used elsewhere. Empty when unknown.
func formatContextForCLI(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%dM", tokens/1_000_000)
	case tokens >= 1000:
		return fmt.Sprintf("%dK", tokens/1000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}
