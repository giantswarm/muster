package basic

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/apptest-framework/v5/pkg/suite"
)

const (
	isUpgrade = false

	deploymentReadyTimeout  = 10 * time.Minute
	deploymentReadyInterval = 5 * time.Second
)

func TestBasic(t *testing.T) {
	suite.New().
		// The namespace to install the app into within the workload cluster
		WithInstallNamespace("kube-system").
		// If this is an upgrade test or not.
		// If true, the suite will first install the latest released version of the app before upgrading to the test version
		WithIsUpgrade(isUpgrade).
		WithValuesFile("./values.yaml").
		AfterClusterReady(func() {
			// Do any pre-install checks here (ensure the cluster has needed pre-reqs)
		}).
		BeforeUpgrade(func() {
			// Perform any checks between installing the latest released version
			// and upgrading it to the version to test
			// E.g. ensure that the initial install has completed and has settled before upgrading
		}).
		Tests(func() {
			It("connects to the workload cluster API", func() {
				wcClient, err := state.GetFramework().WC(state.GetCluster().Name)
				Expect(err).NotTo(HaveOccurred())

				nodes := &corev1.NodeList{}
				Expect(wcClient.List(state.GetContext(), nodes)).To(Succeed())
				Expect(nodes.Items).NotTo(BeEmpty())
			})

			It("runs the muster deployment", func() {
				wcClient, err := state.GetFramework().WC(state.GetCluster().Name)
				Expect(err).NotTo(HaveOccurred())

				app := state.GetApplication()
				// app-operator strips the cluster prefix from the App CR name
				// when naming the workload Helm release.
				releaseName := strings.TrimPrefix(app.InstallName, state.GetCluster().Name+"-")
				key := types.NamespacedName{Namespace: app.InstallNamespace, Name: releaseName}

				Eventually(func() error {
					deployment := &appsv1.Deployment{}
					if err := wcClient.Get(state.GetContext(), key, deployment); err != nil {
						return err
					}
					desired := int32(1)
					if deployment.Spec.Replicas != nil {
						desired = *deployment.Spec.Replicas
					}
					if deployment.Status.ObservedGeneration < deployment.Generation ||
						deployment.Status.ReadyReplicas != desired ||
						deployment.Status.UpdatedReplicas != desired {
						return fmt.Errorf("deployment %s: readyReplicas %d/%d, updatedReplicas %d/%d",
							key, deployment.Status.ReadyReplicas, desired, deployment.Status.UpdatedReplicas, desired)
					}
					return nil
				}).
					WithTimeout(deploymentReadyTimeout).
					WithPolling(deploymentReadyInterval).
					Should(Succeed())
			})
		}).
		Run(t, "Basic Test")
}
