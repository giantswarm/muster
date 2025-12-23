package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ServiceClassSpec defines the desired state of ServiceClass
type ServiceClassSpec struct {
	// Description provides a human-readable description of this ServiceClass.
	// +kubebuilder:validation:MaxLength=1000
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Args defines the argument schema for service instantiation.
	Args map[string]ArgDefinition `json:"args,omitempty" yaml:"args,omitempty"`

	// ServiceConfig defines the core service configuration template.
	// +kubebuilder:validation:Required
	ServiceConfig ServiceConfig `json:"serviceConfig" yaml:"serviceConfig"`
}

// ArgDefinition defines validation and metadata for service arguments
type ArgDefinition struct {
	// Type specifies the expected data type for this argument.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=string;integer;boolean;number;object;array
	Type string `json:"type" yaml:"type"`

	// Required indicates whether this argument must be provided.
	// +kubebuilder:default=false
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`

	// Default provides a default value if the argument is not specified.
	Default *runtime.RawExtension `json:"default,omitempty" yaml:"default,omitempty"`

	// Description explains the purpose and usage of this argument.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// ServiceConfig defines the service template configuration
type ServiceConfig struct {
	// DefaultName provides a template for generating service instance names.
	DefaultName string `json:"defaultName,omitempty" yaml:"defaultName,omitempty"`

	// Dependencies lists other ServiceClasses that must be available.
	Dependencies []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`

	// LifecycleTools defines the tools for service lifecycle management.
	// +kubebuilder:validation:Required
	LifecycleTools LifecycleTools `json:"lifecycleTools" yaml:"lifecycleTools"`

	// HealthCheck configures health monitoring for service instances.
	HealthCheck *HealthCheckConfig `json:"healthCheck,omitempty" yaml:"healthCheck,omitempty"`

	// Timeout configures timeouts for various operations.
	Timeout *TimeoutConfig `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// Outputs defines template-based outputs for service instances.
	Outputs map[string]string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
}

// LifecycleTools defines the tools for service lifecycle management
type LifecycleTools struct {
	// Start tool configuration for starting service instances.
	// +kubebuilder:validation:Required
	Start ToolCall `json:"start" yaml:"start"`

	// Stop tool configuration for stopping service instances.
	// +kubebuilder:validation:Required
	Stop ToolCall `json:"stop" yaml:"stop"`

	// Restart tool configuration for restarting service instances.
	Restart *ToolCall `json:"restart,omitempty" yaml:"restart,omitempty"`

	// HealthCheck tool configuration for health checking.
	HealthCheck *HealthCheckToolCall `json:"healthCheck,omitempty" yaml:"healthCheck,omitempty"`

	// Status tool configuration for querying service status.
	Status *ToolCall `json:"status,omitempty" yaml:"status,omitempty"`
}

// ToolCall defines a tool invocation with arguments and output mapping
type ToolCall struct {
	// Tool specifies the name of the tool to execute.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Tool string `json:"tool" yaml:"tool"`

	// Args provides arguments for the tool execution (supports templating).
	Args map[string]*runtime.RawExtension `json:"args,omitempty" yaml:"args,omitempty"`

	// Outputs maps tool result paths to variable names for later use.
	Outputs map[string]string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
}

// HealthCheckToolCall extends ToolCall with health check expectations
type HealthCheckToolCall struct {
	// Tool specifies the name of the tool to execute.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Tool string `json:"tool" yaml:"tool"`

	// Args provides arguments for the tool execution (supports templating).
	Args map[string]*runtime.RawExtension `json:"args,omitempty" yaml:"args,omitempty"`

	// Expect defines positive health check expectations.
	Expect *HealthCheckExpectation `json:"expect,omitempty" yaml:"expect,omitempty"`

	// ExpectNot defines negative health check expectations.
	ExpectNot *HealthCheckExpectation `json:"expectNot,omitempty" yaml:"expectNot,omitempty"`
}

// HealthCheckExpectation defines what to expect from health check results
type HealthCheckExpectation struct {
	// Success indicates whether the tool call should succeed.
	Success *bool `json:"success,omitempty" yaml:"success,omitempty"`

	// JSONPath defines JSON path conditions to check in the result.
	JSONPath map[string]*runtime.RawExtension `json:"jsonPath,omitempty" yaml:"jsonPath,omitempty"`
}

// HealthCheckConfig configures health monitoring behavior
type HealthCheckConfig struct {
	// Enabled controls whether health checking is active.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// Interval specifies how often to perform health checks.
	// +kubebuilder:validation:Pattern="^[0-9]+(ns|us|ms|s|m|h)$"
	// +kubebuilder:default="30s"
	Interval string `json:"interval,omitempty" yaml:"interval,omitempty"`

	// FailureThreshold sets the number of failures before marking unhealthy.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	FailureThreshold int `json:"failureThreshold,omitempty" yaml:"failureThreshold,omitempty"`

	// SuccessThreshold sets the number of successes to mark healthy.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	SuccessThreshold int `json:"successThreshold,omitempty" yaml:"successThreshold,omitempty"`
}

// TimeoutConfig configures operation timeouts
type TimeoutConfig struct {
	// Create timeout for service creation operations.
	// +kubebuilder:validation:Pattern="^[0-9]+(ns|us|ms|s|m|h)$"
	Create string `json:"create,omitempty" yaml:"create,omitempty"`

	// Delete timeout for service deletion operations.
	// +kubebuilder:validation:Pattern="^[0-9]+(ns|us|ms|s|m|h)$"
	Delete string `json:"delete,omitempty" yaml:"delete,omitempty"`

	// HealthCheck timeout for individual health check operations.
	// +kubebuilder:validation:Pattern="^[0-9]+(ns|us|ms|s|m|h)$"
	HealthCheck string `json:"healthCheck,omitempty" yaml:"healthCheck,omitempty"`
}

// ServiceClassStatus defines the observed state of ServiceClass
type ServiceClassStatus struct {
	// Valid indicates whether the ServiceClass spec passes structural validation.
	Valid bool `json:"valid,omitempty" yaml:"valid,omitempty"`

	// ValidationErrors contains any spec validation error messages.
	ValidationErrors []string `json:"validationErrors,omitempty" yaml:"validationErrors,omitempty"`

	// ReferencedTools lists all tools mentioned in the ServiceClass lifecycle definitions.
	// This is informational only; actual availability depends on the user's session.
	// See ADR 007 for details on session-scoped tool visibility.
	ReferencedTools []string `json:"referencedTools,omitempty" yaml:"referencedTools,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sc
// +kubebuilder:printcolumn:name="Valid",type="boolean",JSONPath=".status.valid"
// +kubebuilder:printcolumn:name="ReferencedTools",type="string",JSONPath=".status.referencedTools"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ServiceClass is the Schema for the serviceclasses API
type ServiceClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceClassSpec   `json:"spec,omitempty"`
	Status ServiceClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceClassList contains a list of ServiceClass
type ServiceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceClass{}, &ServiceClassList{})
}
