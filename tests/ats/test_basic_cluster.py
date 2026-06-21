import logging
import os
import subprocess
import time
from typing import Generator, List

import pykube
import pytest
import requests
from pytest_helm_charts.clusters import Cluster

logger = logging.getLogger(__name__)

deployment_name = "muster"
namespace_name = "muster"

timeout: int = 180


def _wait_for_labeled_deployments(kube_client: pykube.HTTPClient, timeout_sec: int) -> List[pykube.Deployment]:
    """Wait for deployments with app.kubernetes.io/name=muster to become ready.

    ct install always appends a random suffix to the release name (e.g. muster-abc123),
    so we discover deployments via label selector rather than exact name.
    """
    label_selector = {"app.kubernetes.io/name": deployment_name}
    for _ in range(timeout_sec):
        deps = list(
            pykube.Deployment.objects(kube_client)
            .filter(namespace=namespace_name)
            .filter(selector=label_selector)
        )
        if deps and all(
            dep.obj.get("status", {}).get("readyReplicas", 0) >= 1 for dep in deps
        ):
            return deps
        time.sleep(1)
    raise TimeoutError(f"No ready deployment with labels {label_selector} after {timeout_sec}s")


@pytest.mark.smoke
def test_api_working(kube_cluster: Cluster) -> None:
    assert kube_cluster.kube_client is not None
    assert len(pykube.Node.objects(kube_cluster.kube_client)) >= 1


@pytest.fixture(scope="module")
def deployment(kube_cluster: Cluster) -> List[pykube.Deployment]:
    return _wait_for_labeled_deployments(kube_cluster.kube_client, timeout)


@pytest.fixture(scope="module")
def muster_url(kube_cluster: Cluster, deployment: List[pykube.Deployment]) -> Generator[str, None, None]:
    kubeconfig = os.environ.get("KUBECONFIG", "")
    label_selector = {"app.kubernetes.io/name": deployment_name}
    services = list(
        pykube.Service.objects(kube_cluster.kube_client)
        .filter(namespace=namespace_name)
        .filter(selector=label_selector)
    )
    if not services:
        raise RuntimeError(f"No service with labels {label_selector} in {namespace_name}")
    svc_name = services[0].obj["metadata"]["name"]
    proc = subprocess.Popen(
        ["kubectl", f"--kubeconfig={kubeconfig}", "-n", namespace_name,
         "port-forward", f"svc/{svc_name}", "18090:8090"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    time.sleep(2)
    try:
        yield "http://localhost:18090"
    finally:
        proc.terminate()
        proc.wait()


@pytest.mark.smoke
@pytest.mark.upgrade
@pytest.mark.flaky(reruns=1, reruns_delay=15)
def test_pods_available(kube_cluster: Cluster, deployment: List[pykube.Deployment]) -> None:
    for s in deployment:
        assert int(s.obj["status"]["readyReplicas"]) == int(s.obj["spec"]["replicas"])


@pytest.mark.functional
def test_health_endpoint(muster_url: str) -> None:
    resp = requests.get(f"{muster_url}/health", timeout=10)
    assert resp.status_code == 200


@pytest.mark.upgrade
def test_deployment_ready_after_upgrade(kube_cluster: Cluster) -> None:
    deployments = _wait_for_labeled_deployments(kube_cluster.kube_client, timeout)
    assert len(deployments) >= 1
    for d in deployments:
        assert int(d.obj["status"]["readyReplicas"]) == int(d.obj["spec"]["replicas"])
