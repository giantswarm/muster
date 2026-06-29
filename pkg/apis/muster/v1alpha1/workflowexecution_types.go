package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkflowExecutionSpec is the durable record of a single workflow run.
//
// A WorkflowExecution is an immutable, append-only record: it captures what
// happened during one execution of a workflow and is never reconciled, so the
// type intentionally has no status subresource. The object's metadata.name is
// the execution UUID. It mirrors api.WorkflowExecution so the in-memory and
// Kubernetes-backed representations stay aligned.
type WorkflowExecutionSpec struct {
	// WorkflowName is the name of the workflow that was executed.
	// +kubebuilder:validation:Required
	WorkflowName string `json:"workflowName" yaml:"workflowName"`

	// Status indicates the final (or current) state of the execution.
	// +kubebuilder:validation:Enum=inprogress;completed;failed
	Status string `json:"status" yaml:"status"`

	// StartedAt is the timestamp when the execution began.
	// +kubebuilder:validation:Required
	StartedAt metav1.Time `json:"startedAt" yaml:"startedAt"`

	// CompletedAt is the timestamp when the execution finished (nil if still running).
	CompletedAt *metav1.Time `json:"completedAt,omitempty" yaml:"completedAt,omitempty"`

	// DurationMs is the total execution duration in milliseconds.
	DurationMs int64 `json:"durationMs,omitempty" yaml:"durationMs,omitempty"`

	// Input contains the original arguments passed to the workflow.
	// +kubebuilder:validation:XPreserveUnknownFields
	Input *apiextensionsv1.JSON `json:"input,omitempty" yaml:"input,omitempty"`

	// Result contains the final result of the workflow execution
	// (nil if failed or in progress).
	// +kubebuilder:validation:XPreserveUnknownFields
	Result *apiextensionsv1.JSON `json:"result,omitempty" yaml:"result,omitempty"`

	// Error contains error information if the execution failed (nil if successful).
	Error *string `json:"error,omitempty" yaml:"error,omitempty"`

	// Steps contains detailed information about each step execution.
	Steps []WorkflowExecutionStepRecord `json:"steps,omitempty" yaml:"steps,omitempty"`

	// Truncated indicates that oversized payloads (the workflow/step Input
	// and/or Result fields) were truncated to keep the stored record within
	// size limits.
	Truncated bool `json:"truncated,omitempty" yaml:"truncated,omitempty"`
}

// WorkflowExecutionStepRecord is the durable record of a single step execution
// within a workflow run. It mirrors api.WorkflowExecutionStep.
type WorkflowExecutionStepRecord struct {
	// StepID is the unique identifier for this step within the workflow.
	// +kubebuilder:validation:Required
	StepID string `json:"stepId" yaml:"stepId"`

	// Tool is the name of the tool that was executed for this step.
	Tool string `json:"tool,omitempty" yaml:"tool,omitempty"`

	// Status indicates the final (or current) state of the step execution.
	// +kubebuilder:validation:Enum=inprogress;completed;failed
	Status string `json:"status" yaml:"status"`

	// StartedAt is the timestamp when the step execution began.
	StartedAt metav1.Time `json:"startedAt,omitempty" yaml:"startedAt,omitempty"`

	// CompletedAt is the timestamp when the step execution finished (nil if still running).
	CompletedAt *metav1.Time `json:"completedAt,omitempty" yaml:"completedAt,omitempty"`

	// DurationMs is the step execution duration in milliseconds.
	DurationMs int64 `json:"durationMs,omitempty" yaml:"durationMs,omitempty"`

	// Input contains the resolved arguments passed to the tool for this step.
	// +kubebuilder:validation:XPreserveUnknownFields
	Input *apiextensionsv1.JSON `json:"input,omitempty" yaml:"input,omitempty"`

	// Result contains the result returned by the tool execution
	// (nil if failed or in progress).
	// +kubebuilder:validation:XPreserveUnknownFields
	Result *apiextensionsv1.JSON `json:"result,omitempty" yaml:"result,omitempty"`

	// Error contains error information if the step failed (nil if successful).
	Error *string `json:"error,omitempty" yaml:"error,omitempty"`

	// StoredAs is the variable name where the step result was stored.
	StoredAs string `json:"storedAs,omitempty" yaml:"storedAs,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:shortName=wfe
//+kubebuilder:printcolumn:name="Workflow",type="string",JSONPath=".spec.workflowName"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".spec.status"
//+kubebuilder:printcolumn:name="Duration(ms)",type="integer",JSONPath=".spec.durationMs"
//+kubebuilder:printcolumn:name="Started",type="date",JSONPath=".spec.startedAt"

// WorkflowExecution is the Schema for the workflowexecutions API. It is an
// immutable, durable record of a single workflow run and therefore has no
// status subresource.
type WorkflowExecution struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkflowExecutionSpec `json:"spec,omitempty"`
}

//+kubebuilder:object:root=true

// WorkflowExecutionList contains a list of WorkflowExecution
type WorkflowExecutionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowExecution `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkflowExecution{}, &WorkflowExecutionList{})
}
