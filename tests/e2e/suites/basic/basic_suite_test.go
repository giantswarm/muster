package basic

import (
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/apptest-framework/v5/pkg/suite"

	"github.com/giantswarm/muster/tests/assertions"
)

const (
	isUpgrade = false

	deploymentReadyTimeout = 10 * time.Minute
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
				Expect(assertions.ClusterReachable(state.GetContext(), wcClient)).To(Succeed())
			})

			It("runs the muster deployment", func() {
				wcClient, err := state.GetFramework().WC(state.GetCluster().Name)
				Expect(err).NotTo(HaveOccurred())

				app := state.GetApplication()
				// app-operator strips the cluster prefix from the App CR name
				// when naming the workload Helm release.
				releaseName := strings.TrimPrefix(app.InstallName, state.GetCluster().Name+"-")
				target := assertions.Target{
					Namespace:   app.InstallNamespace,
					ReleaseName: releaseName,
					AppVersion:  app.Version,
				}
				Expect(assertions.DeploymentReady(state.GetContext(), wcClient, target, deploymentReadyTimeout)).To(Succeed())
			})
		}).
		Run(t, "Basic Test")
}
