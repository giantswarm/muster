#!/usr/bin/env bash
set -euo pipefail

# Renders templates/prometheusrule.yaml, extracts the bare rule groups (promtool
# wants `groups:`, not the PrometheusRule CRD wrapper), and runs the promtool
# unit tests against them. Requires: helm, yq, promtool.
#
# These tests live outside the helm-unittest suite in ../ on purpose: that
# directory is globbed as tests/*_test.yaml by `helm unittest`, and the two
# frameworks share neither a schema nor a runner.

# mikefarah/yq, not the similarly named python jq wrapper -- the `eval` syntax
# below is specific to it, and the wrapper fails with a confusing argparse error.
if ! yq --version 2>&1 | grep -q 'mikefarah/yq'; then
  echo "error: mikefarah/yq is required (see https://github.com/mikefarah/yq)" >&2
  exit 1
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart="$here/../.."
rendered="$here/rendered.rules.yaml"

trap 'rm -f "$rendered"' EXIT

helm template muster "$chart" \
  --set muster.observability.metrics.prometheus.prometheusRule.enabled=true \
  --show-only templates/prometheusrule.yaml \
  | yq eval '.spec' - > "$rendered"

promtool check rules "$rendered"
promtool test rules "$here/prometheusrule.test.yaml"
