package analytics

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

var (
	sensitiveKeyPattern = regexp.MustCompile(`(?i)(token|secret|password|api[_-]?key|authorization|cookie|credential|oauth|refresh|access|private)`)
	secretValuePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)bearer\s+[a-z0-9._\-]{12,}`),
		regexp.MustCompile(`sk-[a-z0-9]{16,}`),
		regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	}
)

var knownSecretPathFragments = buildSecretPathFragments()

// buildSecretPathFragments returns the lowercase path fragments that mark a
// value as a credential/secret path to be redacted. The SwarmOS root fragment
// is derived from paths.Root() (the single source of truth for the ~/.swarm
// root) rather than a hardcoded literal, so it tracks any root override.
//
// credentials.json and *_oauth.json are included explicitly (audit W3-10): the
// canonical secret files now live under ~/.swarm/config (credentials.json and
// ~/.swarm/config/oauth/<provider>.json), and any path containing them must be
// scrubbed from telemetry.
func buildSecretPathFragments() []string {
	frags := []string{
		".aws/credentials",
		".aws/config",
		".ssh/",
		"credentials.json",
		"oauth.json",
		"_oauth.json",
		"cloud_tokens.json",
		"tui_accounts.json",
		".env",
	}
	// Redact anything under the canonical SwarmOS root (e.g. ~/.swarm/...).
	if root := strings.ToLower(strings.TrimSpace(filepath.Base(paths.Root()))); root != "" {
		frags = append(frags, root+"/")
	}
	return frags
}

type RedactionResult struct {
	Value any
	Flags []string
}

type Redactor struct {
	homeDir string
}

func NewRedactor() *Redactor {
	homeDir, _ := os.UserHomeDir()
	return &Redactor{homeDir: homeDir}
}

func (r *Redactor) Redact(value any) RedactionResult {
	flags := map[string]struct{}{}
	redacted := r.redactValue(value, nil, flags)
	return RedactionResult{Value: redacted, Flags: sortedFlags(flags)}
}

func (r *Redactor) redactValue(value any, keyPath []string, flags map[string]struct{}) any {
	currentKey := ""
	if len(keyPath) > 0 {
		currentKey = keyPath[len(keyPath)-1]
	}
	if currentKey != "" && sensitiveKeyPattern.MatchString(currentKey) {
		flags["sensitive_key:"+strings.ToLower(currentKey)] = struct{}{}
		return "[REDACTED:secret]"
	}

	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = r.redactValue(child, append(keyPath, key), flags)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = r.redactValue(typed[i], keyPath, flags)
		}
		return result
	case string:
		return r.redactString(typed, flags)
	default:
		return value
	}
}

func (r *Redactor) redactString(value string, flags map[string]struct{}) string {
	normalized := value
	if r.homeDir != "" {
		normalized = strings.ReplaceAll(normalized, r.homeDir, "~")
	}
	normalized = regexp.MustCompile(`/Users/[^/\s]+`).ReplaceAllString(normalized, "~")
	normalized = regexp.MustCompile(`/home/[^/\s]+`).ReplaceAllString(normalized, "~")
	lower := strings.ToLower(normalized)
	for _, fragment := range knownSecretPathFragments {
		if strings.Contains(lower, fragment) {
			flags["credential_path"] = struct{}{}
			return "[REDACTED:path]"
		}
	}
	for _, pattern := range secretValuePatterns {
		if pattern.MatchString(normalized) {
			flags["secret_value"] = struct{}{}
			return "[REDACTED:secret]"
		}
	}
	return normalized
}

func sortedFlags(flags map[string]struct{}) []string {
	if len(flags) == 0 {
		return nil
	}
	result := make([]string, 0, len(flags))
	for flag := range flags {
		result = append(result, flag)
	}
	slices.Sort(result)
	return result
}
