#!/usr/bin/env bash
set -euo pipefail

# Install muster-crds chart from source before muster is deployed.
# CRDs are managed in a separate lifecycle chart — this mirrors production.
helm package helm/muster-crds/ -d /tmp/muster-crds-pkg

helm install muster-crds /tmp/muster-crds-pkg/muster-crds-*.tgz \
  --namespace "${ATS_DEPLOY_NAMESPACE:-muster}" \
  --create-namespace \
  --wait \
  --timeout 120s
