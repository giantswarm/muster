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
key_secret_name="$test_namespace-encryption-key"
key_entry=oauth-encryption-key

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
trap 'printf "FAIL: encryption-key check at line %s\n" "$LINENO" >&2' ERR

"${kubernetes_command[@]}" create namespace "$test_namespace" > /dev/null
"${kubernetes_command[@]}" create secret generic "$test_namespace-credentials" \
  --from-literal=dex-client-secret=fixture > /dev/null
"${kubernetes_command[@]}" get secret "$test_namespace-credentials" -o json \
  | jq -Sc '.data' > "$scratch_directory/credentials-before"

install() {
  "${helm_command[@]}" install "$test_namespace" "$chart_directory" \
    --skip-crds --set replicaCount=2 --set "image.tag=$MUSTER_HELM_TEST_IMAGE_TAG" \
    --set "muster.oauth.server.existingSecret=$test_namespace-credentials" \
    --wait --timeout 3m > "$scratch_directory/install.log" 2>&1
}

read_key() {
  "${kubernetes_command[@]}" get secret "$key_secret_name" -o json > "$scratch_directory/secret.json"
  jq -er --arg key "$key_entry" '.data[$key]' "$scratch_directory/secret.json" > "$1"
}

check_checksum() {
  local expected_checksum actual_checksum actual_source actual_entry
  expected_checksum="$(jq -jr --arg key "$key_entry" '.data[$key]' "$scratch_directory/secret.json" | sha256sum | cut -d ' ' -f 1)"
  actual_checksum="$("${kubernetes_command[@]}" get deployment "$test_namespace" -o 'jsonpath={.spec.template.metadata.annotations.checksum\/encryption-key}')"
  [[ "$actual_checksum" == "$expected_checksum" ]]
  actual_source="$("${kubernetes_command[@]}" get deployment "$test_namespace" -o 'jsonpath={.spec.template.spec.volumes[?(@.name=="encryption-key")].secret.secretName}')"
  actual_entry="$("${kubernetes_command[@]}" get deployment "$test_namespace" -o 'jsonpath={.spec.template.spec.volumes[?(@.name=="encryption-key")].secret.items[0].key}')"
  [[ "$actual_source" == "$key_secret_name" && "$actual_entry" == "$key_entry" ]]
}

install
read_key "$scratch_directory/key-before"
base64 --decode < "$scratch_directory/key-before" | base64 --decode > "$scratch_directory/key-bytes"
[[ "$(wc -c < "$scratch_directory/key-bytes")" -eq 32 ]]
check_checksum
echo "PASS: two replicas install with one generated 32-byte key and a matching checksum"

check_mcp() {
  local pod_name local_port session_id
  local -a curl_command
  "${kubernetes_command[@]}" get pods -l "app.kubernetes.io/instance=$test_namespace" -o json \
    | jq -r '.items[] | select(.metadata.deletionTimestamp == null) | .metadata.name' > "$scratch_directory/pods"
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
}
check_mcp
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

"${helm_command[@]}" upgrade "$test_namespace" "$chart_directory" --reuse-values --set muster.oauth.server.encryptionKey=false --wait --timeout 3m > "$scratch_directory/disable.log" 2>&1
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
"${helm_command[@]}" upgrade "$test_namespace" "$chart_directory" --reuse-values --set muster.oauth.server.encryptionKey=true --wait --timeout 3m > "$scratch_directory/enable.log" 2>&1
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
check_checksum
echo "PASS: disabling and re-enabling encryption preserves the key"

"${helm_command[@]}" uninstall "$test_namespace" > "$scratch_directory/uninstall.log" 2>&1
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
install
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
check_checksum
echo "PASS: uninstall and reinstall preserve the key"

base64 --decode < "$scratch_directory/key-before" > "$scratch_directory/key-value"
"${kubernetes_command[@]}" create secret generic "$test_namespace-supplied-key" \
  --from-file="aes-key=$scratch_directory/key-value" > /dev/null
"${helm_command[@]}" upgrade "$test_namespace" "$chart_directory" --reuse-values \
  --set "muster.oauth.server.encryptionKeySecret.name=$test_namespace-supplied-key" \
  --set muster.oauth.server.encryptionKeySecret.key=aes-key \
  --wait --timeout 3m > "$scratch_directory/explicit-key.log" 2>&1
key_secret_name="$test_namespace-supplied-key"
key_entry=aes-key
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
check_checksum
check_mcp
echo "PASS: an explicit key Secret with a custom entry serves both replicas"

"${helm_command[@]}" uninstall "$test_namespace" > "$scratch_directory/uninstall.log" 2>&1
"${kubernetes_command[@]}" delete secret "$test_namespace-encryption-key" > /dev/null
"${kubernetes_command[@]}" create secret generic "$test_namespace-legacy" \
  --from-file="oauth-encryption-key=$scratch_directory/key-value" > /dev/null
"${helm_command[@]}" install "$test_namespace" "$chart_directory" \
  --skip-crds --set replicaCount=2 --set "image.tag=$MUSTER_HELM_TEST_IMAGE_TAG" \
  --set "muster.oauth.server.existingSecret=$test_namespace-legacy" \
  --wait --timeout 3m > "$scratch_directory/external-legacy.log" 2>&1
key_secret_name="$test_namespace-legacy"
key_entry=oauth-encryption-key
read_key "$scratch_directory/key-after"
cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
check_checksum
[[ "$("${kubernetes_command[@]}" get secret "$test_namespace-encryption-key" --ignore-not-found -o name)" == "" ]]
echo "PASS: an existing external credentials key is reused without copying it"

if [[ -n "${MUSTER_HELM_TEST_PREVIOUS_CHART:-}" ]]; then
  "${helm_command[@]}" uninstall "$test_namespace" > "$scratch_directory/uninstall.log" 2>&1
  "${helm_command[@]}" install "$test_namespace" "$MUSTER_HELM_TEST_PREVIOUS_CHART" \
    --skip-crds --set replicaCount=2 --set "image.tag=$MUSTER_HELM_TEST_IMAGE_TAG" \
    --set muster.oauth.server.encryptionKey=true \
    --set-file "muster.oauth.server.encryptionKeyValue=$scratch_directory/key-value" \
    --wait --timeout 3m > "$scratch_directory/previous.log" 2>&1
  "${helm_command[@]}" upgrade "$test_namespace" "$chart_directory" --reset-values \
    --set replicaCount=2 --set "image.tag=$MUSTER_HELM_TEST_IMAGE_TAG" \
    --wait --timeout 3m > "$scratch_directory/migrate.log" 2>&1
  key_secret_name="$test_namespace-encryption-key"
  read_key "$scratch_directory/key-after"
  cmp --silent "$scratch_directory/key-before" "$scratch_directory/key-after"
  check_checksum
  check_mcp
  echo "PASS: upgrade migrates the owned OAuth key unchanged into a dedicated Secret"
fi

"${kubernetes_command[@]}" get secret "$test_namespace-credentials" -o json \
  | jq -Sc '.data' > "$scratch_directory/credentials-after"
cmp --silent "$scratch_directory/credentials-before" "$scratch_directory/credentials-after"
echo "PASS: externally managed OAuth credentials remain unchanged"
