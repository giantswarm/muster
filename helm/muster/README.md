# muster

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

A Helm chart for muster - Universal Control Plane for AI Agents built on MCP

**Homepage:** <https://github.com/giantswarm/muster>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Giant Swarm | <team-planeteers@giantswarm.io> |  |

## Source Code

* <https://github.com/giantswarm/muster>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| autoscaling.enabled | bool | `false` |  |
| autoscaling.maxReplicas | int | `10` |  |
| autoscaling.minReplicas | int | `1` |  |
| autoscaling.targetCPUUtilizationPercentage | int | `80` |  |
| ciliumNetworkPolicy.allowClusterIngress | bool | `false` |  |
| ciliumNetworkPolicy.annotations | object | `{}` |  |
| ciliumNetworkPolicy.enabled | bool | `false` |  |
| ciliumNetworkPolicy.labels | object | `{}` |  |
| crds.install | bool | `true` |  |
| fullnameOverride | string | `""` |  |
| gatewayAPI.backendTrafficPolicy.annotations | object | `{}` |  |
| gatewayAPI.backendTrafficPolicy.enabled | bool | `false` |  |
| gatewayAPI.backendTrafficPolicy.labels | object | `{}` |  |
| gatewayAPI.backendTrafficPolicy.timeout | string | `"0s"` |  |
| gatewayAPI.enabled | bool | `false` |  |
| gatewayAPI.httpRoute.annotations | object | `{}` |  |
| gatewayAPI.httpRoute.hostnames | list | `[]` |  |
| gatewayAPI.httpRoute.labels | object | `{}` |  |
| gatewayAPI.httpRoute.parentRefs | list | `[]` |  |
| gatewayAPI.httpRoute.rules | list | `[]` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.registry | string | `"gsoci.azurecr.io"` |  |
| image.repository | string | `"giantswarm/muster"` |  |
| image.tag | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| ingress.annotations | object | `{}` |  |
| ingress.className | string | `""` |  |
| ingress.enabled | bool | `false` |  |
| ingress.hosts[0].host | string | `"muster.local"` |  |
| ingress.hosts[0].paths[0].path | string | `"/"` |  |
| ingress.hosts[0].paths[0].pathType | string | `"Prefix"` |  |
| ingress.tls | list | `[]` |  |
| muster.aggregator.port | int | `8090` |  |
| muster.aggregator.transport | string | `"streamable-http"` |  |
| muster.debug | bool | `false` |  |
| muster.events | bool | `false` |  |
| muster.namespace | string | `""` |  |
| muster.oauth.mcpClient.callbackPath | string | `"/oauth/proxy/callback"` |  |
| muster.oauth.mcpClient.cimd.path | string | `"/.well-known/oauth-client.json"` |  |
| muster.oauth.mcpClient.cimd.scopes | string | `""` |  |
| muster.oauth.mcpClient.clientId | string | `""` |  |
| muster.oauth.mcpClient.enabled | bool | `false` |  |
| muster.oauth.mcpClient.publicUrl | string | `""` |  |
| muster.oauth.server.allowLocalhostRedirectURIs | bool | `true` |  |
| muster.oauth.server.allowPublicClientRegistration | bool | `false` |  |
| muster.oauth.server.baseUrl | string | `""` |  |
| muster.oauth.server.dex.clientId | string | `""` |  |
| muster.oauth.server.dex.clientSecret | string | `""` |  |
| muster.oauth.server.dex.connectorId | string | `""` |  |
| muster.oauth.server.dex.issuerUrl | string | `""` |  |
| muster.oauth.server.enableCIMD | bool | `true` |  |
| muster.oauth.server.enabled | bool | `false` |  |
| muster.oauth.server.encryptionKey | bool | `false` |  |
| muster.oauth.server.encryptionKeyValue | string | `""` |  |
| muster.oauth.server.existingSecret | string | `""` |  |
| muster.oauth.server.google.clientId | string | `""` |  |
| muster.oauth.server.google.clientSecret | string | `""` |  |
| muster.oauth.server.provider | string | `"dex"` |  |
| muster.oauth.server.registrationToken | string | `""` |  |
| muster.oauth.server.storage.type | string | `"memory"` |  |
| muster.oauth.server.storage.valkey.db | int | `0` |  |
| muster.oauth.server.storage.valkey.existingSecret | string | `""` |  |
| muster.oauth.server.storage.valkey.keyPrefix | string | `"muster:"` |  |
| muster.oauth.server.storage.valkey.password | string | `""` |  |
| muster.oauth.server.storage.valkey.secretKeyPassword | string | `"valkey-password"` |  |
| muster.oauth.server.storage.valkey.tls.enabled | bool | `false` |  |
| muster.oauth.server.storage.valkey.url | string | `""` |  |
| muster.oauth.server.trustedAudiences | list | `[]` |  |
| muster.oauth.server.trustedPublicRegistrationRedirectURIs | list | `[]` |  |
| muster.oauth.server.trustedPublicRegistrationSchemes | list | `[]` |  |
| muster.observability.metrics.exporter | string | `""` |  |
| muster.observability.metrics.prometheus.port | int | `9464` |  |
| muster.observability.metrics.prometheus.serviceMonitor.enabled | bool | `false` |  |
| muster.observability.metrics.prometheus.serviceMonitor.interval | string | `""` |  |
| muster.observability.metrics.prometheus.serviceMonitor.labels | object | `{}` |  |
| muster.observability.otel.endpoint | string | `""` |  |
| muster.observability.otel.headers | string | `""` |  |
| muster.observability.otel.protocol | string | `"grpc"` |  |
| muster.observability.otel.resourceAttributes | string | `""` |  |
| nameOverride | string | `""` |  |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podDisruptionBudget.enabled | bool | `false` |  |
| podDisruptionBudget.minAvailable | int | `1` |  |
| podLabels | object | `{}` |  |
| podSecurityContext.fsGroup | int | `1000` |  |
| podSecurityContext.runAsGroup | int | `1000` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.runAsUser | int | `1000` |  |
| rbac.create | bool | `true` |  |
| replicaCount | int | `1` |  |
| resources.limits.cpu | string | `"500m"` |  |
| resources.limits.memory | string | `"512Mi"` |  |
| resources.requests.cpu | string | `"100m"` |  |
| resources.requests.memory | string | `"128Mi"` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.runAsGroup | int | `1000` |  |
| securityContext.runAsNonRoot | bool | `true` |  |
| securityContext.runAsUser | int | `1000` |  |
| securityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| service.port | int | `8090` |  |
| service.type | string | `"ClusterIP"` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.automount | bool | `false` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| tolerations | list | `[]` |  |
| volumeMounts | list | `[]` |  |
| volumes | list | `[]` |  |
