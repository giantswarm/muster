# muster

![Version: 0.3.12](https://img.shields.io/badge/Version-0.3.12-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.3.12](https://img.shields.io/badge/AppVersion-0.3.12-informational?style=flat-square)

A Helm chart for muster - Universal Control Plane for AI Agents built on MCP

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
| enabled | bool | `true` |  |
| replicaCount | int | `1` |  |
| image.registry | string | `"gsoci.azurecr.io"` |  |
| image.repository | string | `"giantswarm/muster"` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.tag | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| nameOverride | string | `""` |  |
| fullnameOverride | string | `""` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.automount | bool | `false` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.name | string | `""` |  |
| rbac.create | bool | `true` |  |
| rbac.additionalSecretNamespaces | list | `[]` |  |
| podAnnotations | object | `{}` |  |
| podLabels | object | `{}` |  |
| podSecurityContext.runAsUser | int | `1000` |  |
| podSecurityContext.runAsGroup | int | `1000` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.fsGroup | int | `1000` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.runAsUser | int | `1000` |  |
| securityContext.runAsGroup | int | `1000` |  |
| securityContext.runAsNonRoot | bool | `true` |  |
| securityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| service.type | string | `"ClusterIP"` |  |
| service.port | int | `8090` |  |
| ingress.enabled | bool | `false` |  |
| ingress.className | string | `""` |  |
| ingress.annotations | object | `{}` |  |
| ingress.hosts[0].host | string | `"muster.local"` |  |
| ingress.hosts[0].paths[0].path | string | `"/"` |  |
| ingress.hosts[0].paths[0].pathType | string | `"Prefix"` |  |
| ingress.tls | list | `[]` |  |
| gatewayAPI.enabled | bool | `false` |  |
| gatewayAPI.httpRoute.parentRefs | list | `[]` |  |
| gatewayAPI.httpRoute.hostnames | list | `[]` |  |
| gatewayAPI.httpRoute.rules | list | `[]` |  |
| gatewayAPI.httpRoute.annotations | object | `{}` |  |
| gatewayAPI.httpRoute.labels | object | `{}` |  |
| gatewayAPI.backendTrafficPolicy.enabled | bool | `false` |  |
| gatewayAPI.backendTrafficPolicy.timeout | string | `"0s"` |  |
| gatewayAPI.backendTrafficPolicy.annotations | object | `{}` |  |
| gatewayAPI.backendTrafficPolicy.labels | object | `{}` |  |
| revisionHistoryLimit | int | `3` |  |
| resources.limits.cpu | string | `"500m"` |  |
| resources.limits.memory | string | `"512Mi"` |  |
| resources.limits.ephemeral-storage | string | `"100Mi"` |  |
| resources.requests.cpu | string | `"100m"` |  |
| resources.requests.memory | string | `"128Mi"` |  |
| resources.requests.ephemeral-storage | string | `"50Mi"` |  |
| autoscaling.enabled | bool | `false` |  |
| autoscaling.minReplicas | int | `1` |  |
| autoscaling.maxReplicas | int | `10` |  |
| autoscaling.targetCPUUtilizationPercentage | int | `80` |  |
| podDisruptionBudget.enabled | bool | `false` |  |
| podDisruptionBudget.minAvailable | int | `1` |  |
| volumes | list | `[]` |  |
| volumeMounts | list | `[]` |  |
| nodeSelector | object | `{}` |  |
| tolerations | list | `[]` |  |
| affinity | object | `{}` |  |
| muster.aggregator.port | int | `8090` |  |
| muster.aggregator.transport | string | `"streamable-http"` |  |
| muster.namespace | string | `""` |  |
| muster.debug | bool | `false` |  |
| muster.extraCaFile.path | string | `"/etc/muster/ca/extra-ca.pem"` |  |
| muster.extraCaFile.secret.name | string | `""` |  |
| muster.extraCaFile.secret.key | string | `"ca.pem"` |  |
| muster.oauth.mcpClient.enabled | bool | `false` |  |
| muster.oauth.mcpClient.publicUrl | string | `""` |  |
| muster.oauth.mcpClient.clientId | string | `""` |  |
| muster.oauth.mcpClient.callbackPath | string | `"/oauth/proxy/callback"` |  |
| muster.oauth.mcpClient.cimd.path | string | `"/.well-known/oauth-client.json"` |  |
| muster.oauth.mcpClient.cimd.scopes | string | `""` |  |
| muster.oauth.mcpClient.postLoginRedirectAllowlist | list | `[]` |  |
| muster.oauth.server.enabled | bool | `false` |  |
| muster.oauth.server.baseUrl | string | `""` |  |
| muster.oauth.server.provider | string | `"dex"` |  |
| muster.oauth.server.dex.issuerUrl | string | `""` |  |
| muster.oauth.server.dex.clientId | string | `""` |  |
| muster.oauth.server.dex.clientSecret | string | `""` |  |
| muster.oauth.server.dex.connectorId | string | `""` |  |
| muster.oauth.server.dex.allowPrivateIPOIDC | bool | `false` |  |
| muster.oauth.server.dex.requestableAudiences | list | `[]` |  |
| muster.oauth.server.google.clientId | string | `""` |  |
| muster.oauth.server.google.clientSecret | string | `""` |  |
| muster.oauth.server.existingSecret | string | `""` |  |
| muster.oauth.server.storage.type | string | `"memory"` |  |
| muster.oauth.server.storage.valkey.url | string | `""` |  |
| muster.oauth.server.storage.valkey.password | string | `""` |  |
| muster.oauth.server.storage.valkey.tls.enabled | bool | `false` |  |
| muster.oauth.server.storage.valkey.keyPrefix | string | `"muster:"` |  |
| muster.oauth.server.storage.valkey.db | int | `0` |  |
| muster.oauth.server.storage.valkey.existingSecret | string | `""` |  |
| muster.oauth.server.storage.valkey.secretKeyPassword | string | `"valkey-password"` |  |
| muster.oauth.server.registrationToken | string | `""` | ------------------------------------------------------------------------- Shared secret a client must present at the DCR endpoint to register. Supply via existingSecret (key: registration-token) in production so the value never appears in Helm release history. |
| muster.oauth.server.allowPublicClientRegistration | bool | `false` |  |
| muster.oauth.server.encryptionKey | bool | `false` |  |
| muster.oauth.server.encryptionKeyValue | string | `""` |  |
| muster.oauth.server.trustedPublicRegistrationSchemes | list | `[]` |  |
| muster.oauth.server.trustedPublicRegistrationRedirectURIs | list | `[]` |  |
| muster.oauth.server.enableCIMD | bool | `true` |  |
| muster.oauth.server.allowLocalhostRedirectURIs | bool | `true` |  |
| muster.oauth.server.trustedAudiences | list | `[]` | ------------------------------------------------------------------------- OIDC client IDs (audiences) whose JWTs muster accepts directly as bearer tokens without the client completing muster's own OAuth flow.  The token's aud claim must match one of these values and the signature must validate against the provider's JWKS. SECURITY: only list client IDs you fully trust. |
| muster.oauth.server.trustedIssuers | list | `[]` |  |
| muster.oauth.server.tokenExchangeBroker | object | `{}` |  |
| muster.oauth.server.trustedProxyCIDRs | list | `[]` |  |
| muster.oauth.server.resourceIdentifier | string | `""` |  |
| muster.observability.otel.endpoint | string | `""` |  |
| muster.observability.otel.protocol | string | `"grpc"` |  |
| muster.observability.otel.headers | string | `""` |  |
| muster.observability.otel.resourceAttributes | string | `""` |  |
| muster.observability.metrics.exporter | string | `""` |  |
| muster.observability.metrics.prometheus.port | int | `9464` |  |
| muster.observability.metrics.prometheus.serviceMonitor.enabled | bool | `false` |  |
| muster.observability.metrics.prometheus.serviceMonitor.interval | string | `""` |  |
| muster.observability.metrics.prometheus.serviceMonitor.labels | object | `{}` |  |
| crds.install | bool | `false` |  |
| crds.annotations."helm.sh/resource-policy" | string | `"keep"` |  |
| networkPolicy.enabled | bool | `false` |  |
| networkPolicy.flavor | string | `"cilium"` |  |
| networkPolicy.labels | object | `{}` |  |
| networkPolicy.annotations | object | `{}` |  |
| networkPolicy.cilium.allowClusterIngress | bool | `false` |  |
| networkPolicy.cilium.ingressGateway.endpointSelector."k8s:io.kubernetes.pod.namespace" | string | `"envoy-gateway-system"` |  |
| networkPolicy.cilium.ingressGateway.endpointSelector."app.kubernetes.io/name" | string | `"envoy"` |  |
| networkPolicy.cilium.ingressGateway.ports[0] | int | `10080` |  |
| networkPolicy.cilium.ingressGateway.ports[1] | int | `10443` |  |
| networkPolicy.cilium.additionalEgress | list | `[]` |  |
| networkPolicy.kubernetes.apiServerCIDR | string | `"0.0.0.0/0"` |  |
| networkPolicy.kubernetes.clusterCIDR | string | `""` |  |
| networkPolicy.kubernetes.worldExcludedCIDRs[0] | string | `"10.0.0.0/8"` |  |
| networkPolicy.kubernetes.worldExcludedCIDRs[1] | string | `"172.16.0.0/12"` |  |
| networkPolicy.kubernetes.worldExcludedCIDRs[2] | string | `"192.168.0.0/16"` |  |
| networkPolicy.kubernetes.worldExcludedCIDRs[3] | string | `"169.254.0.0/16"` |  |
| networkPolicy.kubernetes.additionalEgress | list | `[]` |  |
