# scripts/

Operational scripts for muster.

| Script | Purpose | Owner |
|---|---|---|
| `dev-restart.sh` | Local dev convenience | dev |
| `setup-systemd.sh` | Systemd unit installer | dev |
| `stress-test.sh` | Local stress test | dev |
| `provision-teleport-bot.sh` | Provision Teleport bot/role/token for the muster aggregator (TB-3) | SRE |

---

## `provision-teleport-bot.sh`

Idempotent SRE-run script that creates the Teleport-side state required for
the muster aggregator on Gazelle to reach Dex and `mcp-kubernetes` on private
customer management clusters via Teleport Application Access. See
[`PLAN.md`](../../PLAN.md) §6 TB-3 and §9 "Bot scope and Teleport role design".

The script applies three resources via `tctl create -f -` (which is
upsert-on-stable-name, so re-running is safe for cluster rebuilds, DR, and
new-environment provisioning):

1. **`bot/muster-aggregator`** — machine identity for muster on Gazelle.
2. **`role/muster-aggregator-role`** — label-selector ACL granting access to
   apps carrying `purpose=muster-aggregator` in an explicit cluster
   allowlist (the trust-boundary fix from PLAN §9). Adding a new MC means
   extending `CLUSTER_ALLOWLIST` and re-running.
3. **`token/muster-aggregator`** — kubernetes-method join token bound to
   Gazelle's in-cluster ServiceAccount issuer. The token name is the bot
   contract: it must match `onboarding.token` in tbot's config (TB-4).

### Prerequisites

- `tctl` on `PATH` (Teleport client tools).
- An admin context for the target Teleport cluster
  (e.g. `tsh login --proxy=teleport.giantswarm.io --auth=...`).

### Usage

```bash
# Production run (interactive confirmation; prod is the default proxy).
./scripts/provision-teleport-bot.sh

# Non-interactive (CI / scripted invocation).
./scripts/provision-teleport-bot.sh --yes

# Render the manifest without applying — useful for review / diff.
./scripts/provision-teleport-bot.sh --dry-run
```

### Configuration

All knobs are env vars. Defaults match the production bot contract; override
only when targeting a test environment or onboarding a new MC.

| Variable | Default | Meaning |
|---|---|---|
| `TELEPORT_PROXY` | `teleport.giantswarm.io:443` | Used in confirmation prompt only; `tctl` itself reads its proxy from your local profile. |
| `BOT_NAME` | `muster-aggregator` | Bot resource name. |
| `ROLE_NAME` | `muster-aggregator-role` | Role resource name. |
| `TOKEN_NAME` | `muster-aggregator` | Join-token name. **Must match** tbot's `onboarding.token` (TB-4). |
| `CLUSTER_ALLOWLIST` | `glean` | Comma-separated MCs the bot may reach (e.g. `glean,finch`). Trust-boundary control. |
| `SA_NAMESPACE` | `muster-system` | Namespace of muster's SA on Gazelle. |
| `SA_NAME` | `muster` | muster's ServiceAccount name on Gazelle (helm chart default). |
| `TEMPLATE_PATH` | `<script-dir>/provision-teleport-bot.yaml.tmpl` | Template file to render. |

### Onboarding a new remote MC

When TB-1/TB-2 register Teleport apps for a new MC (e.g. `finch`), you must
extend the bot's role allowlist *before* tbot on Gazelle requests certs for
those apps — otherwise tbot's cert request is denied:

```bash
CLUSTER_ALLOWLIST="glean,finch" ./scripts/provision-teleport-bot.sh --yes
```

The script upserts the role; bot and token are unchanged on re-run.

### Operational notes

- Designed for SRE; **not wired into CI**. See TB-3a in `PLAN.md` for the
  follow-up that moves provisioning into `teleport-operator`.
- Treats `tctl create -f` non-zero as failure, except "already exists" which
  is treated as success-equivalent (rare race; `-f` should upsert silently).
- The role uses **label selectors**, not enumerated app names — so adding a
  new app within an already-allowlisted MC requires no role change. Adding
  a new MC requires extending `CLUSTER_ALLOWLIST` (above).
- API versions in the template (`role v7`, `token v2`) target Teleport
  v17.5.4 (the version in `teleport-kube-agent-app/helm/teleport-kube-agent`).
  Update if the cluster is upgraded to a release that changes resource shape.
