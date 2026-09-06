package tools

import "strings"

// BuildPermissionContext assembles a normalized context map for permission checks.
func BuildPermissionContext(
	toolName string,
	params map[string]any,
	required []Permission,
	scope ToolScope,
	scopeID string,
	agentID string,
	conversationID string,
	mode string,
) map[string]any {
	ctxData := map[string]any{}

	if toolName != "" {
		ctxData["tool"] = toolName
		ctxData["tool_name"] = toolName
	}
	if params != nil {
		ctxData["params"] = params
	}
	if scope != "" {
		ctxData["scope"] = scope
	}
	if scopeID != "" {
		ctxData["scope_id"] = scopeID
	}
	if agentID != "" {
		ctxData["agent_id"] = agentID
	}
	if conversationID != "" {
		ctxData["conversation_id"] = conversationID
	}
	if mode != "" {
		ctxData["mode"] = mode
	}

	operations := deriveOperations(required, toolName, params)
	if len(operations) > 0 {
		ctxData["operations"] = operations
		if len(operations) == 1 {
			ctxData["operation"] = operations[0]
		}
	}

	if params != nil {
		if path, ok := getStringParam(params, "path"); ok {
			ctxData["path"] = path
		}
		if path, ok := getStringParam(params, "file_path"); ok {
			ctxData["path"] = path
		}
		if paths, ok := getStringSliceParam(params, "paths"); ok {
			ctxData["paths"] = paths
		}
		if command, ok := getStringParam(params, "command"); ok {
			ctxData["command"] = command
		}
		if commands, ok := getStringSliceParam(params, "commands"); ok {
			ctxData["commands"] = commands
		}
		if url, ok := getStringParam(params, "url"); ok {
			ctxData["url"] = url
		}
		if urls, ok := getStringSliceParam(params, "urls"); ok {
			ctxData["urls"] = urls
		}
		if size, ok := extractSizeBytes(params); ok {
			ctxData["file_size"] = size
		}
		// Vault-specific context for vault_exec. Typed tool parameters use
		// credentialId; credential remains a compatibility alias for older
		// dynamic callers.
		if credential, ok := getVaultCredentialParam(params); ok {
			ctxData["credential"] = credential
		}
		if args, ok := getStringSliceParam(params, "args"); ok {
			ctxData["args"] = args
		}
	}

	return ctxData
}

func getVaultCredentialParam(params map[string]any) (string, bool) {
	if credential, ok := getStringParam(params, "credentialId"); ok {
		return credential, true
	}
	return getStringParam(params, "credential")
}

func deriveOperations(required []Permission, toolName string, params map[string]any) []string {
	operations := make([]string, 0, len(required)+2)
	operations = append(operations, operationsFromPermissions(required)...)

	if params != nil {
		if op, ok := getStringParam(params, "operation"); ok {
			operations = append(operations, op)
		}
		if ops, ok := getStringSliceParam(params, "operations"); ok {
			operations = append(operations, ops...)
		}
	}

	tool := strings.ToLower(strings.TrimSpace(toolName))
	if tool == "" {
		return normalizeOperations(operations)
	}

	switch tool {
	case "file_read", "file_read_v2", "read_file":
		operations = append(operations, "read")
	case "file_write", "write_file":
		operations = append(operations, "write")
	case "apply_patch", "file_edit", "edit_file":
		operations = append(operations, "apply_patch", "edit", "write")
	case "delete_file", "file_delete":
		operations = append(operations, "delete")
	case "bash", "shell", "exec", "execute":
		operations = append(operations, "execute")
	case "http", "http_request", "fetch", "web", "browser":
		operations = append(operations, "network")
	case "vault_exec", "vault_list":
		operations = append(operations, "secret_access")
	}

	return normalizeOperations(operations)
}
