package kubernetes

import (
	"context"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func (k *Client) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	var obj musterv1alpha1.Workflow
	if err := k.getResource(ctx, name, namespace, kindWorkflow, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func (k *Client) ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error) {
	var list musterv1alpha1.WorkflowList
	err := k.listResources(ctx, namespace, kindWorkflow, &list)
	return list.Items, err
}

func (k *Client) CreateWorkflow(ctx context.Context, w *musterv1alpha1.Workflow) error {
	return k.createResource(ctx, kindWorkflow, w)
}

func (k *Client) UpdateWorkflow(ctx context.Context, w *musterv1alpha1.Workflow) error {
	return k.updateResource(ctx, kindWorkflow, w)
}

func (k *Client) DeleteWorkflow(ctx context.Context, name, namespace string) error {
	return k.deleteResource(ctx, name, namespace, kindWorkflow, &musterv1alpha1.Workflow{})
}

func (k *Client) UpdateWorkflowStatus(ctx context.Context, w *musterv1alpha1.Workflow) error {
	return k.updateResourceStatus(ctx, kindWorkflow, w)
}
