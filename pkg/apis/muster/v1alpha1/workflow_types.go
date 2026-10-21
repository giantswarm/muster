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

	// Output is an optional output template that shapes the workflow's
	// returned document. It is rendered once after all steps complete, against
	// .input / .results / .vars, and replaces the default
	// {execution_id, workflow, status, input, steps[], ...} response. Each leaf
	// is a Go-template/sprig expression; JSON structure is preserved so numbers
	// stay numbers and arrays stay arrays (e.g. "{{ .results.pods.items }}" or
	// "{{ len .results.events.items }}"). A leaf's type comes from the value it
	// evaluates to, not from how its rendered text looks: a single-action leaf
	// keeps its real type (a number stays a number, "{{ len .x }}" is a number),
	// and a computed string keeps its exact string form, so values whose form
	// matters (leading zeros, versions, IDs like "08" or "1.20") are preserved
	// without any coercion or workaround. Every step result is referenceable
	// here regardless of its output flag. When omitted, the default response is
	// returned unchanged.
	// +kubebuilder:validation:XPreserveUnknownFields
	Output map[string]apiextensionsv1.JSON `json:"output,omitempty" yaml:"output,omitempty"`
}

// WorkflowStep defines a single step in the workflow execution.
// A step is exactly one of: a tool call (tool), a loop (forEach), or a
// concurrent group (parallel).
// +kubebuilder:validation:XValidation:rule="(has(self.tool) ? 1 : 0) + (has(self.forEach) ? 1 : 0) + (has(self.parallel) ? 1 : 0) == 1",message="exactly one of tool, forEach, or parallel must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.paginate) || has(self.tool)",message="paginate applies to a tool step only"
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

	// Paginate makes a tool step follow the tool's next-page token and store
	// every page's items as one list. Only valid with tool.
	Paginate *WorkflowPaginate `json:"paginate,omitempty" yaml:"paginate,omitempty"`

	// JQ is a jq program that reshapes the step's result before it is stored:
	// the tool's result for a tool step, the per-iteration records for a
	// forEach step. It sees the workflow input as $input and the loop
	// variables as $vars, and must yield one value.
	JQ string `json:"jq,omitempty" yaml:"jq,omitempty"`

	// ForEach executes a body of sub-steps once per item of a list. Mutually
	// exclusive with tool and parallel.
	ForEach *WorkflowForEach `json:"forEach,omitempty" yaml:"forEach,omitempty"`

	// Parallel executes a group of sub-steps concurrently. Each sub-step
	// resolves its arguments from the workflow state as it was before the
	// group started; siblings cannot reference each other's results. Mutually
	// exclusive with tool and forEach.
	// +kubebuilder:validation:MinItems=1
	Parallel []WorkflowSubStep `json:"parallel,omitempty" yaml:"parallel,omitempty"`

	// Output indicates whether this step's result is included in the workflow's
	// returned document (what the caller receives). Every step result is always
	// referenceable by later steps via {{ .results.<id>.<field> }} regardless of
	// this flag; Output only controls visibility in the returned result. When
	// unset, the deprecated Store flag is used as a fallback.
	Output *bool `json:"output,omitempty" yaml:"output,omitempty"`

	// Store is a deprecated alias for Output. It originally also controlled
	// whether a step result was referenceable by later steps, but referencing
	// is now always available; Store now only affects result visibility and is
	// kept for backwards compatibility. Prefer Output.
	// +kubebuilder:default=false
	Store bool `json:"store,omitempty" yaml:"store,omitempty"`

	// AllowFailure defines if in case of an error the next step is executed or not.
	// +kubebuilder:default=false
	AllowFailure bool `json:"allowFailure,omitempty" yaml:"allowFailure,omitempty"`

	// Description provides human-readable documentation for this step's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowForEach describes a loop over a list of items. The body is a list
// of sub-steps executed once per item; a body sub-step may itself be a
// forEach, one level deep.
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

	// Parallelism is how many iterations may run at once. Unset, they run
	// one after another. Results, step records and the loop's per-iteration
	// records come out in item order either way.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	Parallelism int `json:"parallelism,omitempty" yaml:"parallelism,omitempty"`

	// Steps is the body executed for each item.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Steps []WorkflowSubStep `json:"steps" yaml:"steps"`
}

// WorkflowPaginate describes how a tool step follows the tool's pages: it
// passes the token found at tokenField back as the tokenArg argument until
// the token is empty or maxPages pages were fetched, and concatenates the
// list found at itemsField. The stored result is the last page with that list
// holding every page's items, plus pagination.pages and pagination.truncated;
// a truncated result keeps the token to resume from. Field names may be
// dotted paths ("data.next").
type WorkflowPaginate struct {
	// ItemsField is where a page's list is. Defaults to "items".
	// +kubebuilder:validation:MinLength=1
	ItemsField string `json:"itemsField,omitempty" yaml:"itemsField,omitempty"`

	// TokenField is where a page's next-page token is. Defaults to "continue".
	// +kubebuilder:validation:MinLength=1
	TokenField string `json:"tokenField,omitempty" yaml:"tokenField,omitempty"`

	// TokenArg is the tool argument the token is passed in. Defaults to
	// "continue".
	// +kubebuilder:validation:MinLength=1
	TokenArg string `json:"tokenArg,omitempty" yaml:"tokenArg,omitempty"`

	// MaxPages bounds the pages fetched. Defaults to 10.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	MaxPages int `json:"maxPages,omitempty" yaml:"maxPages,omitempty"`
}

// WorkflowSubStep is a step used inside forEach bodies, parallel groups, and
// onFailure handlers: a tool call or, in a forEach body only, a nested
// forEach. Unlike WorkflowStep it cannot hold a parallel group, and a nested
// forEach holds tool calls only, which keeps the CRD schema structural
// (non-recursive).
// +kubebuilder:validation:XValidation:rule="(has(self.tool) ? 1 : 0) + (has(self.forEach) ? 1 : 0) == 1",message="exactly one of tool or forEach must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.paginate) || has(self.tool)",message="paginate applies to a tool step only"
type WorkflowSubStep struct {
	// ID is the unique identifier for this sub-step.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-zA-Z0-9_-]+$"
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id" yaml:"id"`

	// Tool specifies the name of the tool to execute. Mutually exclusive
	// with forEach.
	// +kubebuilder:validation:MinLength=1
	Tool string `json:"tool,omitempty" yaml:"tool,omitempty"`

	// Args provides arguments for the tool execution (supports templating).
	Args map[string]apiextensionsv1.JSON `json:"args,omitempty" yaml:"args,omitempty"`

	// Condition defines an optional condition that determines whether this sub-step should execute.
	Condition *WorkflowCondition `json:"condition,omitempty" yaml:"condition,omitempty"`

	// ForEach makes this sub-step a nested loop, one level deep. Only valid in
	// a forEach body (not in parallel or onFailure); its loop variable must
	// differ from the outer one. Mutually exclusive with tool.
	ForEach *WorkflowNestedForEach `json:"forEach,omitempty" yaml:"forEach,omitempty"`

	// Paginate makes a tool sub-step follow the tool's pages (see
	// WorkflowStep.paginate).
	Paginate *WorkflowPaginate `json:"paginate,omitempty" yaml:"paginate,omitempty"`

	// JQ reshapes the sub-step's result before it is stored (see
	// WorkflowStep.jq). On a nested forEach it reshapes the loop's
	// per-iteration records.
	JQ string `json:"jq,omitempty" yaml:"jq,omitempty"`

	// Output indicates whether this sub-step's result is included in the
	// workflow's returned document. The result is always referenceable by later
	// steps regardless of this flag. When unset, the deprecated Store flag is
	// used as a fallback.
	Output *bool `json:"output,omitempty" yaml:"output,omitempty"`

	// Store is a deprecated alias for Output, kept for backwards compatibility.
	// Prefer Output.
	// +kubebuilder:default=false
	Store bool `json:"store,omitempty" yaml:"store,omitempty"`

	// AllowFailure defines if in case of an error execution continues.
	// +kubebuilder:default=false
	AllowFailure bool `json:"allowFailure,omitempty" yaml:"allowFailure,omitempty"`

	// Description provides human-readable documentation for this sub-step's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowNestedForEach is a forEach inside a forEach body. Its body is tool
// calls only.
type WorkflowNestedForEach struct {
	// Items is a template expression that must resolve to an array, typically
	// a result of an earlier sub-step of the outer iteration, e.g.
	// "{{ .results.clusters.items }}".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Items string `json:"items" yaml:"items"`

	// As is the loop variable name made available to the body as
	// "{{ .vars.<as> }}". Defaults to "item"; it must differ from the outer
	// loop's.
	// +kubebuilder:default=item
	As string `json:"as,omitempty" yaml:"as,omitempty"`

	// Parallelism is how many iterations may run at once within one outer
	// iteration. The calls in flight at once are at most the outer
	// parallelism times this one.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	Parallelism int `json:"parallelism,omitempty" yaml:"parallelism,omitempty"`

	// Steps is the body executed for each item.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Steps []WorkflowNestedStep `json:"steps" yaml:"steps"`
}

// WorkflowNestedStep is a tool-call step in the body of a nested forEach.
type WorkflowNestedStep struct {
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

	// Paginate makes the sub-step follow the tool's pages (see
	// WorkflowStep.paginate).
	Paginate *WorkflowPaginate `json:"paginate,omitempty" yaml:"paginate,omitempty"`

	// JQ reshapes the sub-step's result before it is stored (see
	// WorkflowStep.jq).
	JQ string `json:"jq,omitempty" yaml:"jq,omitempty"`

	// Output indicates whether this sub-step's result is included in the
	// workflow's returned document. The result is always referenceable by
	// later steps regardless of this flag.
	Output *bool `json:"output,omitempty" yaml:"output,omitempty"`

	// Store is a deprecated alias for Output, kept for backwards compatibility.
	// Prefer Output.
	// +kubebuilder:default=false
	Store bool `json:"store,omitempty" yaml:"store,omitempty"`

	// AllowFailure defines if in case of an error execution continues.
	// +kubebuilder:default=false
	AllowFailure bool `json:"allowFailure,omitempty" yaml:"allowFailure,omitempty"`

	// Description provides human-readable documentation for this sub-step's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowCondition defines execution conditions for workflow steps.
// A condition selects its evaluation source with exactly one of Template, Tool,
// or FromStep. A tool/fromStep condition must declare an Expect or ExpectNot:
// without one the executor falls back to expecting the call to fail, which is
// rarely intended.
// +kubebuilder:validation:XValidation:rule="(has(self.template) ? 1 : 0) + (has(self.tool) ? 1 : 0) + (has(self.fromStep) ? 1 : 0) == 1",message="exactly one of template, tool, or fromStep must be set"
// +kubebuilder:validation:XValidation:rule="has(self.template) || has(self.expect) || has(self.expectNot)",message="a tool or fromStep condition requires expect or expectNot"
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
