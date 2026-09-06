package commands

import (
	"strings"

	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

var reasoningEffortOrder = []string{
	sdkprovider.ReasoningEffortNone,
	sdkprovider.ReasoningEffortMinimal,
	sdkprovider.ReasoningEffortLow,
	sdkprovider.ReasoningEffortMedium,
	sdkprovider.ReasoningEffortHigh,
	sdkprovider.ReasoningEffortXHigh,
	sdkprovider.ReasoningEffortMax,
}

func normalizeReasoningEfforts(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	allowed := make(map[string]bool, len(reasoningEffortOrder))
	for _, value := range reasoningEffortOrder {
		allowed[value] = true
	}

	normalizedSet := make(map[string]bool, len(values))
	for _, value := range values {
		normalized := sdkprovider.NormalizeReasoningEffortSetting(value)
		if normalized == sdkprovider.ReasoningEffortAuto {
			continue
		}
		if !allowed[normalized] {
			continue
		}
		normalizedSet[normalized] = true
	}

	if len(normalizedSet) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(normalizedSet))
	for _, effort := range reasoningEffortOrder {
		if normalizedSet[effort] {
			normalized = append(normalized, effort)
		}
	}
	return normalized
}

// InferReasoningEfforts infers supported reasoning effort values when metadata is absent.
func InferReasoningEfforts(apiType string, providerName string, modelID string) []string {
	normalizedAPIType := strings.ToLower(strings.TrimSpace(apiType))
	if normalizedAPIType != "" && normalizedAPIType != "openai" {
		return nil
	}
	if normalizedAPIType == "" {
		normalizedProvider := strings.ToLower(strings.TrimSpace(providerName))
		if normalizedProvider != "openai" && normalizedProvider != "codex" {
			return nil
		}
	}

	normalizedModel := strings.ToLower(strings.TrimSpace(modelID))
	if idx := strings.LastIndex(normalizedModel, "/"); idx != -1 {
		normalizedModel = normalizedModel[idx+1:]
	}

	if !strings.HasPrefix(normalizedModel, "gpt-5") {
		return nil
	}

	efforts := []string{
		sdkprovider.ReasoningEffortNone,
		sdkprovider.ReasoningEffortMinimal,
		sdkprovider.ReasoningEffortLow,
		sdkprovider.ReasoningEffortMedium,
		sdkprovider.ReasoningEffortHigh,
	}
	// xhigh is accepted by the codex-suffixed variants and the numbered
	// families the Codex catalog lists it for (gpt-5.2/5.4/5.5/5.6) — NOT
	// by plain gpt-5/gpt-5.1 on the public API, which reject it. Catalog
	// metadata written by the Codex OAuth refresher overrides this
	// inference entirely (see ResolveReasoningEffortsForModel priority).
	if strings.Contains(normalizedModel, "codex") ||
		strings.HasPrefix(normalizedModel, "gpt-5.2") ||
		strings.HasPrefix(normalizedModel, "gpt-5.4") ||
		strings.HasPrefix(normalizedModel, "gpt-5.5") ||
		strings.HasPrefix(normalizedModel, "gpt-5.6") {
		efforts = append(efforts, sdkprovider.ReasoningEffortXHigh)
	}
	// The gpt-5.6 family additionally supports the max effort tier.
	if strings.HasPrefix(normalizedModel, "gpt-5.6") {
		efforts = append(efforts, sdkprovider.ReasoningEffortMax)
	}
	return efforts
}

// ResolveReasoningEffortsForModel resolves supported non-auto reasoning effort values.
// Priority:
// 1. explicit reasoning_efforts list
// 2. explicit supports_reasoning_effort=false
// 3. inferred support from provider/model identity
func ResolveReasoningEffortsForModel(apiType string, providerName string, modelID string, supportsReasoningEffort *bool, reasoningEfforts []string) []string {
	if supportsReasoningEffort != nil && !*supportsReasoningEffort {
		return nil
	}

	explicitEfforts := normalizeReasoningEfforts(reasoningEfforts)
	if len(explicitEfforts) > 0 {
		return explicitEfforts
	}

	inferred := InferReasoningEfforts(apiType, providerName, modelID)
	if len(inferred) > 0 {
		return inferred
	}

	if supportsReasoningEffort != nil && *supportsReasoningEffort {
		return []string{
			sdkprovider.ReasoningEffortLow,
			sdkprovider.ReasoningEffortMedium,
			sdkprovider.ReasoningEffortHigh,
		}
	}

	return nil
}

// AvailableReasoningEffortSettings returns persisted setting values in display order.
func AvailableReasoningEffortSettings(apiType string, providerName string, modelID string, supportsReasoningEffort *bool, reasoningEfforts []string) []string {
	resolved := ResolveReasoningEffortsForModel(apiType, providerName, modelID, supportsReasoningEffort, reasoningEfforts)
	if len(resolved) == 0 {
		return []string{sdkprovider.ReasoningEffortAuto}
	}

	options := make([]string, 0, len(resolved)+1)
	options = append(options, sdkprovider.ReasoningEffortAuto)
	options = append(options, resolved...)
	return options
}
