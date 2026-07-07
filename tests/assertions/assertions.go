package assertions

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Target identifies the installation under test, independent of which
// harness deployed it.
type Target struct {
	// Namespace the chart was deployed into.
	Namespace string
	// ReleaseName is the Helm release name; the muster chart names its
	// workload resources after it.
	ReleaseName string
	// AppVersion of the chart under test. Informational; empty is valid.
	AppVersion string
}

const pollInterval = 5 * time.Second

// ClusterReachable verifies API connectivity by listing nodes and requiring
// at least one.
func ClusterReachable(ctx context.Context, kubeClient client.Client) error {
	nodes := &corev1.NodeList{}
	if err := kubeClient.List(ctx, nodes); err != nil {
		return fmt.Errorf("listing nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return errors.New("cluster reports no nodes")
	}
	return nil
}

// DeploymentReady waits until the deployment named after the release has all
// desired replicas ready and updated for its current generation.
func DeploymentReady(ctx context.Context, kubeClient client.Client, target Target, timeout time.Duration) error {
	var lastState string
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		deployment := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: target.Namespace, Name: target.ReleaseName}
		if err := kubeClient.Get(ctx, key, deployment); err != nil {
			lastState = err.Error()
			return false, nil
		}
		desired := int32(1)
		if deployment.Spec.Replicas != nil {
			desired = *deployment.Spec.Replicas
		}
		lastState = fmt.Sprintf("observedGeneration %d/%d, readyReplicas %d/%d, updatedReplicas %d/%d",
			deployment.Status.ObservedGeneration, deployment.Generation,
			deployment.Status.ReadyReplicas, desired,
			deployment.Status.UpdatedReplicas, desired)
		ready := deployment.Status.ObservedGeneration >= deployment.Generation &&
			deployment.Status.ReadyReplicas == desired &&
			deployment.Status.UpdatedReplicas == desired
		return ready, nil
	})
	if err != nil {
		return fmt.Errorf("deployment %s/%s not ready (%s): %w", target.Namespace, target.ReleaseName, lastState, err)
	}
	return nil
}
