package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MCPServerSpec defines the desired state of MCPServer
type MCPServerSpec struct {
	// Type specifies how this MCP server should be executed.
	// Supported values: "local" for local processes, "remote" for remote MCP servers
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=local;remote
	Type string `json:"type" yaml:"type"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_-]*$"
	ToolPrefix string `json:"toolPrefix,omitempty" yaml:"toolPrefix,omitempty"`

	// Description provides a human-readable description of this MCP server's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Local contains configuration for local MCP servers (type=local)
	// +kubebuilder:validation:Optional
	Local *MCPServerLocalSpec `json:"local,omitempty" yaml:"local,omitempty"`

	// Remote contains configuration for remote MCP servers (type=remote)
	// +kubebuilder:validation:Optional
	Remote *MCPServerRemoteSpec `json:"remote,omitempty" yaml:"remote,omitempty"`
}

// MCPServerLocalSpec defines configuration for local MCP servers
type MCPServerLocalSpec struct {
	// AutoStart determines whether this MCP server should be automatically started
	// when the muster system initializes or when dependencies become available.
	// +kubebuilder:default=false
	AutoStart bool `json:"autoStart,omitempty" yaml:"autoStart,omitempty"`

	// Command specifies the command line arguments for local type servers.
	// The first element is the executable path, followed by command line arguments.
	// This field is required when Type is "local".
	// +kubebuilder:validation:MinItems=1
	Command []string `json:"command,omitempty" yaml:"command,omitempty"`

	// Env contains environment variables to set for local type servers.
	// These are passed to the process when it is started.
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

// MCPServerRemoteSpec defines configuration for remote MCP servers
type MCPServerRemoteSpec struct {
	// Endpoint is the URL where the remote MCP server can be reached
	// Examples: http://mcp-server:8080/mcp, https://api.example.com/mcp
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://[^\s/$.?#].[^\s]*$`
	Endpoint string `json:"endpoint" yaml:"endpoint"`

	// Transport specifies the protocol used to communicate with the remote server
	// Supported values: "http" (for HTTP/HTTPS), "sse" (Server-Sent Events), "websocket"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=http;sse;websocket
	Transport string `json:"transport" yaml:"transport"`

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
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.remote.endpoint"
// +kubebuilder:printcolumn:name="AutoStart",type="boolean",JSONPath=".spec.local.autoStart"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Health",type="string",JSONPath=".status.health"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="(self.type == 'local' && has(self.local) && !has(self.remote)) || (self.type == 'remote' && has(self.remote) && !has(self.local))",message="exactly one of local or remote configuration must be specified based on type"
// +kubebuilder:validation:XValidation:rule="self.type != 'remote' || (has(self.remote) && has(self.remote.transport))",message="transport is required when type is remote"
// +kubebuilder:validation:XValidation:rule="self.type != 'local' || (has(self.local) && size(self.local.command) > 0)",message="command is required when type is local"

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
