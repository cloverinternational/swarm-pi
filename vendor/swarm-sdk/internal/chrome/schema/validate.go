package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
)

// ValidateInput validates raw JSON against the canonical tool input schema.
func ValidateInput(tool string, raw []byte) error {
	contract, err := Lookup(tool)
	if err != nil {
		return err
	}
	return validateJSON(contract.Input, raw)
}

// ValidateResult validates raw JSON against the canonical successful-result schema.
func ValidateResult(tool string, raw []byte) error {
	contract, err := Lookup(tool)
	if err != nil {
		return err
	}
	return validateJSON(contract.Result, raw)
}

func validateJSON(schema map[string]any, raw []byte) error {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("chrome schema: malformed JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("chrome schema: multiple JSON values")
		}
		return fmt.Errorf("chrome schema: malformed trailing JSON: %w", err)
	}
	if err := validate(schema, value, "$"); err != nil {
		return fmt.Errorf("chrome schema: %w", err)
	}
	return nil
}

func validate(schema map[string]any, value any, path string) error {
	if variants, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, rawVariant := range variants {
			variant, ok := rawVariant.(map[string]any)
			if ok && validate(variant, value, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s must match exactly one schema variant (matched %d)", path, matches)
		}
		return nil
	}
	if constant, ok := schema["const"]; ok && !jsonEqual(value, constant) {
		return fmt.Errorf("%s must equal %v", path, constant)
	}
	if allowed, ok := schema["enum"].([]any); ok {
		found := false
		for _, candidate := range allowed {
			found = found || jsonEqual(value, candidate)
		}
		if !found {
			return fmt.Errorf("%s is not an allowed value", path)
		}
	}
	switch expected := schema["type"].(type) {
	case string:
		if err := validateType(expected, value, path); err != nil {
			return err
		}
	case []any:
		valid := false
		for _, item := range expected {
			if name, ok := item.(string); ok && validateType(name, value, path) == nil {
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("%s has an invalid type", path)
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		return validateObject(schema, typed, path)
	case []any:
		return validateArray(schema, typed, path)
	case string:
		return validateString(schema, typed, path)
	case json.Number:
		return validateNumber(schema, typed, path)
	}
	return nil
}

func validateObject(schema map[string]any, value map[string]any, path string) error {
	properties, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	for _, rawName := range required {
		name, _ := rawName.(string)
		if _, ok := value[name]; !ok {
			return fmt.Errorf("%s.%s is required", path, name)
		}
	}
	for name, field := range value {
		rawProperty, known := properties[name]
		if !known {
			if additional, exists := schema["additionalProperties"]; exists && additional == false {
				return fmt.Errorf("%s.%s is not allowed", path, name)
			}
			continue
		}
		property, ok := rawProperty.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s has malformed schema", path, name)
		}
		if err := validate(property, field, path+"."+name); err != nil {
			return err
		}
	}
	return nil
}

func validateArray(schema map[string]any, value []any, path string) error {
	if minimum, ok := asInt(schema["minItems"]); ok && len(value) < minimum {
		return fmt.Errorf("%s has fewer than %d items", path, minimum)
	}
	if maximum, ok := asInt(schema["maxItems"]); ok && len(value) > maximum {
		return fmt.Errorf("%s has more than %d items", path, maximum)
	}
	if schema["uniqueItems"] == true {
		for left := range value {
			for right := left + 1; right < len(value); right++ {
				if jsonEqual(value[left], value[right]) {
					return fmt.Errorf("%s items must be unique", path)
				}
			}
		}
	}
	if prefix, ok := schema["prefixItems"].([]any); ok {
		for index := 0; index < len(value) && index < len(prefix); index++ {
			itemSchema, _ := prefix[index].(map[string]any)
			if err := validate(itemSchema, value[index], fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		if schema["items"] == false && len(value) > len(prefix) {
			return fmt.Errorf("%s has unvalidated tuple items", path)
		}
		return nil
	}
	if itemSchema, ok := schema["items"].(map[string]any); ok {
		for index, item := range value {
			if err := validate(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateString(schema map[string]any, value, path string) error {
	if minimum, ok := asInt(schema["minLength"]); ok && len([]rune(value)) < minimum {
		return fmt.Errorf("%s is shorter than %d characters", path, minimum)
	}
	if maximum, ok := asInt(schema["maxLength"]); ok && len([]rune(value)) > maximum {
		return fmt.Errorf("%s is longer than %d characters", path, maximum)
	}
	if pattern, ok := schema["pattern"].(string); ok {
		compiled, err := regexp.Compile(pattern)
		if err != nil || !compiled.MatchString(value) {
			return fmt.Errorf("%s does not match required pattern", path)
		}
	}
	if schema["format"] == "date-time" {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("%s is not an RFC3339 timestamp", path)
		}
	}
	return nil
}

func validateNumber(schema map[string]any, value json.Number, path string) error {
	number, err := value.Float64()
	if err != nil {
		return fmt.Errorf("%s is not a finite number", path)
	}
	if minimum, ok := asFloat(schema["minimum"]); ok && number < minimum {
		return fmt.Errorf("%s is less than %v", path, minimum)
	}
	if maximum, ok := asFloat(schema["maximum"]); ok && number > maximum {
		return fmt.Errorf("%s is greater than %v", path, maximum)
	}
	if minimum, ok := asFloat(schema["exclusiveMinimum"]); ok && number <= minimum {
		return fmt.Errorf("%s must be greater than %v", path, minimum)
	}
	return nil
}

func validateType(expected string, value any, path string) error {
	valid := false
	switch expected {
	case "null":
		valid = value == nil
	case "object":
		_, valid = value.(map[string]any)
	case "array":
		_, valid = value.([]any)
	case "string":
		_, valid = value.(string)
	case "boolean":
		_, valid = value.(bool)
	case "number":
		_, valid = value.(json.Number)
	case "integer":
		if number, ok := value.(json.Number); ok {
			float, err := number.Float64()
			valid = err == nil && !math.IsInf(float, 0) && math.Trunc(float) == float
		}
	default:
		return fmt.Errorf("%s uses unsupported schema type %q", path, expected)
	}
	if !valid {
		return fmt.Errorf("%s must be %s", path, expected)
	}
	return nil
}

func jsonEqual(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func asInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}
