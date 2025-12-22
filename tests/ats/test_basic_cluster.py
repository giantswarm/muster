import logging
from typing import List

import pykube
import pytest
from pytest_helm_charts.clusters import Cluster
from pytest_helm_charts.k8s.deployment import wait_for_deployments_to_run

logger = logging.getLogger(__name__)

deployment_name = "muster"
namespace_name = "muster"

timeout: int = 560


@pytest.mark.smoke
def test_api_working(kube_cluster: Cluster) -> None:
    """Test that we can connect to the Kubernetes API."""
    assert kube_cluster.kube_client is not None
    assert len(pykube.Node.objects(kube_cluster.kube_client)) >= 1


@pytest.fixture(scope="module")
def deployment(request, kube_cluster: Cluster) -> List[pykube.Deployment]:
    logger.info("Waiting for muster deployment..")

    deployment_ready = wait_for_deployment(kube_cluster)

    logger.info("muster deployment looks satisfied..")

    return deployment_ready


def wait_for_deployment(kube_cluster: Cluster) -> List[pykube.Deployment]:
    deployments = wait_for_deployments_to_run(
        kube_cluster.kube_client,
        [deployment_name],
        namespace_name,
        timeout,
    )
    return deployments


@pytest.mark.smoke
@pytest.mark.upgrade
@pytest.mark.flaky(reruns=5, reruns_delay=10)
def test_pods_available(kube_cluster: Cluster, deployment: List[pykube.Deployment]):
    for s in deployment:
        assert int(s.obj["status"]["readyReplicas"]) == int(
            s.obj["spec"]["replicas"])
