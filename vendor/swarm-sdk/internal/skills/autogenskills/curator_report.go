package autogenskills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DefaultCuratorRenameLimit = 10

// CuratorInventoryEntry is the stable identity of one active or archived skill.
type CuratorInventoryEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// CuratorInventory is a point-in-time filesystem inventory.
type CuratorInventory struct {
	CapturedAt time.Time                        `json:"captured_at"`
	Active     map[string]CuratorInventoryEntry `json:"active"`
	Archived   map[string]CuratorInventoryEntry `json:"archived"`
}

// CuratorRemovalClassification records exactly one disposition for a removed skill.
type CuratorRemovalClassification struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // "consolidated" or "pruned"
	Into   string `json:"into,omitempty"`
	Reason string `json:"reason,omitempty"`
	Source string `json:"source"`
}

// CuratorStructuredSummary is the exact machine-readable fenced YAML schema.
type CuratorStructuredSummary struct {
	Consolidations []CuratorStructuredConsolidation
	Prunings       []CuratorStructuredPruning
}

type CuratorStructuredConsolidation struct {
	From, Into, Reason string
}

type CuratorStructuredPruning struct {
	Name, Reason string
}

// CuratorRenameSummary is deliberately bounded for terminal and Markdown use.
type CuratorRenameSummary struct {
	Total     int                            `json:"total"`
	Truncated int                            `json:"truncated"`
	Items     []CuratorRemovalClassification `json:"items"`
}

// CuratorRunReportOptions supplies metadata collected by a future CLI/orchestrator.
type CuratorRunReportOptions struct {
	AutogenDir  string
	OutputDir   string
	Before      CuratorInventory
	After       CuratorInventory
	State       *CuratorState
	ModelOutput string
	Model       string
	Provider    string
	TurnCount   int
	TokenCount  int
	Error       string
	StartedAt   time.Time
	FinishedAt  time.Time
	RenameLimit int
}

// CuratorRunReport is serialized losslessly to run.json.
type CuratorRunReport struct {
	StartedAt     time.Time                      `json:"started_at"`
	FinishedAt    time.Time                      `json:"finished_at"`
	Model         string                         `json:"model"`
	Provider      string                         `json:"provider"`
	TurnCount     int                            `json:"turn_count"`
	TokenCount    int                            `json:"token_count"`
	Error         string                         `json:"error"`
	ModelOutput   string                         `json:"model_output,omitempty"`
	Before        CuratorInventory               `json:"before"`
	After         CuratorInventory               `json:"after"`
	Removed       []CuratorRemovalClassification `json:"removed"`
	Added         []string                       `json:"added"`
	RenameSummary CuratorRenameSummary           `json:"rename_summary"`
}

// SnapshotCuratorInventory inventories skill directories without touching them.
func SnapshotCuratorInventory(autogenDir string) (CuratorInventory, error) {
	inv := CuratorInventory{
		CapturedAt: time.Now().UTC(),
		Active:     make(map[string]CuratorInventoryEntry),
		Archived:   make(map[string]CuratorInventoryEntry),
	}
	entries, err := os.ReadDir(autogenDir)
	if errors.Is(err, os.ErrNotExist) {
		return inv, nil
	}
	if err != nil {
		return inv, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == "archive" || entry.Name() == ".archive" {
			if err := inventorySkillChildren(filepath.Join(autogenDir, entry.Name()), inv.Archived); err != nil {
				return inv, err
			}
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if item, ok, err := inventorySkill(filepath.Join(autogenDir, entry.Name()), entry.Name(), autogenDir); err != nil {
			return inv, err
		} else if ok {
			inv.Active[entry.Name()] = item
		}
	}
	return inv, nil
}

func inventorySkillChildren(root string, dest map[string]CuratorInventoryEntry) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	base := filepath.Dir(root)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		item, ok, err := inventorySkill(filepath.Join(root, entry.Name()), entry.Name(), base)
		if err != nil {
			return err
		}
		if ok {
			dest[entry.Name()] = item
		}
	}
	return nil
}

func inventorySkill(root, name, relativeTo string) (CuratorInventoryEntry, bool, error) {
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
		return CuratorInventoryEntry{}, false, nil
	} else if err != nil {
		return CuratorInventoryEntry{}, false, err
	}
	hash := sha256.New()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("curator report: symlink in skill %q", name)
		}
		if entry.Type().IsRegular() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return CuratorInventoryEntry{}, false, err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		raw, err := os.ReadFile(path)
		if err != nil {
			return CuratorInventoryEntry{}, false, err
		}
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		hash.Write(raw)
	}
	rel, _ := filepath.Rel(relativeTo, root)
	return CuratorInventoryEntry{Name: name, Path: filepath.ToSlash(rel), Digest: hex.EncodeToString(hash.Sum(nil))}, true, nil
}

var structuredFence = regexp.MustCompile("(?is)```ya?ml[ \\t]*\\r?\\n(.*?)\\r?\\n```")

// ParseCuratorStructuredSummary parses only the required fenced YAML subset.
// It intentionally rejects general YAML features, duplicate fields and malformed
// indentation so an untrusted model response cannot be interpreted ambiguously.
func ParseCuratorStructuredSummary(output string) (CuratorStructuredSummary, error) {
	var result CuratorStructuredSummary
	match := structuredFence.FindStringSubmatch(output)
	if match == nil {
		return result, errors.New("curator report: structured YAML block not found")
	}
	section := ""
	var cons *CuratorStructuredConsolidation
	var prune *CuratorStructuredPruning
	seen := map[string]bool{}
	flush := func() error {
		if cons != nil {
			if cons.From == "" || cons.Into == "" {
				return errors.New("curator report: consolidation requires from and into")
			}
			result.Consolidations = append(result.Consolidations, *cons)
			cons = nil
		}
		if prune != nil {
			if prune.Name == "" {
				return errors.New("curator report: pruning requires name")
			}
			result.Prunings = append(result.Prunings, *prune)
			prune = nil
		}
		seen = map[string]bool{}
		return nil
	}
	for _, raw := range strings.Split(strings.ReplaceAll(match[1], "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if line == "consolidations:" || line == "consolidations: []" || line == "prunings:" || line == "prunings: []" {
			if err := flush(); err != nil {
				return CuratorStructuredSummary{}, err
			}
			parts := strings.SplitN(line, ":", 2)
			section = parts[0]
			continue
		}
		if !strings.HasPrefix(line, "  ") || section == "" {
			return CuratorStructuredSummary{}, fmt.Errorf("curator report: malformed YAML line %q", raw)
		}
		trimmed := strings.TrimSpace(line)
		newItem := strings.HasPrefix(trimmed, "- ")
		if newItem {
			if err := flush(); err != nil {
				return CuratorStructuredSummary{}, err
			}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if section == "consolidations" {
				cons = &CuratorStructuredConsolidation{}
			} else {
				prune = &CuratorStructuredPruning{}
			}
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(value) == "" {
			return CuratorStructuredSummary{}, fmt.Errorf("curator report: malformed YAML field %q", raw)
		}
		key, value = strings.TrimSpace(key), curatorYAMLScalar(strings.TrimSpace(value))
		if seen[key] {
			return CuratorStructuredSummary{}, fmt.Errorf("curator report: duplicate YAML field %q", key)
		}
		seen[key] = true
		switch {
		case cons != nil && key == "from":
			cons.From = value
		case cons != nil && key == "into":
			cons.Into = value
		case cons != nil && key == "reason":
			cons.Reason = value
		case prune != nil && key == "name":
			prune.Name = value
		case prune != nil && key == "reason":
			prune.Reason = value
		default:
			return CuratorStructuredSummary{}, fmt.Errorf("curator report: unexpected YAML field %q", key)
		}
	}
	if err := flush(); err != nil {
		return CuratorStructuredSummary{}, err
	}
	return result, nil
}

func curatorYAMLScalar(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}

// ClassifyCuratorRemovals classifies every before-minus-after active skill once.
func ClassifyCuratorRemovals(before, after CuratorInventory, state *CuratorState, modelOutput string) ([]CuratorRemovalClassification, error) {
	structured, _ := ParseCuratorStructuredSummary(modelOutput) // malformed/missing blocks fall back safely
	model := make(map[string]CuratorRemovalClassification)
	for _, item := range structured.Consolidations {
		if _, exists := model[item.From]; exists {
			return nil, fmt.Errorf("curator report: duplicate classification for %q", item.From)
		}
		model[item.From] = CuratorRemovalClassification{Name: item.From, Kind: "consolidated", Into: item.Into, Reason: item.Reason, Source: "model"}
	}
	for _, item := range structured.Prunings {
		if _, exists := model[item.Name]; exists {
			return nil, fmt.Errorf("curator report: duplicate classification for %q", item.Name)
		}
		model[item.Name] = CuratorRemovalClassification{Name: item.Name, Kind: "pruned", Reason: item.Reason, Source: "model"}
	}
	var removed []string
	for name := range before.Active {
		if _, exists := after.Active[name]; !exists {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	out := make([]CuratorRemovalClassification, 0, len(removed))
	for _, name := range removed {
		var item CuratorRemovalClassification
		meta, authoritative := SkillMeta{}, false
		if state != nil {
			meta, authoritative = state.SkillStates[name]
		}
		if authoritative && meta.AbsorbedInto != "" {
			item = CuratorRemovalClassification{Name: name, Kind: "consolidated", Into: meta.AbsorbedInto, Reason: meta.ArchiveReason, Source: "curator-state"}
		} else if authoritative && meta.ArchiveReason != "" {
			item = CuratorRemovalClassification{Name: name, Kind: "pruned", Reason: meta.ArchiveReason, Source: "curator-state"}
		} else if candidate, ok := model[name]; ok {
			item = candidate
		} else {
			return nil, fmt.Errorf("curator report: removed skill %q has no archive provenance or structured summary entry", name)
		}
		if item.Kind == "consolidated" {
			if item.Into == name {
				return nil, fmt.Errorf("curator report: %q cannot consolidate into itself", name)
			}
			if _, exists := after.Active[item.Into]; !exists {
				return nil, fmt.Errorf("curator report: consolidation target %q for %q does not exist", item.Into, name)
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// WriteCuratorRunReport emits run.json and REPORT.md, returning the JSON model.
func WriteCuratorRunReport(options CuratorRunReportOptions) (CuratorRunReport, error) {
	removed, err := ClassifyCuratorRemovals(options.Before, options.After, options.State, options.ModelOutput)
	if err != nil {
		return CuratorRunReport{}, err
	}
	var added []string
	for name := range options.After.Active {
		if _, exists := options.Before.Active[name]; !exists {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	limit := options.RenameLimit
	if limit <= 0 {
		limit = DefaultCuratorRenameLimit
	}
	var renames []CuratorRemovalClassification
	for _, item := range removed {
		if item.Kind == "consolidated" {
			renames = append(renames, item)
		}
	}
	total := len(renames)
	if len(renames) > limit {
		renames = renames[:limit]
	}
	report := CuratorRunReport{
		StartedAt: options.StartedAt, FinishedAt: options.FinishedAt,
		Model: options.Model, Provider: options.Provider, TurnCount: options.TurnCount,
		TokenCount: options.TokenCount, Error: options.Error, ModelOutput: options.ModelOutput,
		Before: options.Before, After: options.After, Removed: removed, Added: added,
		RenameSummary: CuratorRenameSummary{Total: total, Truncated: total - len(renames), Items: renames},
	}
	dir := options.OutputDir
	if dir == "" {
		stamp := report.FinishedAt
		if stamp.IsZero() {
			stamp = time.Now()
		}
		dir = filepath.Join(filepath.Dir(options.AutogenDir), ".curator_logs", stamp.UTC().Format("20060102-150405"))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return CuratorRunReport{}, err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return CuratorRunReport{}, err
	}
	if err := writeAtomic(filepath.Join(dir, "run.json"), append(raw, '\n')); err != nil {
		return CuratorRunReport{}, err
	}
	if err := writeAtomic(filepath.Join(dir, "REPORT.md"), []byte(renderCuratorReport(report))); err != nil {
		return CuratorRunReport{}, err
	}
	return report, nil
}

func renderCuratorReport(report CuratorRunReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Curator run report\n\n- Provider: `%s`\n- Model: `%s`\n- Turns: %d\n- Tokens: %d\n- Error: %s\n\n",
		report.Provider, report.Model, report.TurnCount, report.TokenCount, emptyDash(report.Error))
	b.WriteString("## Removed skills\n\n")
	if len(report.Removed) == 0 {
		b.WriteString("None.\n")
	}
	for _, item := range report.Removed {
		if item.Kind == "consolidated" {
			fmt.Fprintf(&b, "- `%s` → `%s` (%s)\n", item.Name, item.Into, emptyDash(item.Reason))
		} else {
			fmt.Fprintf(&b, "- `%s` pruned (%s)\n", item.Name, emptyDash(item.Reason))
		}
	}
	fmt.Fprintf(&b, "\n## Rename summary\n\nShowing %d of %d", len(report.RenameSummary.Items), report.RenameSummary.Total)
	if report.RenameSummary.Truncated > 0 {
		fmt.Fprintf(&b, " (%d omitted)", report.RenameSummary.Truncated)
	}
	b.WriteString(".\n")
	return b.String()
}

func emptyDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
