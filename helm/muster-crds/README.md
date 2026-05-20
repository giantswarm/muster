# muster-crds

CustomResourceDefinitions for [muster](https://github.com/giantswarm/muster).
Shipped as a separate chart from the muster control-plane chart so the CRD
lifecycle (install / upgrade / delete) can be managed independently.

## Contents

- `mcpservers.muster.giantswarm.io` (Namespaced)
- `workflows.muster.giantswarm.io` (Namespaced)

The CRDs live under `crds/` and are applied by Helm at install time. No
templated resources are rendered.

## Why a separate chart

Helm 3 only special-cases the top-level chart's `crds/` directory. Sub-chart
`crds/` directories are silently ignored. Bundling CRDs through the main
`muster` chart's templates (the prior `templates/crds.yaml` glob) leaked the
CRD lifecycle into every `helm upgrade` of the control plane and made the
chart unsuitable as a sub-chart dependency (e.g. inside `agentic-platform`).

Splitting CRDs out gives operators:

- explicit ordering — install CRDs once, then install the control plane
- safe upgrades — CRD schema bumps are an explicit App / `helm upgrade`
  step, not a side effect of a control-plane release
- safe uninstall — `helm uninstall muster` no longer cascade-deletes CRDs
  and every MCPServer / Workflow CR

## Install

Giant Swarm App platform:

```yaml
apiVersion: application.giantswarm.io/v1alpha1
kind: App
metadata:
  name: muster-crds
  namespace: <org-namespace>
spec:
  catalog: giantswarm
  name: muster-crds
  namespace: muster
  version: <chart-version>
```

Raw Helm:

```bash
helm install muster-crds \
  oci://gsoci.azurecr.io/charts/giantswarm/muster-crds \
  --version <chart-version> \
  --namespace muster --create-namespace
```

Always install / upgrade `muster-crds` BEFORE `muster` (and before
`agentic-platform`, which depends on `muster`). See [`UPGRADE.md`](./UPGRADE.md)
for the schema-change procedure.

## Upgrades

`helm upgrade` does not re-apply CRDs from `crds/`. Bumping a CRD schema is an
explicit step — see [`UPGRADE.md`](./UPGRADE.md).
