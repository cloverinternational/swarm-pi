package provider

import "strings"

// unsupportedSchemaKeys are JSON Schema meta-fields that many LLM provider
// APIs (Fireworks, Gemini, etc.) reject when present in tool parameter schemas.
var unsupportedSchemaKeys = map[string]bool{
	"$schema":         true,
	"examples":        true,
	"show_whitespace": true,
}

// NormalizeToolParametersSchema coerces JSON Schemas into a provider-friendly
// shape. Some backends reject object schemas that omit an explicit properties
// map, even when the tool takes no arguments. It also strips meta-fields like
// "$schema" that are not understood by most LLM provider APIs (e.g. Fireworks
// returns HTTP 400 when "$schema" is present in a tool parameter schema).
func NormalizeToolParametersSchema(schema any) any {
	switch value := schema.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(value)+1)
		for key, nested := range value {
			if unsupportedSchemaKeys[key] {
				continue
			}
			normalized[key] = NormalizeToolParametersSchema(nested)
		}

		schemaType, _ := normalized["type"].(string)
		if strings.EqualFold(strings.TrimSpace(schemaType), "object") {
			if properties, exists := normalized["properties"]; !exists || properties == nil {
				normalized["properties"] = map[string]any{}
			}
		}

		return normalized
	case []any:
		normalized := make([]any, len(value))
		for index, nested := range value {
			normalized[index] = NormalizeToolParametersSchema(nested)
		}
		return normalized
	default:
		return schema
	}
}
