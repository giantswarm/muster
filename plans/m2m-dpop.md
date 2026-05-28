# M2M auth on muster — MVP plan

Tracked in: [giantswarm/giantswarm#35672](https://github.com/giantswarm/giantswarm/issues/35672)

This doc supersedes the earlier "prove → productize → harden DPoP" framing. The
revised approach treats autonomous-klaus M2M as the gap to close, with muster
playing the role of a transitional machine IdP until Dex (or a Dex replacement
like Zitadel) ships CIMD support and claude-code's OAuth client can talk to it
directly.

---

## Two flows, not one

The earlier framing conflated two distinct flows. Untangling them:

| | Human-driven session | Autonomous klaus (SRE agent) |
|---|---|---|
| Trigger | A user opens claude-code | klaus-operator runs an unattended agent |
| Identity | Alice (Dex SSO) | `system:serviceaccount:<ns>:<klaus-sre|klaus-deployer|…>` |
| Today | Forwarded Dex Bearer → muster → MCPs. Each MCP impersonates Alice via `email`. **Works.** | Static Bearer baked into `mcp-config.json` via K8s Secrets. No rotation. **Gap.** |
| MVP work | None (regression-tested only) | Build the machine-IdP path |

**Human SSO is unchanged through every phase below.** Autonomous klaus is the
new work.

---

## Per-MCP autonomy matrix

Verified against `mcp-kubernetes` (`internal/server/oauth_http.go:515`,
`internal/tools/federation.go:107-117`) and `mcp-observability-platform`
(`cmd/serve.go:130-158`, `internal/authz/resolver.go:169-237`). Both validate
via `mcp-oauth.ValidateToken` against a single provider (Dex|Google) and
impersonate by `email`.

| MCP | Today's autonomous access | What it needs |
|---|---|---|
| `workflow_*` on muster itself | Works once muster broker issues machine tokens | muster claim injection |
| kubernetes-mcp | Blocked (no email on K8s SA token; muster JWT wrong issuer) | mcp-oauth multi-issuer + muster claim injection + K8s `User: <role>@machine.giantswarm.io` RBAC |
| mcp-flux, mcp-capi | Same as kubernetes-mcp (presumed; uses same mcp-oauth pattern, not verified) | Same |
| mcp-observability-platform | Blocked (Grafana resolves orgs by email + groups) | mcp-oauth multi-issuer + muster claim injection + observability-operator pre-creates Grafana users for each machine principal |
| mcp-runbooks, mcp-search | Unknown auth shape (repos not inspected) | Confirm at kickoff |

Track 1 (mcp-oauth multi-issuer) is the gating dependency for everything beyond
muster's own tools.

---

## Pre-MVP technical premises (verified)

- mcp-oauth `ValidateToken` was single-issuer (single configured provider).
  **Now landed** as multi-issuer in
  [giantswarm/mcp-oauth#409](https://github.com/giantswarm/mcp-oauth/pull/409):
  `WithTrustedIssuers` accepts peer JWTs at `/mcp` as Bearers in addition to
  the existing token-exchange subject path, with RFC 9068 §4 `typ: at+jwt`
  enforcement.
- mcp-oauth's `ExchangeSubjectToken` does **not** yet inject `email`/`groups`
  claims; the access-token issuer already supports them
  (`server/access_token.go:206-227`, `AccessTokenClaims.Extra` at line 86-90),
  so the extension is small (~30 LOC). Tracked separately.
- K8s RBAC binds to arbitrary impersonated users. `ClusterRoleBinding` on
  `User: klaus-sre@machine.giantswarm.io` works without that user existing in
  any IdP — K8s only checks the username from the `Impersonate-User` header.
- Grafana does **not** auto-create users from unknown IdPs. observability MCP
  autonomy therefore requires observability-operator to pre-provision Grafana
  users for each machine principal listed in shared-configs.

---

## MVP scope — single end-to-end slice

**Goal**: prove `klaus-sre SA → muster /oauth/token → muster JWT (with email +
groups) → kubernetes-mcp /mcp → impersonates klaus-sre@machine.giantswarm.io →
list pods succeeds` on the glean management cluster.

**Code changes (all small):**

1. **mcp-oauth: multi-issuer `ValidateToken`** — landed in PR #409.
2. **mcp-oauth: extra claims on `ExchangeSubjectToken`** — extend the call so
   muster can pass `email`/`groups` (~30 LOC). Next mcp-oauth PR.
3. **muster: `machinePrincipals` config + claim injection** — in the existing
   open PR (branch `feat/trusted-issuer-allowed-claims`, audit and update).
   New config:
   ```yaml
   oauth:
     server:
       machinePrincipals:
         "system:serviceaccount:muster-m2m-test:klaus-sre":
           email: "klaus-sre@machine.giantswarm.io"
           groups: ["klaus-sre"]
   ```
   On exchange, look up the subject's `sub`; pass the resulting claims into
   the extended `ExchangeSubjectToken`.
4. **kubernetes-mcp**: add muster to `WithTrustedIssuers` (config change once
   it pins the new mcp-oauth release).

**glean fixtures:**

- Namespace `muster-m2m-test`, ServiceAccount `klaus-sre`.
- `ClusterRoleBinding` `klaus-sre-reader` → ClusterRole `view`, User
  `klaus-sre@machine.giantswarm.io`.
- shared-configs override on glean's muster app: `machinePrincipals` block
  above, plus `trustedIssuers` for glean's K8s SA issuer (already in place
  from earlier proofs).

**Smoke test (`/tmp/m2m-mvp/`, throwaway ~80-line Go):**

```bash
TOKEN=$(kubectl --context glean -n muster-m2m-test create token klaus-sre --audience muster)
ACCESS=$(curl -sX POST https://muster.glean.azuretest.gigantic.io/oauth/token \
  -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange \
  -d "subject_token=$TOKEN" \
  -d subject_token_type=urn:ietf:params:oauth:token-type:jwt \
  -d "resource=<kubernetes-mcp resource id>" | jq -r .access_token)
# decode → verify email/groups claims present
# call kubernetes-mcp /mcp with Bearer $ACCESS → tools/list + kubectl_list_pods
```

**Exit criteria:**

- [ ] Exchange returns muster JWT with `email: klaus-sre@machine.giantswarm.io`
      and `groups: ["klaus-sre"]`.
- [ ] kubernetes-mcp validates the muster JWT (multi-issuer working) and
      impersonates the synthetic user.
- [ ] glean RBAC permits listing pods via the impersonation.
- [ ] Human SSO regression test passes (Dex Bearer still works at kubernetes-mcp).
- [ ] A SA token whose `sub` is **not** in `machinePrincipals` produces a JWT
      without `email` → kubernetes-mcp rejects it with a clear error.

---

## Post-MVP tracks (parallelisable)

Once the MVP smoke test passes on glean:

1. **Klaus in cluster, productized.** Add projected SA volume to the klaus pod
   template (`audience: muster`), document the dev-loop laptop flow.
2. **Per-installation rollout.** Shared-configs `machinePrincipals` entries
   per customer; ClusterRoles for each predefined role
   (`klaus-sre`, `klaus-deployer`, …).
3. **Observability MCP autonomy.** observability-operator pre-provisions
   Grafana users for each machine principal listed in shared-configs;
   `GrafanaOrganization.spec.rbac.viewers` includes those groups.
4. **Other Dex-only MCPs** (mcp-flux, mcp-capi, mcp-runbooks, mcp-search):
   pin new mcp-oauth release, add muster to their `WithTrustedIssuers`.
5. **DPoP at muster `/mcp` boundary.** Mount `DPoPMiddleware`, wire nonce
   provider, land the upstream `cnf.jkt` binding-selective fix, extend
   broker/clients to mint proofs. Concurrent with track 6 — they're
   independent.
6. **OBO at muster aggregator → downstream MCPs.** Per-MCP audience exchange
   for clean sender-constraint when combined with DPoP.
7. **Per-session SAs.** klaus-operator mints fresh SAs per agent task; same
   `allowedClaims.sub` glob covers them as predefined roles.
8. **Broker split.** Extract broker handler out of `muster serve` into its
   own binary, no protocol change.

---

## Long-term: muster as machine IdP is transitional

Muster as the M2M token issuer is the **shortest path to working autonomy**,
but not the right home long-term. The OAuth AS should be a real IdP — Dex
(with extensions) or Zitadel. Migration becomes unblocked once one of them
ships CIMD support that claude-code's MCP OAuth client requires. Until then,
the MVP design above lets us deliver SRE-agent value now without locking in
the wrong architecture: we use the same `trustedIssuers` mechanism on
downstream MCPs that the future IdP would also need; the muster broker is
the only piece that gets replaced.

---

## Out of scope (deferred)

- `KubernetesSATrusts` → `AllowedClaims` migration (already complete in
  v0.2.162 via [mcp-oauth#398](https://github.com/giantswarm/mcp-oauth/pull/398)).
- A new `MachinePrincipal` CRD (premature — single consumer until
  observability work begins; promote later if a second consumer appears).
- Splitting the broker into its own binary (refactor, not protocol).
- Cross-MC Dex federation (per-MC dex stays).
