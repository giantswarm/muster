# muster-crds

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

CRD-only Helm chart for muster - ships the MCPServer and Workflow CustomResourceDefinitions

**Homepage:** <https://github.com/giantswarm/muster>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Giant Swarm | <team-bumblebee@giantswarm.io> |  |

## Source Code

* <https://github.com/giantswarm/muster>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| crds.annotations."helm.sh/resource-policy" | string | `"keep"` |  |

## When to use this chart

Install `muster-crds` only if you upgrade muster with plain Helm and you want
Helm to upgrade the CRDs as well.

The `muster` application chart already ships the same CRDs in its Helm 3
`crds/` directory, so `helm install muster` on a clean cluster creates them.
Helm never touches that directory again on `helm upgrade`, so a later CRD
schema change never reaches the cluster. This chart renders the same CRDs from
`files/crds/` as ordinary templates, so `helm upgrade muster-crds` applies CRD
changes like any other resource.

| You install muster with | Use this chart | Reason |
|---|---|---|
| a single `helm install`, no upgrades | No | The app chart's `crds/` directory is enough |
| plain `helm upgrade` | Yes | Helm never upgrades CRDs in `crds/` |
| a Flux `HelmRelease` | No | Set `install.crds` and `upgrade.crds` to `CreateReplace` on the muster HelmRelease |
| an umbrella chart | Yes | Order the CRD chart first with a chart dependency or a sync wave |

Install order matters. Reconcile `muster-crds` before `muster`:

```bash
helm upgrade --install muster-crds giantswarm-catalog/muster-crds --version <version>
helm upgrade --install muster giantswarm-catalog/muster --version <version>
```

The app chart's bundled CRDs are then a no-op, because Helm skips CRDs that
already exist.

Both copies of the CRDs come from the same Go types. `make generate-crds`
writes `helm/muster-crds/files/crds/` and syncs `helm/muster/crds/`, and
`make verify-crds` fails the build if either copy is stale.

`UPGRADE.md` covers the install order in more detail, the CRD schema ratchet,
and what `helm uninstall` leaves behind.
