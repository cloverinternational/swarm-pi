package compaction

import (
	"fmt"
	"strings"
)

// MCPServerInfo contiene información detallada de un servidor MCP
type MCPServerInfo struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description,omitempty"`
	Tools       []MCPToolInfo     `json:"tools,omitempty"`
	Resources   []MCPResourceInfo `json:"resources,omitempty"`
	Enabled     bool              `json:"enabled"`
}

// MCPToolInfo representa una herramienta expuesta por MCP
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	AutoApprove bool   `json:"auto_approve,omitempty"`
}

// MCPResourceInfo representa un recurso disponible vía MCP
type MCPResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MCPContext contiene el estado completo de MCP para preservación
type MCPContext struct {
	Servers       []MCPServerInfo `json:"servers"`
	ActiveSession string          `json:"active_session,omitempty"`
	RecentCalls   []MCPCallRecord `json:"recent_calls,omitempty"`
}

// MCPCallRecord registra una llamada a herramienta MCP
type MCPCallRecord struct {
	ToolName  string `json:"tool_name"`
	Server    string `json:"server"`
	Timestamp int64  `json:"timestamp"`
	Success   bool   `json:"success"`
}

// MCPConfig configura el comportamiento de MCP en compaction
type MCPConfig struct {
	PreserveServers   bool
	PreserveTools     bool
	PreserveResources bool
	MaxRecentCalls    int
}

// DefaultMCPConfig retorna configuración por defecto
func DefaultMCPConfig() MCPConfig {
	return MCPConfig{
		PreserveServers:   true,
		PreserveTools:     true,
		PreserveResources: false,
		MaxRecentCalls:    10,
	}
}

// BuildMCPContext construye el contexto MCP completo
func BuildMCPContext(
	serverNames []string,
	toolNames []string,
	config MCPConfig,
) *MCPContext {
	ctx := &MCPContext{
		Servers:     make([]MCPServerInfo, 0),
		RecentCalls: make([]MCPCallRecord, 0),
	}

	// Construir información de servidores
	if config.PreserveServers {
		for _, name := range serverNames {
			server := MCPServerInfo{
				Name:    name,
				Enabled: true,
			}
			ctx.Servers = append(ctx.Servers, server)
		}
	}

	return ctx
}

// AddServer añade un servidor al contexto MCP
func (m *MCPContext) AddServer(server MCPServerInfo) {
	m.Servers = append(m.Servers, server)
}

// RecordCall registra una llamada a herramienta MCP
func (m *MCPContext) RecordCall(call MCPCallRecord, maxCalls int) {
	m.RecentCalls = append(m.RecentCalls, call)

	// Mantener solo las últimas N llamadas
	if len(m.RecentCalls) > maxCalls {
		m.RecentCalls = m.RecentCalls[len(m.RecentCalls)-maxCalls:]
	}
}

// GetActiveTools retorna todas las herramientas de servidores activos
func (m *MCPContext) GetActiveTools() []MCPToolInfo {
	var tools []MCPToolInfo
	for _, server := range m.Servers {
		if server.Enabled {
			tools = append(tools, server.Tools...)
		}
	}
	return tools
}

// GetServerByName busca un servidor por nombre
func (m *MCPContext) GetServerByName(name string) *MCPServerInfo {
	for i := range m.Servers {
		if m.Servers[i].Name == name {
			return &m.Servers[i]
		}
	}
	return nil
}

// HasEnabledServers verifica si hay servidores MCP activos
func (m *MCPContext) HasEnabledServers() bool {
	return len(m.Servers) > 0
}

// FormatMCPContext formatea el contexto MCP para el prompt de compaction
func FormatMCPContext(mcpCtx *MCPContext) string {
	if mcpCtx == nil || !mcpCtx.HasEnabledServers() {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("## MCP Context\n\n")

	// Listar servidores conectados
	if len(mcpCtx.Servers) > 0 {
		builder.WriteString("### Connected MCP Servers\n\n")
		for _, server := range mcpCtx.Servers {
			status := "🟢"
			if !server.Enabled {
				status = "🔴"
			}
			builder.WriteString(fmt.Sprintf("%s **%s**", status, server.Name))
			if server.Version != "" {
				builder.WriteString(fmt.Sprintf(" (v%s)", server.Version))
			}
			if server.Description != "" {
				builder.WriteString(fmt.Sprintf(": %s", server.Description))
			}
			builder.WriteString("\n")

			// Listar herramientas disponibles
			if len(server.Tools) > 0 {
				builder.WriteString("  Available tools:\n")
				for _, tool := range server.Tools {
					builder.WriteString(fmt.Sprintf("  - %s", tool.Name))
					if tool.AutoApprove {
						builder.WriteString(" (auto-approved)")
					}
					builder.WriteString("\n")
				}
			}
			builder.WriteString("\n")
		}
	}

	// Llamadas recientes
	if len(mcpCtx.RecentCalls) > 0 {
		builder.WriteString("### Recent MCP Tool Calls\n\n")
		for _, call := range mcpCtx.RecentCalls {
			status := "✅"
			if !call.Success {
				status = "❌"
			}
			builder.WriteString(fmt.Sprintf("%s **%s** from %s\n",
				status, call.ToolName, call.Server))
		}
		builder.WriteString("\n")
	}

	// Instrucciones de reconexión
	builder.WriteString("### Reconnection Info\n\n")
	builder.WriteString("If you need to reconnect to MCP servers after this compaction, " +
		"use the available MCP tools. The following servers were active:\n\n")
	for _, server := range mcpCtx.Servers {
		if server.Enabled {
			builder.WriteString(fmt.Sprintf("- Server: **%s**\n", server.Name))
		}
	}

	return builder.String()
}

// ExtendCompactionContextWithMCP añade contexto MCP al CompactionContext existente
func ExtendCompactionContextWithMCP(
	compCtx *CompactionContext,
	mcpCtx *MCPContext,
) *CompactionContext {
	if compCtx == nil {
		return nil
	}

	// Agregar nombres de servidores MCP
	if mcpCtx != nil {
		for _, server := range mcpCtx.Servers {
			if server.Enabled {
				compCtx.ActiveMCPServers = append(compCtx.ActiveMCPServers, server.Name)
			}
		}

		// Agregar nombres de tools
		for _, tool := range mcpCtx.GetActiveTools() {
			compCtx.RecentMCPTools = append(compCtx.RecentMCPTools, tool.Name)
		}
	}

	return compCtx
}

// IsMCPEnabled verifica si la integración MCP está habilitada
func IsMCPEnabled() bool {
	// Controlado por feature flag o configuración global
	return true
}
