package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// WorkflowSpec defines the desired state of Workflow
type WorkflowSpec struct {
	// Name is the unique identifier for this workflow.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name" yaml:"name"`

	// Description provides a human-readable description of the workflow's purpose.
	// +kubebuilder:validation:MaxLength=1000
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Args defines the argument schema for workflow execution validation.
	Args map[string]ArgDefinition `json:"args,omitempty" yaml:"args,omitempty"`

	// Steps defines the sequence of workflow steps defining the execution flow.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Steps []WorkflowStep `json:"steps" yaml:"steps"`
}

// WorkflowStep defines a single step in the workflow execution
type WorkflowStep struct {
	// ID is the unique identifier for this step within the workflow.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-zA-Z0-9_-]+$"
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id" yaml:"id"`

	// Tool specifies the name of the tool to execute for this step.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Tool string `json:"tool" yaml:"tool"`

	// Args provides arguments for the tool execution (supports templating).
	Args map[string]*runtime.RawExtension `json:"args,omitempty" yaml:"args,omitempty"`

	// Condition defines an optional condition that determines whether this step should execute.
	Condition *WorkflowCondition `json:"condition,omitempty" yaml:"condition,omitempty"`

	// Store indicates whether to store the step result for use in later steps.
	// +kubebuilder:default=false
	Store bool `json:"store,omitempty" yaml:"store,omitempty"`

	// AllowFailure defines if in case of an error the next step is executed or not.
	// +kubebuilder:default=false
	AllowFailure bool `json:"allowFailure,omitempty" yaml:"allowFailure,omitempty"`

	// Outputs defines how step results should be stored and made available to subsequent steps.
	Outputs map[string]*runtime.RawExtension `json:"outputs,omitempty" yaml:"outputs,omitempty"`

	// Description provides human-readable documentation for this step's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowCondition defines execution conditions for workflow steps
type WorkflowCondition struct {
	// Tool specifies the name of the tool to execute for condition evaluation.
	// Optional when FromStep is used.
	Tool string `json:"tool,omitempty" yaml:"tool,omitempty"`

	// Args provides the arguments to pass to the condition tool.
	Args map[string]*runtime.RawExtension `json:"args,omitempty" yaml:"args,omitempty"`

	// FromStep specifies the step ID to reference for condition evaluation.
	FromStep string `json:"fromStep,omitempty" yaml:"fromStep,omitempty"`

	// Expect defines positive health check expectations.
	Expect *WorkflowConditionExpectation `json:"expect,omitempty" yaml:"expect,omitempty"`

	// ExpectNot defines negative health check expectations.
	ExpectNot *WorkflowConditionExpectation `json:"expectNot,omitempty" yaml:"expectNot,omitempty"`
}

// WorkflowConditionExpectation defines expected outcomes for workflow conditions
type WorkflowConditionExpectation struct {
	// Success indicates whether the tool call should succeed.
	Success *bool `json:"success,omitempty" yaml:"success,omitempty"`

	// JsonPath defines JSON path conditions to check in the result.
	JsonPath map[string]*runtime.RawExtension `json:"jsonPath,omitempty" yaml:"jsonPath,omitempty"`
}

// WorkflowStatus defines the observed state of Workflow
type WorkflowStatus struct {
	// Available indicates whether all required tools for this workflow are available.
	Available bool `json:"available,omitempty" yaml:"available,omitempty"`

	// RequiredTools lists all tools required by workflow steps.
	RequiredTools []string `json:"requiredTools,omitempty" yaml:"requiredTools,omitempty"`

	// MissingTools lists required tools that are currently unavailable.
	MissingTools []string `json:"missingTools,omitempty" yaml:"missingTools,omitempty"`

	// StepValidation contains validation results for each workflow step.
	StepValidation []StepValidationResult `json:"stepValidation,omitempty" yaml:"stepValidation,omitempty"`

	// Conditions represent the latest available observations of the workflow's state.
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// StepValidationResult contains validation results for a workflow step
type StepValidationResult struct {
	// StepID identifies the workflow step.
	StepID string `json:"stepId" yaml:"stepId"`

	// Valid indicates whether the step passed validation.
	Valid bool `json:"valid" yaml:"valid"`

	// ToolAvailable indicates whether the required tool is available.
	ToolAvailable bool `json:"toolAvailable" yaml:"toolAvailable"`

	// ValidationErrors contains any validation error messages.
	ValidationErrors []string `json:"validationErrors,omitempty" yaml:"validationErrors,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=wf
//+kubebuilder:printcolumn:name="Available",type="boolean",JSONPath=".status.available"
//+kubebuilder:printcolumn:name="Steps",type="integer",JSONPath=".spec.steps[*].id"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Workflow is the Schema for the workflows API
type Workflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkflowSpec   `json:"spec,omitempty"`
	Status WorkflowStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// WorkflowList contains a list of Workflow
type WorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workflow{}, &WorkflowList{})
}
