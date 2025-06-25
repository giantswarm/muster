package api

import (
	"context"
	"time"
)

// ToolUpdateEvent represents a tool availability change event
type ToolUpdateEvent struct {
	Type       string    `json:"type"` // "server_registered", "server_deregistered", "tools_updated"
	ServerName string    `json:"server_name"`
	Tools      []string  `json:"tools"` // List of tool names
	Timestamp  time.Time `json:"timestamp"`
	Error      string    `json:"error,omitempty"`
}

// CallToolResult represents the result of a tool/capability call
type CallToolResult struct {
	Content []interface{} `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolMetadata describes a tool that can be exposed
type ToolMetadata struct {
	Name        string // e.g., "workflow_list", "action_login", "auth_login"
	Description string
	Parameters  []ParameterMetadata
}

// ParameterMetadata describes a tool parameter
type ParameterMetadata struct {
	Name        string
	Type        string // "string", "number", "boolean", "object"
	Required    bool
	Description string
	Default     interface{}
}

// ToolProvider interface - implemented by workflow and capability packages
type ToolProvider interface {
	// Returns all tools this provider offers
	GetTools() []ToolMetadata

	// Executes a tool by name
	ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error)
}

// ToolUpdateSubscriber interface for components that want to receive tool update events
type ToolUpdateSubscriber interface {
	OnToolsUpdated(event ToolUpdateEvent)
}

// ToolCall defines how to call an aggregator tool for a lifecycle event
type ToolCall struct {
	Tool            string                 `yaml:"tool" json:"tool"`
	Arguments       map[string]interface{} `yaml:"arguments" json:"arguments"`
	ResponseMapping ResponseMapping        `yaml:"responseMapping" json:"responseMapping"`
}

// Common enums and types used across all muster concepts

// HealthStatus represents the health status of a service or capability
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthChecking  HealthStatus = "checking"
)

// SchemaProperty defines a single property in the schema
type SchemaProperty struct {
	Type        string      `yaml:"type" json:"type"`
	Description string      `yaml:"description" json:"description"`
	Default     interface{} `yaml:"default,omitempty" json:"default,omitempty"`
}

// TimeoutConfig defines timeout behavior for operations
type TimeoutConfig struct {
	Create      time.Duration `yaml:"create" json:"create"`
	Delete      time.Duration `yaml:"delete" json:"delete"`
	HealthCheck time.Duration `yaml:"healthCheck" json:"healthCheck"`
}

// HealthCheckConfig defines health checking behavior
type HealthCheckConfig struct {
	Enabled          bool          `yaml:"enabled" json:"enabled"`
	Interval         time.Duration `yaml:"interval" json:"interval"`
	FailureThreshold int           `yaml:"failureThreshold" json:"failureThreshold"`
	SuccessThreshold int           `yaml:"successThreshold" json:"successThreshold"`
}

// ParameterMapping defines how service creation parameters map to tool arguments
type ParameterMapping struct {
	ToolParameter string      `yaml:"toolParameter" json:"toolParameter"`
	Default       interface{} `yaml:"default,omitempty" json:"default,omitempty"`
	Required      bool        `yaml:"required" json:"required"`
	Transform     string      `yaml:"transform,omitempty" json:"transform,omitempty"`
}

// ResponseMapping defines how to extract information from tool responses
type ResponseMapping struct {
	Name     string            `yaml:"name,omitempty" json:"name,omitempty"`
	Status   string            `yaml:"status,omitempty" json:"status,omitempty"`
	Health   string            `yaml:"health,omitempty" json:"health,omitempty"`
	Error    string            `yaml:"error,omitempty" json:"error,omitempty"`
	Metadata map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}
