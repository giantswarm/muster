# muster-crds

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

CRD-only Helm chart for muster - ships the MCPServer and Workflow CustomResourceDefinitions

**Homepage:** <https://github.com/giantswarm/muster>

## About

This chart contains only the muster CustomResourceDefinitions:

- `mcpservers.muster.giantswarm.io` (kind: `MCPServer`)
- `workflows.muster.giantswarm.io` (kind: `Workflow`)

It exists so the CRD lifecycle can be decoupled from the muster application
chart and owned independently by a downstream `agentic-platform-crds` umbrella.
The muster application chart no longer renders these CRDs.

The CRDs are loaded from `files/crds/*.yaml` by `templates/crds.yaml` (regular
chart templates, not the Helm 3 `crds/` special directory), so they are
upgradable on `helm upgrade` and the loader can merge `crds.annotations` into
each CRD's `metadata.annotations`.

## Ordering

Install or upgrade **muster-crds before muster**. The muster application chart
expects the CRDs to already exist and ships with `muster.crds.install: false`.

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
