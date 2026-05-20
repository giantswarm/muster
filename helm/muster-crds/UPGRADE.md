# Upgrading muster-crds

Helm 3 reads the chart's top-level `crds/` directory only on **install** —
`helm upgrade` does NOT reconcile CRDs from `crds/`. Bumping a CRD schema is
therefore an explicit step, not a side effect of upgrading the chart.

## Ordering

`muster-crds` MUST be upgraded **before** `muster` (and before
`agentic-platform`, which depends on `muster`) on any schema-affecting
release.

When using the Giant Swarm App platform, encode this with `spec.dependsOn`
on the consuming App CR:

```yaml
apiVersion: application.giantswarm.io/v1alpha1
kind: App
metadata:
  name: muster
spec:
  dependsOn:
    - name: muster-crds
      namespace: muster
```

## Schema changes

CRD changes are categorized; the rollout procedure depends on the category.

### Additive — new optional fields, new printer columns

No special handling. The new chart release ships the updated CRD manifests;
operators apply them with `kubectl replace -f` or by reinstalling the
`muster-crds` App at the new version.

### Field removals / required-field changes / served-version flips

Follow the Kubernetes deprecation policy (
[k/community/contributors/devel/sig-architecture/api_changes.md](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api_changes.md#alpha-beta-and-stable-versions)
):

1. Ship a release that **adds** the replacement field / version. The old
   field / version remains served. Existing CRs continue to validate.
2. Migrate every CR to the new shape (manual or via a one-shot Job).
3. Ship a release that **removes** the old field / version. Operators
   upgrade `muster-crds` first, verify no CRs reference the removed shape,
   then upgrade `muster`.

Never do both steps in one release — that breaks every CR mid-rollout.

### Re-applying CRDs out-of-band

When a release ships CRD changes that `helm upgrade muster-crds` will not
pick up (the common case), apply them with `kubectl`:

```bash
helm template muster-crds \
  oci://gsoci.azurecr.io/charts/giantswarm/muster-crds \
  --version <new-version> --include-crds \
  | kubectl apply --server-side -f -
```

Then upgrade the App / Helm release to record the new chart version.

## Uninstall

`helm uninstall muster-crds` does **not** delete the CRDs. Garbage-collecting
them is operator-driven and irreversible:

```bash
kubectl delete crd \
  mcpservers.muster.giantswarm.io \
  workflows.muster.giantswarm.io
```

Deleting the CRDs cascade-deletes every `MCPServer` and `Workflow` CR in the
cluster. Confirm with `kubectl get mcpservers,workflows -A` first.
