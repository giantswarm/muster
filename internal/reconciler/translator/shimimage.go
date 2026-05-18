package translator

// ShimImage is the OCI reference of the musterstdio shim image emitted by the
// K8s emitter for every stdio-derived backend. It is pinned to the muster
// version this binary was built against so the shim deployed in cluster mode
// matches the reconciler that wrote the Deployment.
//
// The image is built from Dockerfile.musterstdio in this repository and
// published as part of the muster release pipeline (see .goreleaser.yaml's
// musterstdio build target).
//
// Bumping the muster version is a deliberate change: update this constant in
// the same PR that bumps go.mod / CHANGELOG.md.
const ShimImage = "gsoci.azurecr.io/giantswarm/musterstdio:dev"
