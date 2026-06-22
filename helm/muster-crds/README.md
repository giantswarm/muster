# muster-crds

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

CRD-only Helm chart for muster - ships the MCPServer and Workflow CustomResourceDefinitions

**Homepage:** <https://github.com/giantswarm/muster>

## About

This chart contains only the muster CustomResourceDefinitions:

- `mcpservers.muster.giantswarm.io` (kind: `MCPServer`)
- `workflows.muster.giantswarm.io` (kind: `Workflow`)

It is a standalone option for managing the CRD lifecycle independently of the
muster application chart — useful for a downstream `agentic-platform-crds`
umbrella, or for plain-Helm users who want CRD updates to apply on
`helm upgrade`.

The CRDs are loaded from `files/crds/*.yaml` by `templates/crds.yaml` (regular
chart templates, not the Helm 3 `crds/` special directory), so they are
upgradable on `helm upgrade` and the loader can merge `crds.annotations` into
each CRD's `metadata.annotations`.

> The muster **application** chart also ships these CRDs, in its Helm 3 `crds/`
> directory (`helm/muster/crds/`). Helm installs them automatically on
> `helm install muster` but does not update them on `helm upgrade`; Flux users
> get atomic CRD upgrades via `install.crds`/`upgrade.crds: CreateReplace` on the
> HelmRelease. The two are compatible: if you install `muster-crds` first, the
> app chart's bundled CRDs already exist and are a no-op (Helm skips existing
> CRDs in `crds/`, so there is no ownership conflict). Use `muster-crds` when you
> want a separate, `helm upgrade`-friendly CRD lifecycle; otherwise the app
> chart alone is sufficient.

## Ordering

When you use this chart, install or upgrade **muster-crds before muster** so the
CRDs are present before the operator starts. (The app chart's own bundled CRDs
become a no-op in this case.)

In an umbrella chart, express this with a dependency ordering / sync wave so
that `muster-crds` reconciles ahead of `muster`.

```bash
# 1. CRDs first
helm upgrade --install muster-crds giantswarm/muster-crds --namespace muster --create-namespace

# 2. then the application
helm upgrade --install muster giantswarm/muster --namespace muster
```

## CRD retention

Each CRD carries `helm.sh/resource-policy: keep` (via `crds.annotations`), so a
`helm uninstall muster-crds` leaves the CRDs — and any `MCPServer` / `Workflow`
custom resources that depend on them — in place. Deleting the CRDs is a
deliberate, manual operation (`kubectl delete crd ...`). See `UPGRADE.md` for
the upgrade and removal semantics.

## Source Code

* <https://github.com/giantswarm/muster>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| crds.annotations | object | `{"helm.sh/resource-policy":"keep"}` | Annotations merged into each CRD's `metadata.annotations`. Default `helm.sh/resource-policy: keep` preserves CRDs on `helm uninstall`. |
