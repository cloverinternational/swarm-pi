// Package skills implements the Claude Code-style skills system.
// This file contains validation logic for skills per the agentskills.io specification.
package skills

import (
	"fmt"
	"regexp"
	"strings"
)

// Validation constants per agentskills.io specification
const (
	// MaxNameLength is the maximum length for skill names
	MaxNameLength = 64

	// MaxDescriptionLength is the maximum length for descriptions.
	//
	// This is a DELIBERATE divergence from agentskills.io, which caps
	// descriptions at 1024. Class-level umbrella skills legitimately need to
	// enumerate the situations they route (that enumeration is what makes them
	// discoverable), and at 1024 the loader was silently skipping such skills
	// entirely — an over-long description made the skill invisible rather than
	// merely verbose. 1500 keeps descriptions bounded while admitting real
	// umbrellas. Descriptions authored for strict agentskills.io interop must
	// still stay within 1024.
	MaxDescriptionLength = 1500

	// MaxLicenseLength is the maximum length for license field
	MaxLicenseLength = 256

	// MaxCompatibilityLength is the maximum length for compatibility field
	MaxCompatibilityLength = 500
)

// nameRegex validates skill names per agentskills.io spec:
// - lowercase alphanumeric + hyphens only
// - no start/end hyphen
// - no consecutive hyphens
// Pattern: ^[a-z0-9]+(-[a-z0-9]+)*$
var nameRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidationError represents a validation issue with a skill.
type ValidationError struct {
	// Field is the name of the field that has the validation error
	Field string `json:"field"`

	// Message describes what is wrong
	Message string `json:"message"`

	// Fatal indicates if this error prevents the skill from being used
	Fatal bool `json:"fatal"`
}

// Error implements the error interface for ValidationError.
func (e ValidationError) Error() string {
	severity := "warning"
	if e.Fatal {
		severity = "error"
	}
	return fmt.Sprintf("[%s] %s: %s", severity, e.Field, e.Message)
}

// ValidationResult contains all validation results for a skill.
type ValidationResult struct {
	// Valid is true if no fatal errors were found
	Valid bool `json:"valid"`

	// Errors contains all validation issues found
	Errors []ValidationError `json:"errors,omitempty"`

	// Warnings contains non-fatal issues
	Warnings []ValidationError `json:"warnings,omitempty"`
}

// HasFatalErrors returns true if any fatal validation errors exist.
func (r *ValidationResult) HasFatalErrors() bool {
	for _, err := range r.Errors {
		if err.Fatal {
			return true
		}
	}
	return false
}

// AllErrors returns both errors and warnings combined.
func (r *ValidationResult) AllErrors() []ValidationError {
	all := make([]ValidationError, 0, len(r.Errors)+len(r.Warnings))
	all = append(all, r.Errors...)
	all = append(all, r.Warnings...)
	return all
}

// ValidateSkill validates a skill against the agentskills.io specification.
// Returns a ValidationResult containing all issues found.
func ValidateSkill(skill *Skill) *ValidationResult {
	result := &ValidationResult{Valid: true}

	if skill == nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "skill",
			Message: "skill cannot be nil",
			Fatal:   true,
		})
		return result
	}

	// Validate metadata
	metaErrors := ValidateMetadata(&skill.Metadata)
	for _, err := range metaErrors {
		if err.Fatal {
			result.Valid = false
			result.Errors = append(result.Errors, err)
		} else {
			result.Warnings = append(result.Warnings, err)
		}
	}

	// Validate path is set
	if skill.Path == "" {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "path",
			Message: "skill path is empty",
			Fatal:   false,
		})
	}

	return result
}

// ValidateName validates a skill name per the agentskills.io specification.
// Requirements:
// - 1-64 characters
// - lowercase alphanumeric + hyphens only
// - no start/end hyphen
// - no consecutive hyphens
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}

	if len(name) > MaxNameLength {
		return fmt.Errorf("name exceeds maximum length of %d characters (got %d)", MaxNameLength, len(name))
	}

	if !nameRegex.MatchString(name) {
		// Provide specific error message
		if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			return fmt.Errorf("name cannot start or end with a hyphen")
		}
		if strings.Contains(name, "--") {
			return fmt.Errorf("name cannot contain consecutive hyphens")
		}
		if strings.ToLower(name) != name {
			return fmt.Errorf("name must be lowercase")
		}
		return fmt.Errorf("name must contain only lowercase alphanumeric characters and hyphens")
	}

	return nil
}

// ValidateDescription validates a description per the agentskills.io specification.
// Requirements:
//   - 1-MaxDescriptionLength characters (see the constant: 1500, above the
//     agentskills.io limit of 1024 by design)
//   - non-empty (required)
func ValidateDescription(desc string) error {
	if desc == "" {
		return fmt.Errorf("description is required")
	}

	trimmed := strings.TrimSpace(desc)
	if trimmed == "" {
		return fmt.Errorf("description cannot be empty or whitespace only")
	}

	if len(desc) > MaxDescriptionLength {
		return fmt.Errorf("description exceeds maximum length of %d characters (got %d)", MaxDescriptionLength, len(desc))
	}

	return nil
}

// ValidateLicense validates the license field per the agentskills.io specification.
// Requirements:
// - max 256 characters if present
func ValidateLicense(license string) error {
	if license == "" {
		return nil // Optional field
	}

	if len(license) > MaxLicenseLength {
		return fmt.Errorf("license exceeds maximum length of %d characters (got %d)", MaxLicenseLength, len(license))
	}

	return nil
}

// ValidateCompatibility validates the compatibility field per the agentskills.io specification.
// Requirements:
// - max 500 characters if present
func ValidateCompatibility(compat string) error {
	if compat == "" {
		return nil // Optional field
	}

	if len(compat) > MaxCompatibilityLength {
		return fmt.Errorf("compatibility exceeds maximum length of %d characters (got %d)", MaxCompatibilityLength, len(compat))
	}

	return nil
}

// ValidateAllowedTools validates the allowed-tools field.
// It should be a space-delimited list of tool names.
func ValidateAllowedTools(tools string) error {
	if tools == "" {
		return nil // Optional field
	}

	// Split and validate each tool name
	toolList := strings.FieldsSeq(tools)
	for tool := range toolList {
		// Tool names should follow similar naming conventions
		if strings.TrimSpace(tool) == "" {
			continue
		}
		// Basic validation - no special characters except underscore
		for _, r := range tool {
			if !isAlphanumeric(r) && r != '_' && r != '-' {
				return fmt.Errorf("invalid tool name %q: contains invalid character %q", tool, r)
			}
		}
	}

	return nil
}

// ValidateMetadata validates all metadata fields against the agentskills.io specification.
func ValidateMetadata(meta *SkillMetadata) []ValidationError {
	var errors []ValidationError

	if meta == nil {
		return []ValidationError{{
			Field:   "metadata",
			Message: "metadata cannot be nil",
			Fatal:   true,
		}}
	}

	// Validate name (required)
	if err := ValidateName(meta.Name); err != nil {
		errors = append(errors, ValidationError{
			Field:   "name",
			Message: err.Error(),
			Fatal:   true,
		})
	}

	// Validate description (required)
	if err := ValidateDescription(meta.Description); err != nil {
		errors = append(errors, ValidationError{
			Field:   "description",
			Message: err.Error(),
			Fatal:   true,
		})
	}

	// Validate license (optional)
	if err := ValidateLicense(meta.License); err != nil {
		errors = append(errors, ValidationError{
			Field:   "license",
			Message: err.Error(),
			Fatal:   false, // Non-fatal for optional fields exceeding length
		})
	}

	// Validate compatibility (optional)
	if err := ValidateCompatibility(meta.Compatibility); err != nil {
		errors = append(errors, ValidationError{
			Field:   "compatibility",
			Message: err.Error(),
			Fatal:   false,
		})
	}

	// Validate allowed-tools (optional)
	if err := ValidateAllowedTools(meta.AllowedTools); err != nil {
		errors = append(errors, ValidationError{
			Field:   "allowed-tools",
			Message: err.Error(),
			Fatal:   false,
		})
	}

	return errors
}

// IsValidSkillName checks if a name is valid per the agentskills.io spec.
// Returns true if the name passes all validation rules.
func IsValidSkillName(name string) bool {
	return ValidateName(name) == nil
}

// SanitizeName attempts to convert a string into a valid skill name.
// It lowercases, replaces invalid characters with hyphens, and removes
// leading/trailing/consecutive hyphens.
func SanitizeName(name string) string {
	// Lowercase
	result := strings.ToLower(name)

	// Replace spaces and underscores with hyphens
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "_", "-")

	// Remove any non-alphanumeric characters except hyphen
	var cleaned strings.Builder
	for _, r := range result {
		if isAlphanumeric(r) || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	result = cleaned.String()

	// Remove consecutive hyphens
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	// Remove leading/trailing hyphens
	result = strings.Trim(result, "-")

	// Truncate to max length
	if len(result) > MaxNameLength {
		result = result[:MaxNameLength]
		// Make sure we don't end with a hyphen after truncation
		result = strings.TrimRight(result, "-")
	}

	return result
}

// isAlphanumeric checks if a rune is alphanumeric (a-z, A-Z, 0-9).
func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// GetAllowedToolsList parses the allowed-tools field and returns a slice of tool names.
func GetAllowedToolsList(meta *SkillMetadata) []string {
	if meta == nil || meta.AllowedTools == "" {
		return nil
	}

	tools := strings.Fields(meta.AllowedTools)
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		if t := strings.TrimSpace(tool); t != "" {
			result = append(result, t)
		}
	}
	return result
}
