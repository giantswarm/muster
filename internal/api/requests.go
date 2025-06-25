package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Request types for all core API operations

// Capability request types
type CapabilityCreateRequest struct {
	Name        string                         `json:"name" validate:"required"`
	Type        string                         `json:"type" validate:"required"`
	Version     string                         `json:"version,omitempty"`
	Description string                         `json:"description,omitempty"`
	Operations  map[string]OperationDefinition `json:"operations" validate:"required"`
}

type CapabilityUpdateRequest struct {
	Name        string                         `json:"name" validate:"required"`
	Type        string                         `json:"type,omitempty"`
	Version     string                         `json:"version,omitempty"`
	Description string                         `json:"description,omitempty"`
	Operations  map[string]OperationDefinition `json:"operations,omitempty"`
}

type CapabilityValidateRequest struct {
	Name        string                         `json:"name" validate:"required"`
	Type        string                         `json:"type" validate:"required"`
	Version     string                         `json:"version,omitempty"`
	Description string                         `json:"description,omitempty"`
	Operations  map[string]OperationDefinition `json:"operations" validate:"required"`
}

// ServiceClass request types
type ServiceClassCreateRequest struct {
	Name          string        `json:"name" validate:"required"`
	Version       string        `json:"version,omitempty"`
	Description   string        `json:"description,omitempty"`
	ServiceConfig ServiceConfig `json:"serviceConfig" validate:"required"`
}

type ServiceClassUpdateRequest struct {
	Name          string        `json:"name" validate:"required"`
	Version       string        `json:"version,omitempty"`
	Description   string        `json:"description,omitempty"`
	ServiceConfig ServiceConfig `json:"serviceConfig,omitempty"`
}

type ServiceClassValidateRequest struct {
	Name          string        `json:"name" validate:"required"`
	Version       string        `json:"version,omitempty"`
	Description   string        `json:"description,omitempty"`
	ServiceConfig ServiceConfig `json:"serviceConfig" validate:"required"`
}

// MCPServer request types
type MCPServerCreateRequest struct {
	Name             string            `json:"name" validate:"required"`
	Type             string            `json:"type" validate:"required"`
	AutoStart        bool              `json:"autoStart,omitempty"`
	ToolPrefix       string            `json:"toolPrefix,omitempty"`
	Command          []string          `json:"command,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Image            string            `json:"image,omitempty"`
	ContainerPorts   []string          `json:"containerPorts,omitempty"`
	ContainerEnv     map[string]string `json:"containerEnv,omitempty"`
	ContainerVolumes []string          `json:"containerVolumes,omitempty"`
	Entrypoint       []string          `json:"entrypoint,omitempty"`
	ContainerUser    string            `json:"containerUser,omitempty"`
}

type MCPServerUpdateRequest struct {
	Name             string            `json:"name" validate:"required"`
	Type             string            `json:"type" validate:"required"`
	AutoStart        bool              `json:"autoStart,omitempty"`
	ToolPrefix       string            `json:"toolPrefix,omitempty"`
	Command          []string          `json:"command,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Image            string            `json:"image,omitempty"`
	ContainerPorts   []string          `json:"containerPorts,omitempty"`
	ContainerEnv     map[string]string `json:"containerEnv,omitempty"`
	ContainerVolumes []string          `json:"containerVolumes,omitempty"`
	Entrypoint       []string          `json:"entrypoint,omitempty"`
	ContainerUser    string            `json:"containerUser,omitempty"`
}

type MCPServerValidateRequest struct {
	Name           string            `json:"name" validate:"required"`
	Type           string            `json:"type" validate:"required"`
	AutoStart      bool              `json:"autoStart,omitempty"`
	Command        []string          `json:"command,omitempty"`
	Image          string            `json:"image,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	ContainerPorts []string          `json:"containerPorts,omitempty"`
	Description    string            `json:"description,omitempty"`
}

// Workflow request types
type WorkflowCreateRequest struct {
	Name        string              `json:"name" validate:"required"`
	Version     string              `json:"version,omitempty"`
	Description string              `json:"description,omitempty"`
	InputSchema WorkflowInputSchema `json:"inputSchema,omitempty"`
	Steps       []WorkflowStep      `json:"steps" validate:"required"`
}

type WorkflowUpdateRequest struct {
	Name        string              `json:"name" validate:"required"`
	Version     string              `json:"version,omitempty"`
	Description string              `json:"description,omitempty"`
	InputSchema WorkflowInputSchema `json:"inputSchema,omitempty"`
	Steps       []WorkflowStep      `json:"steps,omitempty"`
}

type WorkflowValidateRequest struct {
	Name        string              `json:"name" validate:"required"`
	Version     string              `json:"version,omitempty"`
	Description string              `json:"description,omitempty"`
	InputSchema WorkflowInputSchema `json:"inputSchema,omitempty"`
	Steps       []WorkflowStep      `json:"steps" validate:"required"`
}

// Service request types
type ServiceValidateRequest struct {
	Name             string                 `json:"name" validate:"required"`
	ServiceClassName string                 `json:"serviceClassName" validate:"required"`
	Parameters       map[string]interface{} `json:"parameters,omitempty"`
	AutoStart        bool                   `json:"autoStart,omitempty"`
	Description      string                 `json:"description,omitempty"`
}

// ParseRequest converts a map[string]interface{} to a typed request struct
// This uses JSON marshaling/unmarshaling for type conversion and validation
func ParseRequest[T any](args map[string]interface{}, request *T) error {
	// First validate that no unknown parameters are present
	if err := validateStrictParameters(args, request); err != nil {
		return err
	}

	// Convert to JSON and back to get proper type conversion
	jsonData, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to marshal request arguments: %w", err)
	}

	// Use strict JSON decoder that fails on unknown fields
	decoder := json.NewDecoder(strings.NewReader(string(jsonData)))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(request); err != nil {
		return fmt.Errorf("failed to parse request: %w", err)
	}

	// Basic validation - check required fields
	if err := validateRequest(request); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	return nil
}

// validateRequest performs basic validation on request structs
// This is a simple implementation - could be replaced with a validation library
func validateRequest(request interface{}) error {
	// Use reflection to check for required fields
	// This is a simplified implementation - in production you might want to use
	// a validation library like github.com/go-playground/validator

	// For now, we rely on the JSON unmarshaling to handle type conversion
	// and the calling code to check for required fields
	return nil
}

// ValidateStrictParameters ensures no unknown parameters are present
func ValidateStrictParameters(args map[string]interface{}, allowedFields []string) error {
	allowedMap := make(map[string]bool)
	for _, field := range allowedFields {
		allowedMap[field] = true
	}

	var unknownFields []string
	for field := range args {
		if !allowedMap[field] {
			unknownFields = append(unknownFields, field)
		}
	}

	if len(unknownFields) > 0 {
		return fmt.Errorf("unknown parameters: %v. Allowed parameters: %v", unknownFields, allowedFields)
	}

	return nil
}

// validateStrictParameters ensures no unknown parameters are present
func validateStrictParameters(args map[string]interface{}, request interface{}) error {
	// Get the struct type
	structType := reflect.TypeOf(request).Elem()

	// Build a map of allowed field names based on JSON tags
	allowedFields := make(map[string]bool)
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		jsonTag := field.Tag.Get("json")

		if jsonTag != "" && jsonTag != "-" {
			// Parse the JSON tag to get the field name
			fieldName := strings.Split(jsonTag, ",")[0]
			if fieldName != "" {
				allowedFields[fieldName] = true
			}
		} else {
			// If no JSON tag, use the field name
			allowedFields[field.Name] = true
		}
	}

	// Check for unknown parameters
	var unknownParams []string
	for paramName := range args {
		if !allowedFields[paramName] {
			unknownParams = append(unknownParams, paramName)
		}
	}

	if len(unknownParams) > 0 {
		var allowedNames []string
		for name := range allowedFields {
			allowedNames = append(allowedNames, name)
		}
		return fmt.Errorf("unknown parameters: %v. Allowed parameters: %v", unknownParams, allowedNames)
	}

	return nil
}
