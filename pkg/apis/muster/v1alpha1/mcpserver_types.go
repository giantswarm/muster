package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MCPServerSpec defines the desired state of MCPServer
type MCPServerSpec struct {
	// Type specifies how this MCP server should be executed.
	// Supported values: "stdio" for local processes, "streamable-http" for HTTP-based servers, "sse" for Server-Sent Events
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=stdio;streamable-http;sse
	Type string `json:"type" yaml:"type"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_-]*$"
	ToolPrefix string `json:"toolPrefix,omitempty" yaml:"toolPrefix,omitempty"`

	// Description provides a human-readable description of this MCP server's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// AutoStart determines whether this MCP server should be automatically started
	// when the muster system initializes or when dependencies become available.
	// +kubebuilder:default=false
	AutoStart bool `json:"autoStart,omitempty" yaml:"autoStart,omitempty"`

	// Command specifies the executable path for stdio type servers.
	// This field is required when Type is "stdio".
	Command string `json:"command,omitempty" yaml:"command,omitempty"`

	// Args specifies the command line arguments for stdio type servers.
	// This field is only available when Type is "stdio".
	Args []string `json:"args,omitempty" yaml:"args,omitempty"`

	// URL is the endpoint where the remote MCP server can be reached
	// This field is required when Type is "streamable-http" or "sse".
	// Examples: http://mcp-server:8080/mcp, https://api.example.com/mcp
	// +kubebuilder:validation:Pattern=`^https?://[^\s/$.?#].[^\s]*$`
	URL string `json:"url,omitempty" yaml:"url,omitempty"`

	// Env contains environment variables to set for the MCP server.
	// For stdio servers, these are passed to the process when it is started.
	// For remote servers, these can be used for authentication or configuration.
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`

	// Headers contains HTTP headers to send with requests to remote MCP servers.
	// This field is only relevant when Type is "streamable-http" or "sse".
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

// MCPServerStatus defines the observed state of MCPServer
type MCPServerStatus struct {
	// State represents the current operational state of the MCP server
	// +kubebuilder:validation:Enum=unknown;starting;running;stopping;stopped;failed
	State string `json:"state,omitempty" yaml:"state,omitempty"`

	// Health represents the health status of the MCP server
	// +kubebuilder:validation:Enum=unknown;healthy;unhealthy;checking
	Health string `json:"health,omitempty" yaml:"health,omitempty"`

	// AvailableTools contains a list of tool names provided by this MCP server
	AvailableTools []string `json:"availableTools,omitempty" yaml:"availableTools,omitempty"`

	// LastError contains any error message from the most recent server operation
	LastError string `json:"lastError,omitempty" yaml:"lastError,omitempty"`

	// Conditions represent the latest available observations of the MCPServer's current state
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mcps
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="AutoStart",type="boolean",JSONPath=".spec.autoStart"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Health",type="string",JSONPath=".status.health"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="self.type != 'stdio' || has(self.command)",message="command is required when type is stdio"
// +kubebuilder:validation:XValidation:rule="self.type == 'stdio' || has(self.url)",message="url is required when type is streamable-http or sse"
// +kubebuilder:validation:XValidation:rule="self.type == 'stdio' || !has(self.args)",message="args field is only allowed when type is stdio"
// +kubebuilder:validation:XValidation:rule="self.type != 'stdio' || !has(self.headers)",message="headers field is only allowed when type is streamable-http or sse"

// MCPServer is the Schema for the mcpservers API
type MCPServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MCPServerSpec   `json:"spec,omitempty"`
	Status MCPServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MCPServerList contains a list of MCPServer
type MCPServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MCPServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MCPServer{}, &MCPServerList{})
}
