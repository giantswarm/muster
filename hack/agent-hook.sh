#!/usr/bin/env bash
# Claude Code hook: runs muster's checks and sends failures back to the agent.
#   after-edit: after an edit to pkg/apis/, runs make lint-api.
#   stop:       before the agent finishes, runs make lint; gives up after 3 blocks.
# Exit code 2 makes Claude Code show stderr to the agent and keep it working.
set -euo pipefail

mode=${1:?usage: agent-hook.sh after-edit|stop}
input=$(cat)
cd "${CLAUDE_PROJECT_DIR:-.}"

case "$mode" in
after-edit)
	file=$(jq -r '.tool_input.file_path // empty' <<<"$input")
	[[ "$file" == */pkg/apis/* ]] || exit 0
	if ! out=$(make --no-print-directory lint-api 2>&1); then
		printf 'make lint-api failed after this edit; fix it before building on it:\n%s\n' "$out" >&2
		exit 2
	fi
	;;
stop)
	session=$(jq -r '.session_id // "unknown"' <<<"$input")
	counter="${TMPDIR:-/tmp}/muster-agent-hook-${session}"
	blocks=$(cat "$counter" 2>/dev/null || echo 0)
	if ((blocks >= 3)); then
		exit 0
	fi
	if ! out=$(make --no-print-directory lint 2>&1); then
		echo $((blocks + 1)) >"$counter"
		printf 'make lint failed; fix it before finishing:\n%s\n' "$(tail -n 60 <<<"$out")" >&2
		exit 2
	fi
	;;
*)
	echo "unknown mode: $mode" >&2
	exit 1
	;;
esac
