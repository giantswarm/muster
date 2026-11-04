package workflow

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/events"
)

// updateWorkflow validates the structured arguments and updates the named
// Workflow CR through writer (see createWorkflow for the identity contract).
//
// The update is a get-modify-write: the read of the current CR stays on the
// SA client — only the spec mutation itself switches to the caller's identity
// — and carries the resourceVersion the apiserver requires for CR updates (a
// fresh object without one is rejected). Conflicts with muster's own status
// reconciler bumping the resourceVersion are retried on a fresh read.
func (a *Adapter) updateWorkflow(ctx context.Context, writer workflowWriter, name string, args map[string]interface{}, tags *[]string) error {
	// Validate the workflow before updating it
	if err := a.ValidateWorkflowFromStructured(args); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Convert structured arguments to api.Workflow
	wf, err := convertToWorkflow(args)
	if err != nil {
		return err
	}

	// Ensure the name matches
	wf.Name = name

	// Only the spec is applied onto the live object, and tags (nil keeps
	// them) onto its annotations.
	spec := a.convertWorkflowToCRD(&wf).Spec

	apply := func() error {
		existing, err := a.client.GetWorkflow(ctx, name, a.namespace)
		if err != nil {
			return err
		}
		existing.Spec = spec
		if existing.Annotations, err = api.ApplyTagsUpdate(existing.Annotations, tags); err != nil {
			return err
		}
		return writer.UpdateWorkflow(ctx, existing)
	}
	err = apply()
	if apierrors.IsConflict(err) {
		err = retry.RetryOnConflict(retry.DefaultRetry, apply)
	}
	var invalidTags *api.InvalidTagsError
	if errors.As(err, &invalidTags) {
		// A refused request, not a failed update: no event.
		return err
	}
	if err != nil {
		// Generate failure event
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: "update",
		})
		return fmt.Errorf("failed to update workflow: %w", err)
	}

	// Generate success event for CRD update
	a.generateCRDEvent(wf.Name, events.ReasonWorkflowUpdated, events.EventData{
		Operation: "update",
		StepCount: len(wf.Steps),
	})

	return nil
}
