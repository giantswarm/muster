package assertions

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func newDeployment(replicas, ready, updated int32, generation, observed int64) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "muster",
			Namespace:  "muster",
			Generation: generation,
		},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: observed,
			ReadyReplicas:      ready,
			UpdatedReplicas:    updated,
		},
	}
}

func TestClusterReachable(t *testing.T) {
	kubeClient := newFakeClient(t, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}})
	require.NoError(t, ClusterReachable(t.Context(), kubeClient))
}

func TestClusterReachableNoNodes(t *testing.T) {
	kubeClient := newFakeClient(t)
	require.ErrorContains(t, ClusterReachable(t.Context(), kubeClient), "no nodes")
}

func TestDeploymentReady(t *testing.T) {
	kubeClient := newFakeClient(t, newDeployment(2, 2, 2, 3, 3))
	target := Target{Namespace: "muster", ReleaseName: "muster"}
	require.NoError(t, DeploymentReady(t.Context(), kubeClient, target, time.Minute))
}

func TestDeploymentReadyTimesOut(t *testing.T) {
	cases := map[string]*appsv1.Deployment{
		"missing":          nil,
		"unready replicas": newDeployment(2, 1, 2, 3, 3),
		"stale generation": newDeployment(2, 2, 2, 4, 3),
	}
	for name, deployment := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var objects []client.Object
				if deployment != nil {
					objects = append(objects, deployment)
				}
				kubeClient := newFakeClient(t, objects...)
				target := Target{Namespace: "muster", ReleaseName: "muster"}
				err := DeploymentReady(t.Context(), kubeClient, target, time.Minute)
				require.ErrorContains(t, err, "muster/muster not ready")
			})
		})
	}
}
