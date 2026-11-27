#!/usr/bin/env bash
set -euo pipefail
umask 077

: "${MUSTER_HELM_TEST_KUBECONFIG:?set a kubeconfig for a disposable test namespace}"
: "${MUSTER_HELM_TEST_IMAGE_TAG:?set an image tag containing requestState sealing}"

test_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart_directory="$test_directory/../.."
test_namespace="muster-key-$(date +%s)-$$"
scratch_directory="$(mktemp -d "${TMPDIR:-$HOME/.cache/tmp}/muster-key.XXXXXX")"
kubernetes_command=(kubectl --kubeconfig "$MUSTER_HELM_TEST_KUBECONFIG" --namespace "$test_namespace")
helm_command=(helm --kubeconfig "$MUSTER_HELM_TEST_KUBECONFIG" --namespace "$test_namespace")
forward_pid=""

cleanup() {
  if [[ -n "$forward_pid" ]]; then
    kill "$forward_pid" 2>/dev/null || true
    wait "$forward_pid" 2>/dev/null || true
  fi
  "${helm_command[@]}" uninstall "$test_namespace" --ignore-not-found > /dev/null 2>&1 || true
  "${kubernetes_command[@]}" delete namespace "$test_namespace" --ignore-not-found --wait=false > /dev/null 2>&1 || true
  rm -rf "$scratch_directory"
}
trap cleanup EXIT

"${kubernetes_command[@]}" create namespace "$test_namespace" > /dev/null

install() {
  "${helm_command[@]}" install "$test_namespace" "$chart_directory" \
    --skip-crds --set replicaCount=2 --set "image.tag=$MUSTER_HELM_TEST_IMAGE_TAG" \
    --wait --timeout 3m > "$scratch_directory/install.log" 2>&1
}

read_key() {
  "${kubernetes_command[@]}" get secret "$test_namespace-oauth" -o json > "$scratch_directory/secret.json"
  jq -er '.data["oauth-encryption-key"]' "$scratch_directory/secret.json" > "$1"
}

check_checksum() {
  local expected_checksum actual_checksum
  expected_checksum="$(jq -jr '"oauth-encryption-key: \"" + .data["oauth-encryption-key"] + "\""' "$scratch_directory/secret.json" | sha256sum | cut -d ' ' -f 1)"
  actual_checksum="$("${kubernetes_command[@]}" get deployment "$test_namespace" -o 'jsonpath={.spec.template.metadata.annotations.checksum\/oauth-secret}')"
  [[ "$actual_checksum" == "$expected_checksum" ]]
}

install
read_key "$scratch_directory/key-before"
base64 --decode < "$scratch_directory/key-before" | base64 --decode > "$scratch_directory/key-bytes"
[[ "$(wc -c < "$scratch_directory/key-bytes")" -eq 32 ]]
check_checksum
echo "PASS: two replicas install with one generated 32-byte key and a matching checksum"

"${kubernetes_command[@]}" get pods -l "app.kubernetes.io/instance=$test_namespace" -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}' > "$scratch_directory/pods"
[[ "$(wc -l < "$scratch_directory/pods")" -eq 2 ]]
while IFS= read -r pod_name; do
  "${kubernetes_command[@]}" port-forward "pod/$pod_name" :8090 > "$scratch_directory/forward.log" 2>&1 &
  forward_pid=$!
  local_port=""
  for _ in {1..50}; do
    local_port="$(sed -nE 's/^Forwarding from 127\.0\.0\.1:([0-9]+).*/\1/p' "$scratch_directory/forward.log")"
    [[ -z "$local_port" ]] || break
    sleep 0.1
  done
  [[ -n "$local_port" ]]
  curl_command=(curl --silent --show-error --fail --max-time 10 \
    --header 'Content-Type: application/json' --header 'Accept: application/json, text/event-stream')
  "${curl_command[@]}" --dump-header "$scratch_directory/headers" \
    --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"helm-key-test","version":"1"}}}' \
    "http://127.0.0.1:$local_port/mcp" > "$scratch_directory/initialize"
  session_id="$(awk 'tolower($1) == "mcp-session-id:" {gsub("\r", "", $2); print $2}' "$scratch_directory/headers")"
  if [[ -n "$session_id" ]]; then
    curl_command+=(--header "Mcp-Session-Id: $session_id")
  fi
  curl_command+=(--header 'MCP-Protocol-Version: 2025-11-25')
  "${curl_command[@]}" --data '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    "http://127.0.0.1:$local_port/mcp" > /dev/null
  "${curl_command[@]}" --data '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
    "http://127.0.0.1:$local_port/mcp" > "$scratch_directory/tools"
  if grep -q '^data:' "$scratch_directory/tools"; then
    sed -n 's/^data: //p' "$scratch_directory/tools" > "$scratch_directory/tools.json"
  else
    cp "$scratch_directory/tools" "$scratch_directory/tools.json"
  fi
  jq -e '.result.tools | type == "array"' "$scratch_directory/tools.json" > /dev/null
  kill "$forward_pid"
  wait "$forward_pid" 2>/dev/null || true
  forward_pid=""
done < "$scratch_directory/pods"
echo "PASS: both replicas answer MCP initialize and tools/list"

"${helm_command[@]}" upgrade "$test_namespace" "$chart_directory" --reuse-values --wait --timeout 3m > "$scratch_directory/upgrade.log" 2>&1
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
check_checksum
echo "PASS: upgrade preserves the key and checksum"

"${helm_command[@]}" upgrade "$test_namespace" "$chart_directory" --reuse-values --set replicaCount=1 --wait --timeout 3m > "$scratch_directory/scale-down.log" 2>&1
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
check_checksum
echo "PASS: scaling down keeps the same mounted key"

"${helm_command[@]}" uninstall "$test_namespace" > "$scratch_directory/uninstall.log" 2>&1
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
install
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
check_checksum
echo "PASS: uninstall and reinstall preserve the key"
