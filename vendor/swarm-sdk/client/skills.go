package client

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/skilltools"
)

func registerSkillsTool(c *Client, parent tools.Registry, workspace string, extraPaths []string) error {
	loader := skills.NewLoader(paths.SkillsDir())

	// Project skills are intentionally added only when a workspace is known.
	// This mirrors the TUI's .claude/skills convention without making the
	// process working directory an implicit project boundary.
	if workspace != "" {
		loader.Registry.AddSearchPath(filepath.Join(workspace, ".claude", "skills"))
	}
	for _, path := range extraPaths {
		if path == "" {
			continue
		}
		loader.Registry.AddSearchPath(path)
	}
	if err := loader.Initialize(context.Background()); err != nil {
		return fmt.Errorf("initialize skill loader: %w", err)
	}

	tool, err := skilltools.NewSkillTool(skilltools.SkillToolConfig{
		Registry: loader.Registry,
		Logger:   c.logger,
		Tracer:   c.tracer,
		SessionIDGetter: func() string {
			return c.sessionID
		},
	})
	if err != nil {
		return fmt.Errorf("create skill tool: %w", err)
	}
	if err := parent.Register(tool); err != nil {
		return fmt.Errorf("register Skill tool: %w", err)
	}

	// Keep the registry available to the existing client skill introspection
	// APIs. The loader owns the registry for the lifetime of the client.
	c.skillRegistry = loader.Registry
	return nil
}
