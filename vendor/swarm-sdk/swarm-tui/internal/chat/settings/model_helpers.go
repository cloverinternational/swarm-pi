package settings

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// ---------------------------------------------------------------------------
// Model capability detection — future-proof for any Claude release after 4.6
// ---------------------------------------------------------------------------

// parsedModelVersion holds a parsed Claude model version.
type parsedModelVersion struct {
	family string // "opus", "sonnet", "haiku"
	major  int
	minor  int
}

var modelVersionRe = regexp.MustCompile(`claude-(\w+)-(\d+)-(\d+)`)

func parseClaudeModelVersion(model string) *parsedModelVersion {
	matches := modelVersionRe.FindStringSubmatch(strings.ToLower(model))
	if len(matches) < 4 {
		return nil
	}
	major, _ := strconv.Atoi(matches[2])
	minor, _ := strconv.Atoi(matches[3])
	return &parsedModelVersion{family: matches[1], major: major, minor: minor}
}

// modelSupportsAdaptiveThinking returns true for Opus 4.6+ (and any future
// model whose version number is >= 4.6 in the opus family, or any family
// whose major version exceeds 4).
func modelSupportsAdaptiveThinking(modelID string) bool {
	v := parseClaudeModelVersion(modelID)
	if v == nil {
		return false
	}
	// Opus 4.6 is the first to support adaptive. All future models beyond
	// version 4.6 in *any* family will very likely support it too, so we
	// treat major>4 as adaptive-capable regardless of family.
	if v.major > 4 {
		return true
	}
	if v.major == 4 && v.minor >= 6 {
		// At version 4.x only Opus supports it for now
		return v.family == "opus"
	}
	return false
}

// modelSupportsExtendedThinking returns true for any Claude 3+ model.
func modelSupportsExtendedThinking(modelID string) bool {
	mid := strings.ToLower(modelID)
	if strings.Contains(mid, "gemini-2.5") || strings.Contains(mid, "gemini-3") {
		return true
	}
	v := parseClaudeModelVersion(modelID)
	if v == nil {
		// Not a recognised Claude model string — unknown, but we still allow
		// the user to enable thinking (they know best).
		return true
	}
	return v.major >= 3
}

// modelThinkingEfforts returns the selectable effort values for a given model.
// Opus 4.6+ gets "max"; all models get "", low, medium, high.
func modelThinkingEfforts(modelID string) []string {
	mid := strings.ToLower(modelID)
	if strings.Contains(mid, "gemini-3") {
		return []string{"", "minimal", "low", "medium", "high"}
	}
	base := []string{"", "low", "medium", "high"}
	if modelSupportsAdaptiveThinking(modelID) {
		return append(base, "max")
	}
	return base
}

// modelThinkingModeLabel returns a short human label for the thinking mode
// that the SDK will choose for this model.
func modelThinkingModeLabel(modelID string) string {
	mid := strings.ToLower(modelID)
	if strings.Contains(mid, "gemini-3") {
		return "manual (level)"
	}
	if strings.Contains(mid, "gemini-2.5") {
		return "manual (budget)"
	}
	if modelSupportsAdaptiveThinking(modelID) {
		return "adaptive"
	}
	return "manual (budget)"
}

func supportsOAuth(providerType string) bool {
	var normalized string = strings.ToLower(strings.TrimSpace(providerType))
	switch normalized {
	case "openai", "anthropic", "gemini", "xai":
		return true
	default:
		return false
	}
}

func resolveAPIType(providerType string) string {
	var normalized string = strings.ToLower(strings.TrimSpace(providerType))
	switch normalized {
	case "openai":
		return "openai"
	case "anthropic":
		return "anthropic"
	case "minimax":
		return "anthropic"
	case "gemini":
		return "gemini"
	case "xai":
		return "xai"
	case "cerebras":
		return "openai-compatible"
	case "fireworks":
		return "openai-compatible"
	case "openrouter":
		return "openai-compatible"
	case "groq":
		return "openai-compatible"
	case "together", "together-ai", "togetherai":
		return "openai-compatible"
	case "deepseek":
		return "openai-compatible"
	case "perplexity":
		return "openai-compatible"
	case "exa":
		return "exa"
	default:
		return "openai-compatible"
	}
}

func resolveProviderTypeFromAPI(apiType string) string {
	var normalized string = strings.ToLower(strings.TrimSpace(apiType))
	switch normalized {
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "gemini":
		return "Gemini"
	case "xai":
		return "xAI / Grok"
	case "exa":
		return "Exa"
	case "openai-compatible":
		return "Custom"
	default:
		return "Custom"
	}
}

func normalizeAuthType(authType string) string {
	var normalized string = strings.ToLower(strings.TrimSpace(authType))
	switch normalized {
	case "oauth", "api_key":
		return normalized
	default:
		return ""
	}
}

func defaultAuthTypeForProvider(providerType string) string {
	if supportsOAuth(providerType) {
		return "oauth"
	}
	return "api_key"
}

func coerceAuthType(providerType string, authType string) string {
	var normalized string = normalizeAuthType(authType)
	if normalized == "oauth" {
		return "oauth"
	}
	if !supportsOAuth(providerType) {
		return "api_key"
	}
	if normalized == "" {
		return defaultAuthTypeForProvider(providerType)
	}
	return normalized
}

func toggleAuthType(authType string) string {
	var normalized string = normalizeAuthType(authType)
	if normalized == "oauth" {
		return "api_key"
	}
	return "oauth"
}

// loadProviders loads provider configs
func loadProviders() []commands.Provider {
	cm, err := commands.NewConfigManager()
	if err != nil {
		return []commands.Provider{}
	}

	providerConfigs, err := cm.LoadProviders()
	if err != nil {
		return []commands.Provider{}
	}

	providers := make([]commands.Provider, len(providerConfigs))
	for i, pc := range providerConfigs {
		models := make([]commands.ModelInfo, len(pc.Models))
		for j, mc := range pc.Models {
			var supportsReasoningEffort *bool
			if mc.SupportsReasoningEffort != nil {
				cloned := *mc.SupportsReasoningEffort
				supportsReasoningEffort = &cloned
			}
			models[j] = commands.ModelInfo{
				ID:                      mc.ID,
				DisplayName:             mc.DisplayName,
				Context:                 mc.Context,
				ContextWindow:           mc.ContextWindow,
				Description:             mc.Description,
				SupportsReasoningEffort: supportsReasoningEffort,
				ReasoningEfforts:        append([]string(nil), mc.ReasoningEfforts...),
				Diffusion:               mc.Diffusion,
			}
		}
		providers[i] = commands.Provider{
			Name:            pc.Name,
			DisplayName:     pc.DisplayName,
			Color:           pc.Color,
			Type:            pc.Type,
			APIType:         pc.APIType,
			BaseURL:         pc.BaseURL,
			HTTPMaxRetries:  pc.HTTPMaxRetries,
			APIKeySecretRef: pc.APIKeySecretRef,
			Source:          pc.Source,
			Models:          models,
			Available:       pc.Available,
		}
	}

	return providers
}

// ---------------------------------------------------------------------------
// Standalone list / filter / match types and helpers
// ---------------------------------------------------------------------------

type listResults struct {
	Indices    []int
	Scores     map[int]int
	Highlights map[int][]int
}

type modelGroup struct {
	Key       string
	Title     string
	Indices   []int
	Collapsed bool
}

type groupedResults struct {
	Groups     []modelGroup
	Indices    []int
	Scores     map[int]int
	Highlights map[int][]int
}

func indexOfInt(values []int, needle int) int {
	for i, value := range values {
		if value == needle {
			return i
		}
	}
	return -1
}

func matchesProviderFilter(filter string, providerName string, providerDisplay string) bool {
	var normalized string = strings.ToLower(strings.TrimSpace(filter))
	if normalized == "" {
		return true
	}
	var name string = strings.ToLower(strings.TrimSpace(providerName))
	var display string = strings.ToLower(strings.TrimSpace(providerDisplay))
	return strings.Contains(name, normalized) || strings.Contains(display, normalized)
}

func matchesContextRange(context int, min int, max int) bool {
	if min == 0 && max == 0 {
		return true
	}
	if context <= 0 {
		return false
	}
	if min > 0 && context < min {
		return false
	}
	if max > 0 && context > max {
		return false
	}
	return true
}

func matchesTags(tags map[commands.ModelTag]bool, required map[commands.ModelTag]bool) bool {
	if len(required) == 0 {
		return true
	}
	for tag := range required {
		if !tags[tag] {
			return false
		}
	}
	return true
}

func matchCandidate(query string, candidate string, label string) (int, []int, bool) {
	if strings.TrimSpace(query) == "" {
		return 0, nil, true
	}
	var score int
	var ok bool
	score, _, ok = commands.FuzzyMatchTokens(query, candidate)
	if !ok {
		return 0, nil, false
	}
	if label == "" {
		return score, nil, true
	}
	var highlight []int
	_, highlight, _ = commands.FuzzyMatchTokens(query, label)
	return score, highlight, true
}

func aliasEntryContext(variants []commands.ModelAliasVariant) int {
	var maxContext int
	for _, variant := range variants {
		var context int = commands.ContextFromModel(variant.Model)
		if context > maxContext {
			maxContext = context
		}
	}
	return maxContext
}

func aliasEntryTags(variants []commands.ModelAliasVariant) map[commands.ModelTag]bool {
	var tags map[commands.ModelTag]bool = make(map[commands.ModelTag]bool)
	for _, variant := range variants {
		var variantTags map[commands.ModelTag]bool = commands.InferModelTags(variant.ProviderName, variant.Model)
		for tag := range variantTags {
			tags[tag] = true
		}
	}
	return tags
}

func formatContextWindow(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens >= 1000 && tokens%1000 == 0 {
		return fmt.Sprintf("%dK", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func groupKeyForSelection(groups []modelGroup, selection int) string {
	for _, group := range groups {
		if slices.Contains(group.Indices, selection) {
			return group.Key
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Standalone rendering helpers (no ModelSettings receiver)
// ---------------------------------------------------------------------------

func renderToggleChip(label string, active bool, accent string, th Theme) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 1)
	if active {
		style = style.
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(accent)).
			Bold(true)
	}
	return style.Render(label)
}

func renderNeutralChip(label string, th Theme) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 1).
		Render(label)
}

func joinLeftRight(width int, left string, right string) string {
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if rightWidth == 0 {
		return left
	}
	gap := width - leftWidth - rightWidth
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func renderHeaderRow(width int, left string, right string) string {
	if left == "" && right == "" {
		return ""
	}
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+1 > width {
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	}
	return joinLeftRight(width, left, right)
}

func trimLabelWithHighlights(label string, highlights []int, maxWidth int) (string, []int) {
	if maxWidth <= 0 {
		return "", nil
	}
	runes := []rune(label)
	if len(runes) <= maxWidth {
		return label, highlights
	}
	if maxWidth <= 3 {
		trimmed := string(runes[:maxWidth])
		return trimmed, filterHighlightIndices(highlights, len([]rune(trimmed)))
	}
	trimmed := string(runes[:maxWidth-3]) + "..."
	return trimmed, filterHighlightIndices(highlights, len([]rune(trimmed)))
}

func filterHighlightIndices(indices []int, max int) []int {
	if max <= 0 || len(indices) == 0 {
		return nil
	}
	filtered := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx >= 0 && idx < max {
			filtered = append(filtered, idx)
		}
	}
	return filtered
}

func renderTagChips(tags map[commands.ModelTag]bool, th Theme) string {
	if len(tags) == 0 {
		return ""
	}
	type tagInfo struct {
		tag   commands.ModelTag
		label string
		color string
	}
	ordered := []tagInfo{
		{commands.TagTools, "Tools", palette.Teal},
		{commands.TagCoding, "Code", palette.Info},
		{commands.TagVision, "Vision", palette.Success},
		{commands.TagFast, "Fast", palette.Warning},
		{commands.TagLongContext, "Long", palette.AccentSoft},
	}
	var chips []string
	for _, info := range ordered {
		if !tags[info.tag] {
			continue
		}
		chips = append(chips, renderToggleChip(info.label, true, info.color, th))
	}
	return strings.Join(chips, " ")
}

func renderPanel(content string, width int, th Theme) string {
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Padding(0, 1)
	if width > 0 {
		style = style.Width(width)
	}
	return style.Render(content)
}

func renderCardLine(line string, width int, selected bool, th Theme) string {
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text))
	if selected {
		style = style.
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Bold(true)
	}
	if width > 0 {
		style = style.Width(width)
	}
	return style.Padding(0, 1).Render(line)
}

func renderGroupHeaderLine(label string, width int, active bool, th Theme) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(th.BGLight)).
		Bold(true)
	if active {
		style = style.Foreground(lipgloss.Color(th.Text))
	}
	if width > 0 {
		style = style.Width(width)
	}
	return style.Padding(0, 1).Render(label)
}

func renderDetailCard(title string, lines []string, width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Bold(true)
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(1, 1)
	if width > 0 {
		cardStyle = cardStyle.Width(width)
	}
	return cardStyle.Render(lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), body))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func aliasVariantTags(variants []commands.ModelAliasVariant) map[commands.ModelTag]bool {
	tags := make(map[commands.ModelTag]bool)
	for _, variant := range variants {
		variantTags := commands.InferModelTags(variant.ProviderName, variant.Model)
		for tag := range variantTags {
			tags[tag] = true
		}
	}
	return tags
}

func highlightText(text string, indices []int, baseStyle lipgloss.Style, highlightStyle lipgloss.Style) string {
	if len(indices) == 0 {
		return baseStyle.Render(text)
	}
	var indexSet map[int]bool = make(map[int]bool, len(indices))
	for _, idx := range indices {
		indexSet[idx] = true
	}
	var runes []rune = []rune(text)
	var builder strings.Builder
	for i, r := range runes {
		if indexSet[i] {
			builder.WriteString(highlightStyle.Render(string(r)))
		} else {
			builder.WriteString(baseStyle.Render(string(r)))
		}
	}
	return builder.String()
}
