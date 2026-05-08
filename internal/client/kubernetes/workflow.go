package kubernetes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// GetWorkflow retrieves a specific Workflow from Kubernetes.
func (k *Client) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	workflow := &musterv1alpha1.Workflow{}
	key := client.ObjectKey{Name: name, Namespace: namespace}

	if err := k.Get(ctx, key, workflow); err != nil {
		return nil, err
	}

	return workflow, nil
}

// ListWorkflows lists all Workflows in a namespace from Kubernetes.
func (k *Client) ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error) {
	workflowList := &musterv1alpha1.WorkflowList{}
	listOptions := &client.ListOptions{Namespace: namespace}

	if err := k.List(ctx, workflowList, listOptions); err != nil {
		return nil, err
	}

	return workflowList.Items, nil
}

// CreateWorkflow creates a new Workflow in Kubernetes.
func (k *Client) CreateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return k.Create(ctx, workflow)
}

// UpdateWorkflow updates an existing Workflow in Kubernetes.
func (k *Client) UpdateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return k.Update(ctx, workflow)
}

// DeleteWorkflow deletes a Workflow from Kubernetes.
func (k *Client) DeleteWorkflow(ctx context.Context, name, namespace string) error {
	workflow := &musterv1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	return k.Delete(ctx, workflow)
}

// UpdateWorkflowStatus updates only the status subresource of a Workflow.
func (k *Client) UpdateWorkflowStatus(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	if err := k.Client.Status().Update(ctx, workflow); err != nil {
		return fmt.Errorf("failed to update Workflow status %s/%s: %w", workflow.Namespace, workflow.Name, err)
	}
	return nil
}
