package filesystem

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func (f *Client) GetWorkflow(_ context.Context, name, _ string) (*musterv1alpha1.Workflow, error) {
	var obj musterv1alpha1.Workflow
	if err := f.getResource(name, &obj, workflowMeta); err != nil {
		return nil, err
	}
	return &obj, nil
}

func (f *Client) ListWorkflows(_ context.Context, _ string) ([]musterv1alpha1.Workflow, error) {
	var list musterv1alpha1.WorkflowList
	err := f.listResources(&list, func() client.Object { return &musterv1alpha1.Workflow{} }, workflowMeta)
	return list.Items, err
}

func (f *Client) CreateWorkflow(_ context.Context, w *musterv1alpha1.Workflow) error {
	return f.createResource(w, workflowMeta)
}

func (f *Client) UpdateWorkflow(_ context.Context, w *musterv1alpha1.Workflow) error {
	return f.updateResource(w, workflowMeta)
}

func (f *Client) DeleteWorkflow(_ context.Context, name, _ string) error {
	return f.deleteResource(name, workflowMeta)
}

// UpdateWorkflowStatus rewrites the entire YAML — filesystem mode embeds
// status alongside spec, so there is no separate status sub-resource.
func (f *Client) UpdateWorkflowStatus(ctx context.Context, w *musterv1alpha1.Workflow) error {
	return f.UpdateWorkflow(ctx, w)
}
