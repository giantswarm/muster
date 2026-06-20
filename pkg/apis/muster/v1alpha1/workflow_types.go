package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ArgDefinition defines validation rules and metadata for a single workflow argument.
// It specifies the expected type, whether the argument is required, an optional default,
// and a human-readable description.
type ArgDefinition struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=string;integer;boolean;number;object;array
	Type string `json:"type" yaml:"type"`

	// Required indicates whether this argument must be provided.
	// +kubebuilder:default=false
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`

	// Default provides a default value when the argument is omitted.
	// +kubebuilder:validation:XPreserveUnknownFields
	Default *apiextensionsv1.JSON `json:"default,omitempty" yaml:"default,omitempty"`

	// Description provides human-readable documentation.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowSpec defines the desired state of Workflow
type WorkflowSpec struct {
	// Description provides a human-readable description of the workflow's purpose.
	// +kubebuilder:validation:MaxLength=1000
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Args defines the argument schema for workflow execution validation.
	Args map[string]ArgDefinition `json:"args,omitempty" yaml:"args,omitempty"`

	// Steps defines the sequence of workflow steps defining the execution flow.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Steps []WorkflowStep `json:"steps" yaml:"steps"`

	// OnFailure defines best-effort cleanup/rollback steps that run when the
	// workflow fails on a step that does not allow failure. The steps execute
	// sequentially and their own failures are tolerated.
	OnFailure []WorkflowSubStep `json:"onFailure,omitempty" yaml:"onFailure,omitempty"`
}

// WorkflowStep defines a single step in the workflow execution.
// A step is exactly one of: a tool call (tool), a sequential loop (forEach),
// or a concurrent group (parallel).
type WorkflowStep struct {
	// ID is the unique identifier for this step within the workflow.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-zA-Z0-9_-]+$"
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id" yaml:"id"`

	// Tool specifies the name of the tool to execute for this step.
	// Mutually exclusive with forEach and parallel.
	Tool string `json:"tool,omitempty" yaml:"tool,omitempty"`

	// Args provides arguments for the tool execution (supports templating).
	// Values may be any JSON type (string, integer, boolean, number, object, array)
	// because the schema uses x-kubernetes-preserve-unknown-fields. Templated
	// strings such as "{{.input.namespace}}" are resolved server-side at
	// execution time.
	Args map[string]apiextensionsv1.JSON `json:"args,omitempty" yaml:"args,omitempty"`

	// Condition defines an optional condition that determines whether this step should execute.
	Condition *WorkflowCondition `json:"condition,omitempty" yaml:"condition,omitempty"`

	// ForEach executes a body of sub-steps once per item of a list. Mutually
	// exclusive with tool and parallel.
	ForEach *WorkflowForEach `json:"forEach,omitempty" yaml:"forEach,omitempty"`

	// Parallel executes a group of sub-steps concurrently. Each sub-step
	// resolves its arguments from the workflow state as it was before the
	// group started; siblings cannot reference each other's results. Mutually
	// exclusive with tool and forEach.
	Parallel []WorkflowSubStep `json:"parallel,omitempty" yaml:"parallel,omitempty"`

	// Store indicates whether to store the step result for use in later steps.
	// +kubebuilder:default=false
	Store bool `json:"store,omitempty" yaml:"store,omitempty"`

	// AllowFailure defines if in case of an error the next step is executed or not.
	// +kubebuilder:default=false
	AllowFailure bool `json:"allowFailure,omitempty" yaml:"allowFailure,omitempty"`

	// Description provides human-readable documentation for this step's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowForEach describes a sequential loop over a list of items. The body is
// a flat list of sub-steps (no nested forEach/parallel), executed once per item.
type WorkflowForEach struct {
	// Items is a template expression that must resolve to an array, e.g.
	// "{{ .input.clusters }}". Each element is bound to the loop variable for
	// the duration of one iteration.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Items string `json:"items" yaml:"items"`

	// As is the loop variable name made available to the body as
	// "{{ .vars.<as> }}". Defaults to "item".
	// +kubebuilder:default=item
	As string `json:"as,omitempty" yaml:"as,omitempty"`

	// Steps is the body executed for each item.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Steps []WorkflowSubStep `json:"steps" yaml:"steps"`
}

// WorkflowSubStep is a tool-call step used inside forEach bodies, parallel
// groups, and onFailure handlers. Unlike WorkflowStep it cannot itself contain
// forEach or parallel, which keeps the CRD schema structural (non-recursive).
type WorkflowSubStep struct {
	// ID is the unique identifier for this sub-step.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-zA-Z0-9_-]+$"
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id" yaml:"id"`

	// Tool specifies the name of the tool to execute.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Tool string `json:"tool" yaml:"tool"`

	// Args provides arguments for the tool execution (supports templating).
	Args map[string]apiextensionsv1.JSON `json:"args,omitempty" yaml:"args,omitempty"`

	// Condition defines an optional condition that determines whether this sub-step should execute.
	Condition *WorkflowCondition `json:"condition,omitempty" yaml:"condition,omitempty"`

	// Store indicates whether to store the sub-step result for use in later steps.
	// +kubebuilder:default=false
	Store bool `json:"store,omitempty" yaml:"store,omitempty"`

	// AllowFailure defines if in case of an error execution continues.
	// +kubebuilder:default=false
	AllowFailure bool `json:"allowFailure,omitempty" yaml:"allowFailure,omitempty"`

	// Description provides human-readable documentation for this sub-step's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowCondition defines execution conditions for workflow steps
type WorkflowCondition struct {
	// Template is a boolean Go-template gate. When set, the step executes only
	// if the template renders to "true" (e.g. "{{ eq .input.env \"production\" }}").
	// Mutually exclusive with Tool/FromStep; when present, Expect/ExpectNot are ignored.
	Template string `json:"template,omitempty" yaml:"template,omitempty"`

	// Tool specifies the name of the tool to execute for condition evaluation.
	// Optional when FromStep or Template is used.
	Tool string `json:"tool,omitempty" yaml:"tool,omitempty"`

	// Args provides the arguments to pass to the condition tool.
	// Values may be any JSON type.
	Args map[string]apiextensionsv1.JSON `json:"args,omitempty" yaml:"args,omitempty"`

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
	// Values may be any JSON type (typically scalars compared to a result field).
	JsonPath map[string]apiextensionsv1.JSON `json:"jsonPath,omitempty" yaml:"jsonPath,omitempty"`
}

// WorkflowStatus defines the observed state of Workflow
type WorkflowStatus struct {
	// Valid indicates whether the Workflow spec passes structural validation.
	Valid bool `json:"valid,omitempty" yaml:"valid,omitempty"`

	// ValidationErrors contains any spec validation error messages.
	ValidationErrors []string `json:"validationErrors,omitempty" yaml:"validationErrors,omitempty"`

	// ReferencedTools lists all tools mentioned in the Workflow steps.
	// This is informational only; actual availability depends on the user's session.
	// See ADR 007 for details on session-scoped tool visibility.
	ReferencedTools []string `json:"referencedTools,omitempty" yaml:"referencedTools,omitempty"`

	// StepCount is the number of steps in the workflow.
	StepCount int `json:"stepCount,omitempty" yaml:"stepCount,omitempty"`

	// Conditions represent the latest available observations of the workflow's state.
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=wf
//+kubebuilder:printcolumn:name="Valid",type="boolean",JSONPath=".status.valid"
//+kubebuilder:printcolumn:name="Steps",type="integer",JSONPath=".status.stepCount"
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
