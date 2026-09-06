package skills

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// CommandHandler handles skill-related commands.
type CommandHandler struct {
	Loader *Loader
}

// NewCommandHandler creates a new command handler.
func NewCommandHandler(loader *Loader) *CommandHandler {
	return &CommandHandler{Loader: loader}
}

// HandleCommand processes a skill command.
func (h *CommandHandler) HandleCommand(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return h.showHelp(), nil
	}

	switch args[0] {
	case "list", "ls":
		return h.listSkills()
	case "search", "find":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: skill search <query>")
		}
		return h.searchSkills(ctx, strings.Join(args[1:], " "))
	case "install", "add":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: skill install <name>")
		}
		return h.installSkill(ctx, args[1])
	case "uninstall", "remove", "rm":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: skill uninstall <name>")
		}
		return h.uninstallSkill(args[1])
	case "activate", "enable":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: skill activate <name>")
		}
		return h.activateSkill(args[1])
	case "deactivate", "disable":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: skill deactivate <name>")
		}
		return h.deactivateSkill(args[1])
	case "info", "show":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: skill info <name>")
		}
		return h.showSkillInfo(args[1])
	case "active":
		return h.listActiveSkills()
	case "update":
		return h.updateDatabase(ctx)
	case "categories":
		return h.listCategories()
	case "featured":
		return h.listFeatured()
	case "help":
		return h.showHelp(), nil
	default:
		return "", fmt.Errorf("unknown command: %s\nRun 'skill help' for usage", args[0])
	}
}

func (h *CommandHandler) showHelp() string {
	return `Skill Management Commands:

  skill list              List all available skills
  skill search <query>    Search for skills
  skill install <name>    Install a skill from marketplace
  skill uninstall <name>  Remove an installed skill
  skill activate <name>   Activate a skill for current session
  skill deactivate <name> Deactivate a skill
  skill info <name>       Show skill details
  skill active            List currently active skills
  skill update            Update marketplace index
  skill categories        List available categories
  skill featured          List featured skills
  skill help              Show this help message
`
}

func (h *CommandHandler) listSkills() (string, error) {
	skills := h.Loader.List()
	if len(skills) == 0 {
		return "No skills found. Run 'skill search' to find skills to install.", nil
	}

	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "NAME\tVERSION\tCATEGORY\tACTIVE\tDESCRIPTION")
	fmt.Fprintln(w, "----\t-------\t--------\t------\t-----------")

	for _, skill := range skills {
		active := "no"
		if h.Loader.Registry.IsActive(skill.Metadata.Name) {
			active = "yes"
		}

		desc := skill.Metadata.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			skill.Metadata.Name,
			skill.Metadata.Version,
			skill.Metadata.Category,
			active,
			desc,
		)
	}

	w.Flush()
	return buf.String(), nil
}

func (h *CommandHandler) searchSkills(ctx context.Context, query string) (string, error) {
	results, err := h.Loader.Search(ctx, query)
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return fmt.Sprintf("No skills found matching '%s'", query), nil
	}

	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	fmt.Fprintf(&buf, "Found %d skills matching '%s':\n\n", len(results), query)
	fmt.Fprintln(w, "NAME\tSOURCE\tINSTALLED\tDESCRIPTION")
	fmt.Fprintln(w, "----\t------\t---------\t-----------")

	for _, r := range results {
		installed := "no"
		if r.Installed {
			installed = "yes"
		}

		desc := r.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.Name,
			r.Source,
			installed,
			desc,
		)
	}

	w.Flush()
	return buf.String(), nil
}

func (h *CommandHandler) installSkill(ctx context.Context, name string) (string, error) {
	skill, err := h.Loader.Install(ctx, name)
	if err != nil {
		return "", fmt.Errorf("failed to install %s: %w", name, err)
	}

	return fmt.Sprintf("✓ Installed skill: %s\n  Path: %s\n  Run 'skill activate %s' to enable it",
		skill.Metadata.Name, skill.Path, skill.Metadata.Name), nil
}

func (h *CommandHandler) uninstallSkill(name string) (string, error) {
	if err := h.Loader.Uninstall(name); err != nil {
		return "", fmt.Errorf("failed to uninstall %s: %w", name, err)
	}

	return fmt.Sprintf("✓ Uninstalled skill: %s", name), nil
}

func (h *CommandHandler) activateSkill(name string) (string, error) {
	if err := h.Loader.Activate(name); err != nil {
		return "", fmt.Errorf("failed to activate %s: %w", name, err)
	}

	return fmt.Sprintf("✓ Activated skill: %s", name), nil
}

func (h *CommandHandler) deactivateSkill(name string) (string, error) {
	if err := h.Loader.Deactivate(name); err != nil {
		return "", fmt.Errorf("failed to deactivate %s: %w", name, err)
	}

	return fmt.Sprintf("✓ Deactivated skill: %s", name), nil
}

func (h *CommandHandler) showSkillInfo(name string) (string, error) {
	skill, ok := h.Loader.Registry.Get(name)
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}

	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	fmt.Fprintf(w, "Name:\t%s\n", skill.Metadata.Name)
	fmt.Fprintf(w, "Description:\t%s\n", skill.Metadata.Description)
	fmt.Fprintf(w, "Version:\t%s\n", skill.Metadata.Version)
	fmt.Fprintf(w, "Author:\t%s\n", skill.Metadata.Author)
	fmt.Fprintf(w, "Category:\t%s\n", skill.Metadata.Category)
	fmt.Fprintf(w, "Tags:\t%s\n", strings.Join(skill.Metadata.Tags, ", "))
	fmt.Fprintf(w, "Path:\t%s\n", skill.Path)
	fmt.Fprintf(w, "Active:\t%v\n", h.Loader.Registry.IsActive(skill.Metadata.Name))

	if len(skill.Scripts) > 0 {
		fmt.Fprintf(w, "\nScripts:\n")
		for _, s := range skill.Scripts {
			fmt.Fprintf(w, "  - %s (%s)\n", s.Name, s.Language)
		}
	}

	if len(skill.References) > 0 {
		fmt.Fprintf(w, "\nReferences:\n")
		for _, r := range skill.References {
			fmt.Fprintf(w, "  - %s (%s)\n", r.Name, r.Format)
		}
	}

	w.Flush()

	if skill.Instructions != "" {
		buf.WriteString("\n--- Instructions ---\n")
		buf.WriteString(skill.Instructions)
	}

	return buf.String(), nil
}

func (h *CommandHandler) listActiveSkills() (string, error) {
	skills := h.Loader.GetActiveSkills()
	if len(skills) == 0 {
		return "No active skills. Run 'skill activate <name>' to enable one.", nil
	}

	var buf strings.Builder
	buf.WriteString("Active Skills:\n\n")

	for _, skill := range skills {
		buf.WriteString(fmt.Sprintf("• %s - %s\n", skill.Metadata.Name, skill.Metadata.Description))
	}

	return buf.String(), nil
}

func (h *CommandHandler) updateDatabase(ctx context.Context) (string, error) {
	if err := h.Loader.Update(ctx); err != nil {
		return "", fmt.Errorf("failed to update: %w", err)
	}

	return "✓ Marketplace index updated", nil
}

func (h *CommandHandler) listCategories() (string, error) {
	categories := h.Loader.ListCategories()
	if len(categories) == 0 {
		return "No categories found. Run 'skill update' to fetch the marketplace index.", nil
	}

	var buf strings.Builder
	buf.WriteString("Available Categories:\n\n")
	for _, cat := range categories {
		buf.WriteString(fmt.Sprintf("• %s\n", cat))
	}

	return buf.String(), nil
}

func (h *CommandHandler) listFeatured() (string, error) {
	featured := h.Loader.GetFeatured()
	if len(featured) == 0 {
		return "No featured skills. Run 'skill update' to fetch the marketplace index.", nil
	}

	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	buf.WriteString("Featured Skills:\n\n")
	fmt.Fprintln(w, "NAME\tCATEGORY\tDESCRIPTION")
	fmt.Fprintln(w, "----\t--------\t-----------")

	for _, p := range featured {
		desc := p.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Category, desc)
	}

	w.Flush()
	return buf.String(), nil
}

// FormatSkillForContext formats a skill's instructions for agent context injection.
func FormatSkillForContext(skill *Skill) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("## Skill: %s\n\n", skill.Metadata.Name))

	if skill.Metadata.Description != "" {
		buf.WriteString(fmt.Sprintf("**Description:** %s\n\n", skill.Metadata.Description))
	}

	if len(skill.Scripts) > 0 {
		buf.WriteString("**Available Scripts:**\n")
		for _, s := range skill.Scripts {
			buf.WriteString(fmt.Sprintf("- `%s` (%s)\n", s.Path, s.Language))
		}
		buf.WriteString("\n")
	}

	if skill.Instructions != "" {
		buf.WriteString("### Instructions\n\n")
		buf.WriteString(skill.Instructions)
		buf.WriteString("\n")
	}

	return buf.String()
}

// PrintSkillTable prints skills in a formatted table to stdout.
func PrintSkillTable(skills []*Skill) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tCATEGORY\tDESCRIPTION")
	fmt.Fprintln(w, "----\t-------\t--------\t-----------")

	for _, s := range skills {
		desc := s.Metadata.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			s.Metadata.Name, s.Metadata.Version, s.Metadata.Category, desc)
	}
	w.Flush()
}
