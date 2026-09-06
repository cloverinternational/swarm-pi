package tools

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// Dangerous command patterns
var (
	// rmPattern matches rm commands
	rmPattern = regexp.MustCompile(`\brm\s+(-[a-zA-Z]*\s+)*`)

	// deletePattern matches delete keywords
	deletePattern = regexp.MustCompile(`\bdelete\b`)

	// chmodDangerousPattern matches chmod 777
	chmodDangerousPattern = regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]*\s+)*777\b`)

	// curlPipePattern matches curl piped to shell
	curlPipePattern = regexp.MustCompile(`\b(curl|wget)\s+.*\|\s*(sh|bash)\b`)

	// ddPattern matches dd commands
	ddPattern = regexp.MustCompile(`\bdd\s+`)

	// mkfsPattern matches mkfs commands
	mkfsPattern = regexp.MustCompile(`\bmkfs\b`)
)

// Sensitive file patterns
var sensitiveFilePatterns = []string{
	".env",
	".env.local",
	".env.production",
	".env.development",
	"credentials.json",
	"credentials.yaml",
	"credentials.yml",
	"secrets.json",
	"secrets.yaml",
	"secrets.yml",
	".aws/credentials",
	".npmrc",
	".pypirc",
}

// IsDangerousAction checks if a tool action is considered dangerous.
func IsDangerousAction(tool string, params map[string]any) bool {
	var normalized string = strings.ToLower(strings.TrimSpace(tool))
	switch normalized {
	case "bash", "bash_execute":
		return IsDangerousBashCommand(params)
	case "file_write", "file_delete", "apply_patch", "file_edit", "write", "edit":
		return IsDangerousFileOperation(params, "")
	case "web_fetch", "web_search", "webfetch", "websearch", "network_access":
		return IsDangerousNetworkRequest(params)
	default:
		return false
	}
}

// IsDangerousBashCommand checks if a bash command is dangerous.
func IsDangerousBashCommand(params map[string]any) bool {
	command, ok := params["command"].(string)
	if !ok || command == "" {
		return false
	}

	commandLower := strings.ToLower(command)

	// Check for rm commands
	if rmPattern.MatchString(commandLower) {
		return true
	}

	// Check for delete keyword
	if deletePattern.MatchString(commandLower) {
		return true
	}

	// Check for dangerous chmod
	if chmodDangerousPattern.MatchString(commandLower) {
		return true
	}

	// Check for curl/wget piped to shell
	if curlPipePattern.MatchString(commandLower) {
		return true
	}

	// Check for dd command
	if ddPattern.MatchString(commandLower) {
		return true
	}

	// Check for mkfs command
	if mkfsPattern.MatchString(commandLower) {
		return true
	}

	return false
}

// IsDangerousFileOperation checks if a file operation is dangerous.
func IsDangerousFileOperation(params map[string]any, projectRoot string) bool {
	// Get the file path from params
	path := ""
	if p, ok := params["path"].(string); ok {
		path = p
	} else if p, ok := params["file_path"].(string); ok {
		path = p
	}

	if path == "" {
		return false
	}

	// Get operation type
	operation := "write" // Default to write
	if op, ok := params["operation"].(string); ok {
		operation = op
	}

	// Read operations are not dangerous
	if operation == "read" {
		return false
	}

	// Check for writes outside project root
	if projectRoot != "" {
		absPath, err := filepath.Abs(path)
		if err == nil {
			absProject, err := filepath.Abs(projectRoot)
			if err == nil {
				if !strings.HasPrefix(absPath, absProject) {
					return true
				}
			}
		}
	}

	// Check for writes to system directories
	systemPaths := []string{"/etc/", "/usr/", "/bin/", "/sbin/", "/root/", "/var/"}
	for _, sysPath := range systemPaths {
		if strings.HasPrefix(path, sysPath) {
			return true
		}
	}

	// Check for sensitive files
	pathLower := strings.ToLower(path)
	baseName := strings.ToLower(filepath.Base(path))

	for _, pattern := range sensitiveFilePatterns {
		patternLower := strings.ToLower(pattern)
		// Check if basename matches pattern
		if baseName == patternLower || baseName == filepath.Base(patternLower) {
			return true
		}
		// Check if path ends with pattern
		if strings.HasSuffix(pathLower, patternLower) {
			return true
		}
	}

	return false
}

// IsDangerousNetworkRequest checks if a network request is dangerous.
func IsDangerousNetworkRequest(params map[string]any) bool {
	urlStr, ok := params["url"].(string)
	if !ok || urlStr == "" {
		return false
	}

	// Parse the URL
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	host := strings.ToLower(parsed.Hostname())

	// Localhost is safe
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}

	// Everything else is potentially dangerous (external network)
	return true
}
