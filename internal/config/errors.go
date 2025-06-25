package config

import (
	"fmt"
	"strings"
)

// ConfigurationError represents a structured error that occurs during configuration loading
type ConfigurationError struct {
	FilePath    string   `json:"filePath"`    // Full path to the file that caused the error
	FileName    string   `json:"fileName"`    // Base name of the file
	Source      string   `json:"source"`      // "user" or "project"
	Category    string   `json:"category"`    // Configuration category (serviceclasses, capabilities, workflows)
	ErrorType   string   `json:"errorType"`   // Type of error (parse, validation, io, etc.)
	Message     string   `json:"message"`     // Human-readable error message
	Details     string   `json:"details"`     // Additional details about the error
	LineNumber  int      `json:"lineNumber"`  // Line number where error occurred (if available)
	Suggestions []string `json:"suggestions"` // Actionable suggestions to fix the error
}

// Error implements the error interface
func (ce ConfigurationError) Error() string {
	return fmt.Sprintf("[%s/%s] %s: %s", ce.Source, ce.Category, ce.FileName, ce.Message)
}

// DetailedError returns a detailed error message with all context
func (ce ConfigurationError) DetailedError() string {
	var parts []string

	// Base error info
	parts = append(parts, fmt.Sprintf("Configuration Error in %s file: %s", ce.Source, ce.FileName))
	parts = append(parts, fmt.Sprintf("  File: %s", ce.FilePath))
	parts = append(parts, fmt.Sprintf("  Category: %s", ce.Category))
	parts = append(parts, fmt.Sprintf("  Type: %s", ce.ErrorType))

	if ce.LineNumber > 0 {
		parts = append(parts, fmt.Sprintf("  Line: %d", ce.LineNumber))
	}

	parts = append(parts, fmt.Sprintf("  Error: %s", ce.Message))

	if ce.Details != "" {
		parts = append(parts, fmt.Sprintf("  Details: %s", ce.Details))
	}

	if len(ce.Suggestions) > 0 {
		parts = append(parts, "  Suggestions:")
		for _, suggestion := range ce.Suggestions {
			parts = append(parts, fmt.Sprintf("    - %s", suggestion))
		}
	}

	return strings.Join(parts, "\n")
}

// ConfigurationErrorCollection holds multiple configuration errors
type ConfigurationErrorCollection struct {
	Errors []ConfigurationError `json:"errors"`
}

// Error implements the error interface for the collection
func (cec ConfigurationErrorCollection) Error() string {
	if len(cec.Errors) == 0 {
		return "no configuration errors"
	}

	if len(cec.Errors) == 1 {
		return cec.Errors[0].Error()
	}

	return fmt.Sprintf("%d configuration errors: %s (and %d more)",
		len(cec.Errors), cec.Errors[0].Error(), len(cec.Errors)-1)
}

// HasErrors returns true if there are any errors in the collection
func (cec *ConfigurationErrorCollection) HasErrors() bool {
	return len(cec.Errors) > 0
}

// Count returns the number of errors in the collection
func (cec *ConfigurationErrorCollection) Count() int {
	return len(cec.Errors)
}

// Add adds a new error to the collection
func (cec *ConfigurationErrorCollection) Add(err ConfigurationError) {
	cec.Errors = append(cec.Errors, err)
}

// AddError adds a basic error to the collection with context
func (cec *ConfigurationErrorCollection) AddError(filePath, fileName, source, category, errorType, message string) {
	cec.Add(ConfigurationError{
		FilePath:  filePath,
		FileName:  fileName,
		Source:    source,
		Category:  category,
		ErrorType: errorType,
		Message:   message,
	})
}

// GetErrorsBySource returns errors filtered by source (user/project)
func (cec *ConfigurationErrorCollection) GetErrorsBySource(source string) []ConfigurationError {
	var filtered []ConfigurationError
	for _, err := range cec.Errors {
		if err.Source == source {
			filtered = append(filtered, err)
		}
	}
	return filtered
}

// GetErrorsByCategory returns errors filtered by category
func (cec *ConfigurationErrorCollection) GetErrorsByCategory(category string) []ConfigurationError {
	var filtered []ConfigurationError
	for _, err := range cec.Errors {
		if err.Category == category {
			filtered = append(filtered, err)
		}
	}
	return filtered
}

// GetSummary returns a summary of all errors grouped by category and source
func (cec *ConfigurationErrorCollection) GetSummary() string {
	if len(cec.Errors) == 0 {
		return "No configuration errors"
	}

	// Group errors by category and source
	groups := make(map[string]map[string][]ConfigurationError)
	for _, err := range cec.Errors {
		if groups[err.Category] == nil {
			groups[err.Category] = make(map[string][]ConfigurationError)
		}
		groups[err.Category][err.Source] = append(groups[err.Category][err.Source], err)
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Configuration Error Summary (%d total errors):", len(cec.Errors)))

	for category, sources := range groups {
		parts = append(parts, fmt.Sprintf("\n%s:", category))
		for source, errors := range sources {
			parts = append(parts, fmt.Sprintf("  %s: %d errors", source, len(errors)))
			for _, err := range errors {
				parts = append(parts, fmt.Sprintf("    - %s: %s", err.FileName, err.Message))
			}
		}
	}

	return strings.Join(parts, "\n")
}

// GetDetailedReport returns a detailed report of all errors
func (cec *ConfigurationErrorCollection) GetDetailedReport() string {
	if len(cec.Errors) == 0 {
		return "No configuration errors to report"
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Detailed Configuration Error Report (%d errors):", len(cec.Errors)))
	parts = append(parts, strings.Repeat("=", 60))

	for i, err := range cec.Errors {
		parts = append(parts, fmt.Sprintf("\nError %d:", i+1))
		parts = append(parts, err.DetailedError())

		if i < len(cec.Errors)-1 {
			parts = append(parts, strings.Repeat("-", 40))
		}
	}

	return strings.Join(parts, "\n")
}

// NewConfigurationError creates a new configuration error with basic information
func NewConfigurationError(filePath, fileName, source, category, errorType, message string) ConfigurationError {
	return ConfigurationError{
		FilePath:  filePath,
		FileName:  fileName,
		Source:    source,
		Category:  category,
		ErrorType: errorType,
		Message:   message,
	}
}

// NewConfigurationErrorWithDetails creates a new configuration error with additional details
func NewConfigurationErrorWithDetails(filePath, fileName, source, category, errorType, message, details string, suggestions []string) ConfigurationError {
	return ConfigurationError{
		FilePath:    filePath,
		FileName:    fileName,
		Source:      source,
		Category:    category,
		ErrorType:   errorType,
		Message:     message,
		Details:     details,
		Suggestions: suggestions,
	}
}

// NewConfigurationErrorCollection creates a new empty error collection
func NewConfigurationErrorCollection() *ConfigurationErrorCollection {
	return &ConfigurationErrorCollection{
		Errors: make([]ConfigurationError, 0),
	}
}
