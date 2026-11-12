#!/usr/bin/env bash
set -euo pipefail

kal=hack/tools/bin/golangci-lint-kube-api-linter
if out=$("$kal" run --config .golangci-kal.yml ./pkg/apis/... 2>&1); then
	exit 0
fi
echo "$out"
if grep -qE 'nobools: field [A-Za-z]+Status\.' <<<"$out"; then
	echo "A true/false observation in a status is a metav1.Condition: add a condition type to status.conditions instead of a bool or an enum field." >&2
fi
exit 1
