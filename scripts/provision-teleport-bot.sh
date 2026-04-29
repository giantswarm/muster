#!/usr/bin/env bash
#
# provision-teleport-bot.sh — TB-3
#
# Idempotent SRE-run script that provisions the Teleport-side state required
# for the muster aggregator on Gazelle to reach Dex / mcp-kubernetes on
# private customer management clusters via Teleport Application Access.
#
# Creates three resources in the Giant Swarm Teleport cluster:
#   - bot         muster-aggregator
#   - role        muster-aggregator-role  (label-selector + cluster allowlist)
#   - token       muster-aggregator       (kubernetes join, bound to Gazelle SA)
#
# `tctl create -f` is upsert-on-stable-name, so this script is safely
# re-runnable for cluster rebuilds, DR, and new-environment provisioning.
# Adding a new remote MC means extending CLUSTER_ALLOWLIST and re-running.
#
# Owner: SRE. Run once per Teleport cluster (prod + each test env).
# Not in CI. See PLAN §6 TB-3 and §9 "Bot scope and Teleport role design".
#
# Usage:
#   ./provision-teleport-bot.sh [--yes] [--dry-run]
#
#   Env-var overrides (all optional):
#     TELEPORT_PROXY      default teleport.giantswarm.io:443
#     BOT_NAME            default muster-aggregator
#     ROLE_NAME           default muster-aggregator-role
#     TOKEN_NAME          default muster-aggregator
#                         (MUST match tbot's onboarding.token in TB-4)
#     CLUSTER_ALLOWLIST   default glean   (comma-separated, no spaces required)
#     SA_NAMESPACE        default muster-system
#     SA_NAME             default muster
#     TEMPLATE_PATH       default <script-dir>/provision-teleport-bot.yaml.tmpl

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ---- Defaults ---------------------------------------------------------------
TELEPORT_PROXY="${TELEPORT_PROXY:-teleport.giantswarm.io:443}"
BOT_NAME="${BOT_NAME:-muster-aggregator}"
ROLE_NAME="${ROLE_NAME:-muster-aggregator-role}"
TOKEN_NAME="${TOKEN_NAME:-muster-aggregator}"
CLUSTER_ALLOWLIST="${CLUSTER_ALLOWLIST:-glean}"
SA_NAMESPACE="${SA_NAMESPACE:-muster-system}"
SA_NAME="${SA_NAME:-muster}"
TEMPLATE_PATH="${TEMPLATE_PATH:-${SCRIPT_DIR}/provision-teleport-bot.yaml.tmpl}"

ASSUME_YES=0
DRY_RUN=0

# ---- Helpers ----------------------------------------------------------------
err()  { printf 'error: %s\n' "$*" >&2; }
info() { printf '%s\n' "$*" >&2; }

usage() {
  sed -n '2,33p' "$0" | sed 's/^# \{0,1\}//'
}

require_tctl() {
  if ! command -v tctl >/dev/null 2>&1; then
    err "'tctl' not found on PATH."
    err "Install Teleport client tools and authenticate against ${TELEPORT_PROXY} (e.g. via 'tsh login --proxy=${TELEPORT_PROXY}') before running this script."
    exit 127
  fi
}

# Render the comma-separated CLUSTER_ALLOWLIST into a YAML inline-list payload,
# i.e. 'glean','finch'. Empty / whitespace tokens are skipped. Single-quoting
# handles cluster names safely and matches the style of the template.
render_cluster_allowlist_yaml() {
  local raw="$1"
  local out=""
  local IFS=','
  # shellcheck disable=SC2086
  for token in $raw; do
    # trim whitespace
    token="${token#"${token%%[![:space:]]*}"}"
    token="${token%"${token##*[![:space:]]}"}"
    [[ -z "$token" ]] && continue
    if [[ -n "$out" ]]; then
      out+=", "
    fi
    out+="'${token}'"
  done
  printf '%s' "$out"
}

render_template() {
  if [[ ! -f "$TEMPLATE_PATH" ]]; then
    err "template not found at ${TEMPLATE_PATH}"
    exit 2
  fi
  local cluster_yaml
  cluster_yaml="$(render_cluster_allowlist_yaml "$CLUSTER_ALLOWLIST")"
  # Guard runs in the parent shell because `exit` inside a command
  # substitution only kills the subshell.
  if [[ -z "$cluster_yaml" ]]; then
    err "CLUSTER_ALLOWLIST resolved to an empty list — refusing to provision a role with no cluster allowlist (would grant access to every MC tagged purpose=muster-aggregator)."
    exit 2
  fi

  # Plain @VAR@ placeholder substitution. None of the substituted values can
  # contain @ or unescaped quotes in normal SRE use; values come from env
  # vars set by an SRE, not untrusted input.
  sed \
    -e "s|@BOT_NAME@|${BOT_NAME}|g" \
    -e "s|@ROLE_NAME@|${ROLE_NAME}|g" \
    -e "s|@TOKEN_NAME@|${TOKEN_NAME}|g" \
    -e "s|@CLUSTER_ALLOWLIST_YAML@|${cluster_yaml}|g" \
    -e "s|@SA_NAMESPACE@|${SA_NAMESPACE}|g" \
    -e "s|@SA_NAME@|${SA_NAME}|g" \
    "$TEMPLATE_PATH"
}

confirm_or_exit() {
  if [[ "$ASSUME_YES" -eq 1 ]]; then
    return 0
  fi
  if [[ ! -t 0 ]]; then
    err "stdin is not a TTY and --yes was not passed; refusing to apply changes non-interactively."
    err "Pass --yes to confirm, or run from an interactive shell."
    exit 3
  fi
  printf 'Apply the above resources to Teleport at %s? [y/N] ' "$TELEPORT_PROXY" >&2
  local reply
  read -r reply
  case "$reply" in
    y|Y|yes|YES) return 0 ;;
    *) info "aborted by user."; exit 4 ;;
  esac
}

# ---- Arg parsing ------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes|-y)      ASSUME_YES=1; shift ;;
    --dry-run|-n)  DRY_RUN=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    --)            shift; break ;;
    *)             err "unknown argument: $1"; usage; exit 64 ;;
  esac
done

# ---- Main -------------------------------------------------------------------
require_tctl

info "Teleport proxy:       ${TELEPORT_PROXY}"
info "Bot name:             ${BOT_NAME}"
info "Role name:            ${ROLE_NAME}"
info "Token name:           ${TOKEN_NAME}    (must match tbot onboarding.token)"
info "Cluster allowlist:    ${CLUSTER_ALLOWLIST}"
info "Bound SA:             ${SA_NAMESPACE}:${SA_NAME}"
info "Template:             ${TEMPLATE_PATH}"
info ""
info "Rendered manifest:"
info "----------------------------------------"
RENDERED="$(render_template)"
printf '%s\n' "$RENDERED" >&2
info "----------------------------------------"

if [[ "$DRY_RUN" -eq 1 ]]; then
  info "--dry-run set; skipping tctl create."
  exit 0
fi

confirm_or_exit

info "Applying via tctl create -f - (upsert)..."

# `tctl create -f -` is the documented upsert path. Capture stderr so we can
# still distinguish a real failure from a benign "already exists with
# identical spec" race in the rare case the server returns one despite -f.
TCTL_OUT=""
TCTL_RC=0
TCTL_OUT="$(printf '%s\n' "$RENDERED" | tctl create -f - 2>&1)" || TCTL_RC=$?

# Always print tctl output for SRE visibility.
if [[ -n "$TCTL_OUT" ]]; then
  printf '%s\n' "$TCTL_OUT" >&2
fi

if [[ "$TCTL_RC" -eq 0 ]]; then
  info "OK: bot/${BOT_NAME}, role/${ROLE_NAME}, token/${TOKEN_NAME} reconciled on ${TELEPORT_PROXY}."
  exit 0
fi

# Belt-and-braces: treat "already exists" as success. With `-f` this should
# not happen, but Teleport has historically returned this on some resource
# kinds when concurrent admins race. Match conservatively.
if printf '%s' "$TCTL_OUT" | grep -qiE 'already exists'; then
  info "tctl reported 'already exists' — treating as success-equivalent (idempotent re-run)."
  exit 0
fi

err "tctl create failed (exit ${TCTL_RC}). See output above."
exit "$TCTL_RC"
