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
| resources.limits.cpu | string | `"500m"` |  |
| resources.limits.memory | string | `"512Mi"` |  |
| resources.requests.cpu | string | `"100m"` |  |
| resources.requests.memory | string | `"128Mi"` |  |
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
| muster.events | bool | `false` |  |
| muster.oauth.mcpClient.enabled | bool | `false` |  |
| muster.oauth.mcpClient.publicUrl | string | `""` |  |
| muster.oauth.mcpClient.clientId | string | `""` |  |
| muster.oauth.mcpClient.callbackPath | string | `"/oauth/proxy/callback"` |  |
| muster.oauth.mcpClient.cimd.path | string | `"/.well-known/oauth-client.json"` |  |
| muster.oauth.mcpClient.cimd.scopes | string | `""` |  |
| muster.oauth.server.enabled | bool | `false` |  |
| muster.oauth.server.baseUrl | string | `""` |  |
| muster.oauth.server.provider | string | `"dex"` |  |
| muster.oauth.server.dex.issuerUrl | string | `""` |  |
| muster.oauth.server.dex.clientId | string | `""` |  |
| muster.oauth.server.dex.clientSecret | string | `""` |  |
| muster.oauth.server.dex.connectorId | string | `""` |  |
| muster.oauth.server.dex.caFile | string | `""` |  |
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
| muster.oauth.server.registrationToken | string | `""` |  |
| muster.oauth.server.allowPublicClientRegistration | bool | `false` |  |
| muster.oauth.server.encryptionKey | bool | `false` |  |
| muster.oauth.server.encryptionKeyValue | string | `""` |  |
| muster.oauth.server.trustedPublicRegistrationSchemes | list | `[]` |  |
| muster.oauth.server.enableCIMD | bool | `true` |  |
| muster.oauth.server.allowLocalhostRedirectURIs | bool | `true` |  |
| muster.oauth.server.trustedAudiences | list | `[]` |  |
| crds.install | bool | `true` |  |
| ciliumNetworkPolicy.enabled | bool | `false` |  |
| ciliumNetworkPolicy.labels | object | `{}` |  |
| ciliumNetworkPolicy.annotations | object | `{}` |  |
| ciliumNetworkPolicy.allowClusterIngress | bool | `false` |  |
