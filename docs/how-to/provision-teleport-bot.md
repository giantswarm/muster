# Provision the Teleport Bot for muster

> **Audience: SRE only.** This guide drives `scripts/provision-teleport-bot.sh`,
> which mutates state on the **Giant Swarm Teleport cluster** at
> `teleport.giantswarm.io`. Teleport is a shared resource — running the script
> with the wrong allowlist or token name affects every Muster aggregator that
> joins via the same bot. Cluster-admins and CR authors do not need to read
> this doc; see [Configure tbot identity](configure-tbot-identity.md) and
> [Access private MCP servers](access-private-mcp-servers.md) instead.

## Overview

`scripts/provision-teleport-bot.sh` is an idempotent SRE-run script that
applies three resources to the Giant Swarm Teleport cluster via
`tctl create -f -`:

1. **`bot/muster-aggregator`** — machine identity for muster on Gazelle.
2. **`role/muster-aggregator-role`** — label-selector ACL with an explicit
   `cluster:` allowlist (the trust-boundary fix from PLAN §9). Adding a new
   MC means extending the allowlist and re-running the script.
3. **`token/muster-aggregator`** — kubernetes-method join token bound to
   Gazelle's in-cluster ServiceAccount issuer. **The token name is the
   bot ↔ tbot contract** — it must match `onboarding.token` in the tbot
   ConfigMap rendered by the muster Helm chart (TB-4).

Why this is a script and not a controller: see PLAN §6 TB-3 and §9 ("Bot scope
and Teleport role design") for the rationale, including the deferred TB-3a
follow-up that moves provisioning into `teleport-operator` once per-MC role
updates feel like toil.

## Prerequisites

- **`tctl`** on `PATH`. Install Teleport client tools:
  `https://goteleport.com/docs/installation/`.
- **Authenticated** to the target Teleport cluster as an admin. For
  production:

  ```bash
  tsh login --proxy=teleport.giantswarm.io --auth=github
  ```

  You need a Teleport role permitting `create`/`update` on `bot`, `role`, and
  `token` resources. In Giant Swarm production this is `editor` (the
  out-of-the-box admin role); custom roles work as long as they grant the
  three verbs above on the three resource kinds. The script does not check
  RBAC up-front — `tctl` will return a clear error if your role is
  insufficient.
- **Bash 4+** (the script uses associative trimming and `set -euo pipefail`).

## Run it

The script lives at `scripts/provision-teleport-bot.sh` and renders a YAML
manifest from `scripts/provision-teleport-bot.yaml.tmpl` before applying.

### 1. Dry-run first

Always start with a dry run to inspect what will be applied:

```bash
./scripts/provision-teleport-bot.sh --dry-run
```

The script prints the rendered manifest (bot + role + token) to stderr and
exits without calling `tctl`. Read the role's `app_labels.cluster:` list
carefully — that is the only knob that controls which MCs the bot can reach.

### 2. Apply it

Two equivalent ways to apply:

```bash
# Interactive — prompts y/N before tctl create.
./scripts/provision-teleport-bot.sh

# Non-interactive (e.g. from a runbook step or smoke test).
./scripts/provision-teleport-bot.sh --yes
```

`tctl create -f` upserts on stable resource names, so re-running the script
with the same inputs is a no-op. The script treats a literal "already exists"
response (rare race with concurrent admins) as success-equivalent.

### 3. Verify

```bash
tctl get bot/muster-aggregator role/muster-aggregator-role token/muster-aggregator
```

All three resources should appear with the expected spec.

## Configuration

All knobs are environment variables. Defaults match the GS-production bot
contract; override only when targeting a non-prod Teleport or onboarding a
new MC.

| Variable | Default | Meaning |
|---|---|---|
| `TELEPORT_PROXY` | `teleport.giantswarm.io:443` | Used in the confirmation prompt only. `tctl` reads its proxy from your `tsh` profile. |
| `BOT_NAME` | `muster-aggregator` | Bot resource name. |
| `ROLE_NAME` | `muster-aggregator-role` | Role resource name. |
| `TOKEN_NAME` | `muster-aggregator` | Join-token name. **Must match** tbot's `onboarding.token` (see [The bot ↔ tbot contract](#the-bot--tbot-contract) below). |
| `CLUSTER_ALLOWLIST` | `glean` | Comma-separated MCs the bot may reach (e.g. `glean,finch`). The trust-boundary control. |
| `SA_NAMESPACE` | `muster-system` | Namespace of muster's ServiceAccount on Gazelle. |
| `SA_NAME` | `muster` | muster's ServiceAccount name on Gazelle (Helm chart default). |
| `TEMPLATE_PATH` | `scripts/provision-teleport-bot.yaml.tmpl` | Template file to render. |

The `CLUSTER_ALLOWLIST` is parsed comma-separated with whitespace tolerated.
An empty allowlist is rejected — the script refuses to provision a role with
no `cluster:` filter, since that would grant access to every MC tagged
`purpose=muster-aggregator`.

## The bot ↔ tbot contract

Two values are wired across two repos and **must stay in sync**:

| Side | Knob | Default |
|---|---|---|
| Teleport (this script) | `token/<TOKEN_NAME>` | `muster-aggregator` |
| muster Helm chart | `helm/muster/templates/tbot-configmap.yaml` → `onboarding.token` | hard-coded to `muster-aggregator` |

If you change `TOKEN_NAME` here, you must also patch the tbot ConfigMap
template in `helm/muster/templates/tbot-configmap.yaml` (currently hard-coded
to match the production bot contract). Do not change this casually — the
contract is also referenced by the readiness gate, the Helm chart helm-test
(TB-4), and runbooks. If you have a real reason to change it (e.g. a parallel
test environment that must not collide with prod), open a small PR that
flips both sides at once.

The bot/role names follow the same pattern: changing `BOT_NAME` or
`ROLE_NAME` requires no muster-side change because muster never references
them — only the join token does.

## Operational scenarios

### Cluster rebuild / DR

The script is safe to re-run as-is:

```bash
./scripts/provision-teleport-bot.sh --yes
```

`tctl create -f` upserts. State lives in the GS Teleport cluster, not on
Gazelle, so a Gazelle blast radius does not corrupt this state. If the
Teleport cluster itself is rebuilt from a backup, re-running the script
reconciles any drift.

### Onboarding a new MC

Per the runbook (PLAN §10 step 1), extend the allowlist and re-run:

```bash
CLUSTER_ALLOWLIST="glean,finch" ./scripts/provision-teleport-bot.sh --yes
```

Only the `role` is updated (the bot and token are upserted unchanged). This
**must happen before** the Helm chart's `transport.teleport.clusters[]` adds
the new entry (see [Configure tbot identity](configure-tbot-identity.md)) —
otherwise tbot's cert request for the new app is denied by the role until
the next reconcile loop.

Worked example for adding `finch` as a second MC, in order:

1. Confirm the Teleport apps (`dex-finch`, `mcp-kubernetes-finch`) are
   already registered in the GitOps repo (PLAN §6 TB-1/TB-2 — out of scope
   for this script).
2. Re-run this script with the extended allowlist:
   ```bash
   CLUSTER_ALLOWLIST="glean,finch" ./scripts/provision-teleport-bot.sh --dry-run
   CLUSTER_ALLOWLIST="glean,finch" ./scripts/provision-teleport-bot.sh --yes
   ```
3. Update muster's Helm values (`transport.teleport.clusters[]`) to add
   `- name: finch` — see [Configure tbot identity](configure-tbot-identity.md).
4. Apply the MCPServer CR — see
   [Access private MCP servers](access-private-mcp-servers.md).

The full six-step onboarding flow is documented in PLAN.md §10. This script
owns step 1 (the role-allowlist update); the rest happens in
`giantswarm-management-clusters` and the muster Helm release.

### Provisioning a non-production Teleport

For a test Teleport (e.g. a personal dev cluster), point the script at it by
overriding both your `tsh` profile **and** `TELEPORT_PROXY`:

```bash
tsh login --proxy=teleport.test.example.com
TELEPORT_PROXY=teleport.test.example.com:443 \
  CLUSTER_ALLOWLIST=test-mc \
  SA_NAMESPACE=muster-test \
  ./scripts/provision-teleport-bot.sh --yes
```

`TELEPORT_PROXY` is used only for the confirmation prompt — `tctl` itself
follows the active `tsh` profile. Setting it correctly is a safety belt: the
prompt text reflects the cluster you are about to mutate.

If the test deployment uses a different muster ServiceAccount, override
`SA_NAMESPACE` and `SA_NAME` so the join token binds to the correct issuer.

## Troubleshooting

### `tctl: command not found`

`tctl` is not on `PATH`. Install Teleport client tools or activate the env
that has them. Confirm with `tctl version`.

### `ERROR: access denied to perform action "create" on "bot"`

Your `tsh` profile is logged in but its role does not grant `create` on the
`bot`/`role`/`token` resource kinds. Re-login with an admin role
(`tsh login --request-roles=editor` if your cluster is configured for
just-in-time access requests) and retry.

### `ERROR: token "muster-aggregator" already exists with a different spec`

Rare — happens if the token was hand-edited via `tctl edit` and the diff
cannot be merged by `-f` upsert. Hard-reset:

```bash
tctl rm token/muster-aggregator
./scripts/provision-teleport-bot.sh --yes
```

The same recipe applies to `role/muster-aggregator-role` if its spec was
hand-edited and a re-render produces a conflict. Avoid `tctl rm bot/...`
unless you intend to invalidate every tbot identity that joined under it
(tbot will recover on its next restart with a re-issued cert chain, but the
window is visible to consumers as transient secret-load failures).

### `CLUSTER_ALLOWLIST resolved to an empty list`

The script refuses to provision a role with no `cluster:` filter (would
grant access to every MC tagged `purpose=muster-aggregator`). Pass at least
one cluster name.

### `stdin is not a TTY and --yes was not passed`

The script refuses non-interactive runs without `--yes` to avoid surprising
applies in CI / piped invocations. Pass `--yes` if you intend the apply.

## Related

- [Configure tbot Identity (cluster-admin)](configure-tbot-identity.md) — the
  Helm-side counterpart that consumes this bot's join token.
- [Access Private MCP Servers (MCPServer author)](access-private-mcp-servers.md)
  — the CR-author guide.
- [PLAN.md §6 TB-3](../../PLAN.md) — design rationale.
- [PLAN.md §10](../../PLAN.md) — full operational runbook for new-MC
  onboarding (this script owns step 1).
- [Teleport Machine ID](https://goteleport.com/docs/enroll-resources/machine-id/getting-started/)
  — upstream reference for tbot, bots, roles, and join tokens.
