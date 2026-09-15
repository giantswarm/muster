# Installation

muster ships as a single static binary, a container image and a Helm chart. Pick the form that
matches how it will be used.

| Form | Use it for |
|---|---|
| Release binary | A laptop: `muster serve` or `muster standalone` for an IDE, the CLI against any muster |
| Container image | muster as a service outside Kubernetes, or a custom deployment |
| Helm chart | muster as a shared service on Kubernetes, with MCP servers and workflows as custom resources |

The quick start and the how-to guides apply to all three; only the way definitions are stored
differs (files locally, custom resources on Kubernetes).

## Release binary

Every release publishes binaries for Linux, macOS and Windows on `amd64` and `arm64`, each with a
Sigstore bundle next to it.

With Homebrew on macOS or Linux, the [tap](https://github.com/giantswarm/homebrew-muster) installs
the binary with shell completions for bash, zsh and fish:

```bash
brew install giantswarm/muster/muster
```

`brew upgrade muster` moves to a newer release. The tap follows every release: the release
pipeline notifies it once the binaries are uploaded, and its workflow verifies each binary against
its Sigstore bundle before it regenerates the formula.

Without Homebrew, download the binary for the platform:

```bash
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
curl -fsSL -o muster "https://github.com/giantswarm/muster/releases/latest/download/muster-${os}-${arch}"
chmod +x muster && sudo mv muster /usr/local/bin/
muster version
```

A specific version is under `releases/download/v<version>/`; the Windows binaries are
`muster-windows-amd64.exe` and `muster-windows-arm64.exe`.

A binary installed this way updates itself: `muster self-update` replaces it with the latest
release after verifying its bundle against a CircleCI build of `giantswarm/muster`. A release
without a bundle, or a download that does not match its signature, is refused and the installed
binary stays. A Homebrew install is updated with `brew upgrade` instead.

```bash
muster self-update
```

With a Go toolchain, `go install github.com/giantswarm/muster@latest` builds from source.

### Running as a user service

The repository ships systemd units for a per-user aggregator that starts on login:
[`muster.service`](https://github.com/giantswarm/muster/blob/main/muster.service) and
[`muster.socket`](https://github.com/giantswarm/muster/blob/main/muster.socket). Adjust the binary
path in the service unit, then:

```bash
mkdir -p ~/.config/systemd/user
cp muster.service muster.socket ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now muster.socket muster.service
journalctl --user -u muster -f
```

The socket unit binds `127.0.0.1:8090`; clients use `muster agent --mcp-server` or connect to
`http://localhost:8090/mcp`.

## Container image

The image is `gsoci.azurecr.io/giantswarm/muster:<version>` (without a `v`), built for `amd64`
and `arm64`. It runs `muster` as its entrypoint; mount a configuration directory and bind the
aggregator to all interfaces of the container:

```bash
mkdir -p ./muster-config/mcpservers ./muster-config/workflows
cat > ./muster-config/config.yaml <<'YAML'
aggregator:
  host: 0.0.0.0
  port: 8090
YAML

docker run --rm -p 8090:8090 -v "$PWD/muster-config:/config" \
  gsoci.azurecr.io/giantswarm/muster:5.21.0 serve --config-path /config
```

Inside a container the `stdio` server type is of limited use because the server's binary would
have to be in the image; register remote servers (`streamable-http`, `sse`) instead.

## Helm chart

The chart is published in the Giant Swarm catalog. It deploys muster in *Kubernetes mode*:
`MCPServer`, `Workflow` and `WorkflowExecution` are custom resources in the release namespace,
reconciled by muster, and the CRDs ship with the chart.

```bash
helm repo add giantswarm https://giantswarm.github.io/giantswarm-catalog/
helm repo update
helm install muster giantswarm/muster --namespace muster --create-namespace
kubectl -n muster get pods
```

Register a first server and watch it come up:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kubernetes
  namespace: muster
spec:
  type: streamable-http
  url: https://mcp-kubernetes.example.com/mcp
```

```bash
kubectl -n muster get mcpservers
kubectl -n muster describe mcpserver kubernetes
```

In Kubernetes mode `type: stdio` is rejected: a stdio server would run as a child process of the
muster pod under its service account. Run MCP servers as their own workloads and register them
by URL.

### Reaching the endpoint

The chart creates a `ClusterIP` service on the aggregator port (`8090`). Expose it with the
chart's `ingress` values or, on clusters with the Gateway API, with `gatewayAPI` (an `HTTPRoute`
and a `BackendTrafficPolicy`). Point the CLI at it:

```bash
muster context add prod --endpoint https://muster.example.com/mcp --use
muster list mcpserver
```

### Protecting the endpoint with Dex

A shared muster runs with OAuth 2.1 protection so that every request carries a person's
identity. muster is the resource server; Dex is the identity provider and the only issuer of
identity. The values below are the minimum; `helm/muster/values-oauth-valkey-example.yaml` in
the repository is a complete example.

```yaml
muster:
  oauth:
    server:
      enabled: true
      baseUrl: https://muster.example.com
      provider: dex
      dex:
        issuerUrl: https://dex.example.com
        clientId: muster
      existingSecret: muster-oauth        # dex-client-secret, registration-token, oauth-encryption-key
      encryptionKey: true
```

```bash
kubectl -n muster create secret generic muster-oauth \
  --from-literal=dex-client-secret=<dex client secret> \
  --from-literal=registration-token="$(openssl rand -hex 32)" \
  --from-literal=oauth-encryption-key="$(openssl rand -base64 32)"
```

Dex needs a client `muster` with `https://muster.example.com/oauth/callback` as redirect URI.
Clients then log in through the browser (the CLI with `muster auth login`, IDEs through the stdio
bridge or their own OAuth support); MCP servers that trust the same Dex receive the person's
identity token when their `MCPServer` sets `auth.forwardToken: true`.

### More than one replica

Sessions, grants and OAuth state live in memory by default. For more than one replica, or to
survive a pod restart without every client logging in again, back them with Valkey:

```yaml
replicaCount: 2
muster:
  oauth:
    server:
      storage:
        type: valkey
        valkey:
          url: valkey.muster.svc.cluster.local:6379
          existingSecret: muster-oauth   # key valkey-password
```

A muster whose configured Valkey is unreachable at startup waits for it and exits if it does not
come; it never falls back to in-memory stores, because that would split sessions across replicas.

### Toolsets, metrics and policies

- `muster.toolsetPresets` defines named tool selections that clients reference as
  `preset:<name>` in the `X-muster-Toolset` header; see [Toolsets](../reference/toolsets.md).
- `muster.observability.metrics.prometheus.serviceMonitor.enabled: true` exposes Prometheus
  metrics and creates the `ServiceMonitor`; `prometheusRule.enabled` adds alert rules and
  `grafanaDashboard.enabled` the dashboard. `muster.observability.otel.endpoint` sends traces,
  metrics and logs to an OpenTelemetry collector. See [Observability](../explanation/observability.md).
- `networkPolicy` and the Cilium variant restrict ingress to the aggregator and metrics ports;
  `podDisruptionBudget`, `autoscaling`, `resources` and `affinity` are the usual knobs.
- `muster.extraCaFile` mounts additional CA certificates for MCP servers behind a private CA.

### Upgrades and CRDs

Helm installs the CRDs from the chart's `crds/` directory on a fresh install and does not
update them on `helm upgrade`. When a release changes the CRD schema, apply the new definitions
first:

```bash
helm show crds giantswarm/muster | kubectl apply --server-side -f -
helm upgrade muster giantswarm/muster --namespace muster
```

Flux users set `install.crds: CreateReplace` and `upgrade.crds: CreateReplace` on the
`HelmRelease` instead. The separate `muster-crds` chart is an alternative when the CRD
lifecycle should be managed as its own release.

## Configuration outside the chart

All chart values under `muster.*` render into `config.yaml`; the same keys work in a file for
the binary and the container. [Configuration](../reference/configuration.md) is the complete
reference, [Security](security.md) explains the token lifecycle a protected deployment runs on.
