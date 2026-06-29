package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
	"github.com/giantswarm/muster/pkg/logging"
)

// Label keys used to index WorkflowExecution records so List can filter
// server-side by workflow name and status before sorting/paginating in memory.
//
// ponytail: workflow names are used verbatim as label values. CRD names are
// DNS-1123 compliant but may exceed the 63-character label-value ceiling; such
// a name would make Store fail when the API server rejects the label. In
// practice muster workflow names are short, so this is acceptable for v1. The
// upgrade path is to hash long names or switch to in-memory-only filtering.
const (
	labelWorkflow = "muster.giantswarm.io/workflow"
	labelStatus   = "muster.giantswarm.io/status"
)

// RetentionPolicy bounds how old a record may be and how many records are kept
// before the retention GC prunes it. The zero value is a no-op (keep
// everything), so an unset policy never deletes records.
type RetentionPolicy struct {
	// MaxAge deletes records whose StartedAt is older than now-MaxAge. A zero
	// value disables age-based pruning.
	MaxAge time.Duration

	// MaxCount keeps only the newest MaxCount records (by StartedAt). A zero
	// value disables count-based pruning.
	MaxCount int
}

// k8sExecutionStorage is the Kubernetes-backed ExecutionStorage. It persists
// each workflow run as a WorkflowExecution CRD via the controller-runtime
// client embedded in MusterClient, so records survive process restarts and are
// visible across replicas and through kubectl.
type k8sExecutionStorage struct {
	client    crclient.Client
	namespace string

	// now is injectable so the retention GC is unit-testable without sleeping.
	now func() time.Time
}

// newK8sExecutionStorage builds a Kubernetes-backed execution storage rooted in
// the given namespace.
func newK8sExecutionStorage(c crclient.Client, namespace string) *k8sExecutionStorage {
	if namespace == "" {
		namespace = "default"
	}
	return &k8sExecutionStorage{
		client:    c,
		namespace: namespace,
		now:       time.Now,
	}
}

// Store upserts a workflow execution record. TrackExecution stores twice (an
// initial in-progress record and a final record under the same ID), so Store
// creates the object on first write and updates it thereafter.
func (s *k8sExecutionStorage) Store(ctx context.Context, execution *api.WorkflowExecution) error {
	desired := s.executionToCRD(execution)

	var existing musterv1alpha1.WorkflowExecution
	err := s.client.Get(ctx, types.NamespacedName{Name: execution.ExecutionID, Namespace: s.namespace}, &existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if createErr := s.client.Create(ctx, desired); createErr != nil {
				return fmt.Errorf("failed to create execution %s: %w", execution.ExecutionID, createErr)
			}
			logging.Debug("ExecutionStorage", "Stored execution %s for workflow %s", execution.ExecutionID, execution.WorkflowName)
			return nil
		}
		return fmt.Errorf("failed to read execution %s before store: %w", execution.ExecutionID, err)
	}

	// Update in place, preserving the existing resourceVersion so the update is
	// an optimistic-concurrency-safe overwrite of the spec and labels.
	existing.Labels = desired.Labels
	existing.Spec = desired.Spec
	if updateErr := s.client.Update(ctx, &existing); updateErr != nil {
		return fmt.Errorf("failed to update execution %s: %w", execution.ExecutionID, updateErr)
	}

	logging.Debug("ExecutionStorage", "Updated execution %s for workflow %s", execution.ExecutionID, execution.WorkflowName)
	return nil
}

// Get retrieves a specific workflow execution by ID.
func (s *k8sExecutionStorage) Get(ctx context.Context, executionID string) (*api.WorkflowExecution, error) {
	var crd musterv1alpha1.WorkflowExecution
	if err := s.client.Get(ctx, types.NamespacedName{Name: executionID, Namespace: s.namespace}, &crd); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("execution %s not found", executionID)
		}
		return nil, fmt.Errorf("failed to get execution %s: %w", executionID, err)
	}
	return crdToExecution(&crd), nil
}

// List returns paginated workflow executions, filtering server-side by workflow
// name and status label selectors then sorting and paginating in memory.
func (s *k8sExecutionStorage) List(ctx context.Context, req *api.ListWorkflowExecutionsRequest) (*api.ListWorkflowExecutionsResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	opts := []crclient.ListOption{crclient.InNamespace(s.namespace)}
	matching := crclient.MatchingLabels{}
	if req.WorkflowName != "" {
		matching[labelWorkflow] = req.WorkflowName
	}
	if req.Status != "" {
		matching[labelStatus] = string(req.Status)
	}
	if len(matching) > 0 {
		opts = append(opts, matching)
	}

	var list musterv1alpha1.WorkflowExecutionList
	if err := s.client.List(ctx, &list, opts...); err != nil {
		return nil, fmt.Errorf("failed to list executions: %w", err)
	}

	summaries := make([]api.WorkflowExecutionSummary, 0, len(list.Items))
	for i := range list.Items {
		summaries = append(summaries, crdToSummary(&list.Items[i]))
	}

	// Sort by StartedAt descending (most recent first).
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].StartedAt.After(summaries[j].StartedAt)
	})

	total := len(summaries)

	var paged []api.WorkflowExecutionSummary
	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		paged = summaries[offset:end]
	}

	hasMore := offset+len(paged) < total

	logging.Debug("ExecutionStorage", "Listed %d executions (total: %d, offset: %d, limit: %d)",
		len(paged), total, offset, limit)

	return &api.ListWorkflowExecutionsResponse{
		Executions: paged,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
	}, nil
}

// Delete removes a workflow execution record by ID.
func (s *k8sExecutionStorage) Delete(ctx context.Context, executionID string) error {
	crd := &musterv1alpha1.WorkflowExecution{
		ObjectMeta: metav1.ObjectMeta{Name: executionID, Namespace: s.namespace},
	}
	if err := s.client.Delete(ctx, crd); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("execution %s not found", executionID)
		}
		return fmt.Errorf("failed to delete execution %s: %w", executionID, err)
	}
	logging.Debug("ExecutionStorage", "Deleted execution %s", executionID)
	return nil
}

// Prune deletes execution records that violate the retention policy: records
// older than MaxAge and/or records beyond the newest MaxCount. It returns the
// number of records deleted. An empty policy deletes nothing.
func (s *k8sExecutionStorage) Prune(ctx context.Context, policy RetentionPolicy) (int, error) {
	if policy.MaxAge <= 0 && policy.MaxCount <= 0 {
		return 0, nil
	}

	var list musterv1alpha1.WorkflowExecutionList
	if err := s.client.List(ctx, &list, crclient.InNamespace(s.namespace)); err != nil {
		return 0, fmt.Errorf("failed to list executions for pruning: %w", err)
	}
	items := list.Items

	toDelete := make(map[int]bool)

	if policy.MaxAge > 0 {
		cutoff := s.now().Add(-policy.MaxAge)
		for i := range items {
			if items[i].Spec.StartedAt.Time.Before(cutoff) {
				toDelete[i] = true
			}
		}
	}

	if policy.MaxCount > 0 && len(items) > policy.MaxCount {
		order := make([]int, len(items))
		for i := range order {
			order[i] = i
		}
		// Newest first so the oldest records fall past the count cap.
		sort.SliceStable(order, func(a, b int) bool {
			return items[order[a]].Spec.StartedAt.After(items[order[b]].Spec.StartedAt.Time)
		})
		for _, i := range order[policy.MaxCount:] {
			toDelete[i] = true
		}
	}

	deleted := 0
	for i := range items {
		if !toDelete[i] {
			continue
		}
		if err := s.client.Delete(ctx, &items[i]); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return deleted, fmt.Errorf("failed to prune execution %s: %w", items[i].Name, err)
		}
		deleted++
	}

	if deleted > 0 {
		logging.Debug("ExecutionStorage", "Pruned %d execution(s)", deleted)
	}
	return deleted, nil
}

// executionToCRD converts an api.WorkflowExecution into its CRD representation,
// including the workflow/status labels used for List filtering.
func (s *k8sExecutionStorage) executionToCRD(execution *api.WorkflowExecution) *musterv1alpha1.WorkflowExecution {
	spec := musterv1alpha1.WorkflowExecutionSpec{
		WorkflowName: execution.WorkflowName,
		Status:       string(execution.Status),
		StartedAt:    metav1.NewTime(execution.StartedAt),
		DurationMs:   execution.DurationMs,
		Input:        toJSON(execution.Input),
		Result:       toJSON(execution.Result),
		Error:        execution.Error,
		Steps:        stepsToCRD(execution.Steps),
		Truncated:    execution.Truncated,
	}
	if execution.CompletedAt != nil {
		completed := metav1.NewTime(*execution.CompletedAt)
		spec.CompletedAt = &completed
	}

	return &musterv1alpha1.WorkflowExecution{
		ObjectMeta: metav1.ObjectMeta{
			Name:      execution.ExecutionID,
			Namespace: s.namespace,
			Labels: map[string]string{
				labelWorkflow: execution.WorkflowName,
				labelStatus:   string(execution.Status),
			},
		},
		Spec: spec,
	}
}

// crdToExecution converts a WorkflowExecution CRD back into the api type.
func crdToExecution(crd *musterv1alpha1.WorkflowExecution) *api.WorkflowExecution {
	execution := &api.WorkflowExecution{
		ExecutionID:  crd.Name,
		WorkflowName: crd.Spec.WorkflowName,
		Status:       api.WorkflowExecutionStatus(crd.Spec.Status),
		StartedAt:    crd.Spec.StartedAt.Time,
		DurationMs:   crd.Spec.DurationMs,
		Input:        mapFromJSON(crd.Spec.Input),
		Result:       fromJSON(crd.Spec.Result),
		Error:        crd.Spec.Error,
		Steps:        stepsFromCRD(crd.Spec.Steps),
		Truncated:    crd.Spec.Truncated,
	}
	if crd.Spec.CompletedAt != nil {
		t := crd.Spec.CompletedAt.Time
		execution.CompletedAt = &t
	}
	return execution
}

// crdToSummary builds the lightweight list summary from a CRD record.
func crdToSummary(crd *musterv1alpha1.WorkflowExecution) api.WorkflowExecutionSummary {
	summary := api.WorkflowExecutionSummary{
		ExecutionID:  crd.Name,
		WorkflowName: crd.Spec.WorkflowName,
		Status:       api.WorkflowExecutionStatus(crd.Spec.Status),
		StartedAt:    crd.Spec.StartedAt.Time,
		DurationMs:   crd.Spec.DurationMs,
		StepCount:    len(crd.Spec.Steps),
		Error:        crd.Spec.Error,
	}
	if crd.Spec.CompletedAt != nil {
		t := crd.Spec.CompletedAt.Time
		summary.CompletedAt = &t
	}
	return summary
}

// stepsToCRD converts api step records into their CRD form.
func stepsToCRD(steps []api.WorkflowExecutionStep) []musterv1alpha1.WorkflowExecutionStepRecord {
	if len(steps) == 0 {
		return nil
	}
	records := make([]musterv1alpha1.WorkflowExecutionStepRecord, 0, len(steps))
	for _, step := range steps {
		record := musterv1alpha1.WorkflowExecutionStepRecord{
			StepID:     step.StepID,
			Tool:       step.Tool,
			Status:     string(step.Status),
			StartedAt:  metav1.NewTime(step.StartedAt),
			DurationMs: step.DurationMs,
			Input:      toJSON(step.Input),
			Result:     toJSON(step.Result),
			Error:      step.Error,
			StoredAs:   step.StoredAs,
		}
		if step.CompletedAt != nil {
			completed := metav1.NewTime(*step.CompletedAt)
			record.CompletedAt = &completed
		}
		records = append(records, record)
	}
	return records
}

// stepsFromCRD converts CRD step records back into the api form.
func stepsFromCRD(records []musterv1alpha1.WorkflowExecutionStepRecord) []api.WorkflowExecutionStep {
	if len(records) == 0 {
		return nil
	}
	steps := make([]api.WorkflowExecutionStep, 0, len(records))
	for _, record := range records {
		step := api.WorkflowExecutionStep{
			StepID:     record.StepID,
			Tool:       record.Tool,
			Status:     api.WorkflowExecutionStatus(record.Status),
			StartedAt:  record.StartedAt.Time,
			DurationMs: record.DurationMs,
			Input:      mapFromJSON(record.Input),
			Result:     fromJSON(record.Result),
			Error:      record.Error,
			StoredAs:   record.StoredAs,
		}
		if record.CompletedAt != nil {
			t := record.CompletedAt.Time
			step.CompletedAt = &t
		}
		steps = append(steps, step)
	}
	return steps
}

// toJSON marshals an arbitrary value into the CRD's raw-JSON wrapper, returning
// nil for nil input so empty fields stay unset.
func toJSON(v interface{}) *apiextensionsv1.JSON {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return &apiextensionsv1.JSON{Raw: raw}
}

// fromJSON unmarshals the CRD's raw-JSON wrapper back into a generic value.
func fromJSON(j *apiextensionsv1.JSON) interface{} {
	if j == nil || len(j.Raw) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(j.Raw, &v); err != nil {
		return nil
	}
	return v
}

// mapFromJSON unmarshals the CRD's raw-JSON wrapper into a string-keyed map,
// matching the api type's Input field shape.
func mapFromJSON(j *apiextensionsv1.JSON) map[string]interface{} {
	if j == nil || len(j.Raw) == 0 {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(j.Raw, &m); err != nil {
		return nil
	}
	return m
}
