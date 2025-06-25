package config

import (
	"fmt"
	"strings"
)

// ValidationError represents a validation error with context
type ValidationError struct {
	Field   string
	Value   interface{}
	Message string
}

// Error implements the error interface
func (ve ValidationError) Error() string {
	if ve.Field == "" {
		return ve.Message
	}
	return fmt.Sprintf("field '%s': %s", ve.Field, ve.Message)
}

// ValidationErrors is a collection of validation errors
type ValidationErrors []ValidationError

// Error implements the error interface for multiple validation errors
func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return "no validation errors"
	}
	if len(ve) == 1 {
		return ve[0].Error()
	}

	var messages []string
	for _, err := range ve {
		messages = append(messages, err.Error())
	}
	return fmt.Sprintf("validation failed: %s", strings.Join(messages, "; "))
}

// HasErrors returns true if there are any validation errors
func (ve ValidationErrors) HasErrors() bool {
	return len(ve) > 0
}

// Add adds a new validation error
func (ve *ValidationErrors) Add(field, message string, value ...interface{}) {
	var val interface{}
	if len(value) > 0 {
		val = value[0]
	}
	*ve = append(*ve, ValidationError{
		Field:   field,
		Value:   val,
		Message: message,
	})
}

// ValidateRequired checks if a required string field is not empty
func ValidateRequired(field, value, entityType string) error {
	if strings.TrimSpace(value) == "" {
		return ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("is required for %s", entityType),
		}
	}
	return nil
}

// ValidateRequiredSlice checks if a required slice is not empty
func ValidateRequiredSlice(field string, value []interface{}, entityType string) error {
	if len(value) == 0 {
		return ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("must have at least one item for %s", entityType),
		}
	}
	return nil
}

// ValidateOneOf checks if a value is in a list of allowed values
func ValidateOneOf(field, value string, allowed []string) error {
	for _, allowedValue := range allowed {
		if value == allowedValue {
			return nil
		}
	}
	return ValidationError{
		Field:   field,
		Value:   value,
		Message: fmt.Sprintf("must be one of: %s", strings.Join(allowed, ", ")),
	}
}

// ValidateMinLength checks if a string meets minimum length requirements
func ValidateMinLength(field, value string, minLength int) error {
	if len(strings.TrimSpace(value)) < minLength {
		return ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("must be at least %d characters long", minLength),
		}
	}
	return nil
}

// ValidateMaxLength checks if a string doesn't exceed maximum length
func ValidateMaxLength(field, value string, maxLength int) error {
	if len(value) > maxLength {
		return ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("must not exceed %d characters", maxLength),
		}
	}
	return nil
}

// ValidateEntityName validates that an entity name follows proper conventions
func ValidateEntityName(name, entityType string) error {
	if err := ValidateRequired("name", name, entityType); err != nil {
		return err
	}

	if err := ValidateMinLength("name", name, 1); err != nil {
		return err
	}

	if err := ValidateMaxLength("name", name, 100); err != nil {
		return err
	}

	// Check for invalid characters (basic validation)
	if strings.Contains(name, " ") {
		return ValidationError{
			Field:   "name",
			Value:   name,
			Message: "cannot contain spaces",
		}
	}

	return nil
}

// FormatValidationError creates a consistent validation error message
func FormatValidationError(entityType, entityName string, err error) error {
	if err == nil {
		return nil
	}

	if entityName != "" {
		return fmt.Errorf("validation failed for %s '%s': %w", entityType, entityName, err)
	}
	return fmt.Errorf("validation failed for %s: %w", entityType, err)
}

// ValidateMap checks if a map field is not nil and optionally not empty
func ValidateMap(field string, value map[string]interface{}, required bool, entityType string) error {
	if value == nil {
		if required {
			return ValidationError{
				Field:   field,
				Value:   nil,
				Message: fmt.Sprintf("is required for %s", entityType),
			}
		}
		return nil
	}

	if required && len(value) == 0 {
		return ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("must not be empty for %s", entityType),
		}
	}

	return nil
}
