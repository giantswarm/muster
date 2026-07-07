//go:build smoke || upgrade

// Chart tests run by app-test-suite's gotest executor; the cluster and the
// deployed release are handed in via KUBECONFIG and ATS_* env vars, per
// app-test-suite docs/TEST_CONTRACT.md.
package ats

import (
	"context"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func newClientset(t *testing.T) *kubernetes.Clientset {
	t.Helper()
	restConfig, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		t.Fatalf("building rest config from KUBECONFIG: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		t.Fatalf("building clientset: %v", err)
	}
	return clientset
}

func TestClusterReachable(t *testing.T) {
	nodes, err := newClientset(t).CoreV1().Nodes().List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing nodes: %v", err)
	}
	if len(nodes.Items) == 0 {
		t.Fatal("cluster reports no nodes")
	}
}

func TestDeploymentReady(t *testing.T) {
	clientset := newClientset(t)
	namespace := os.Getenv("ATS_RELEASE_NAMESPACE")
	name := os.Getenv("ATS_RELEASE_NAME")
	if namespace == "" || name == "" {
		t.Fatal("ATS_RELEASE_NAMESPACE and ATS_RELEASE_NAME must be set by ATS")
	}

	var lastState string
	err := wait.PollUntilContextTimeout(t.Context(), 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			lastState = err.Error()
			return false, nil
		}
		desired := int32(1)
		if deployment.Spec.Replicas != nil {
			desired = *deployment.Spec.Replicas
		}
		lastState = deployment.Status.String()
		return deployment.Status.ObservedGeneration >= deployment.Generation &&
			deployment.Status.ReadyReplicas == desired &&
			deployment.Status.UpdatedReplicas == desired, nil
	})
	if err != nil {
		t.Fatalf("deployment %s/%s not ready (%s): %v", namespace, name, lastState, err)
	}
}
