# config-db

![Version: 0.3.0](https://img.shields.io/badge/Version-0.3.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.5](https://img.shields.io/badge/AppVersion-0.0.5-informational?style=flat-square)

A Helm chart for config-db

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| configAnalysisRetentionDays | int | `60` |  |
| configChangeRetentionDays | int | `60` |  |
| db.embedded.persist | bool | `true` |  |
| db.embedded.storage | string | `"20Gi"` |  |
| db.embedded.storageClass | string | `""` |  |
| db.external.create | bool | `false` |  |
| db.external.enabled | bool | `false` |  |
| db.external.secretKeyRef.key | string | `"DB_URL"` |  |
| db.external.secretKeyRef.name | string | `"config-db-postgresql"` |  |
| db.external.storage | string | `"20Gi"` |  |
| db.external.storageClass | string | `""` |  |
| db.runMigrations | bool | `true` |  |
| disablePostgrest | bool | `false` |  |
| env | object | `{}` |  |
| extra | object | `{}` |  |
| extraArgs | object | `{}` |  |
| extraEnvFrom | list | `[]` |  |
| global.affinity | object | `{}` |  |
| global.db.connectionPooler.enabled | bool | `false` |  |
| global.db.connectionPooler.secretKeyRef.key | string | `"DB_URL"` |  |
| global.db.connectionPooler.secretKeyRef.name | string | `"mission-control-connection-pooler"` |  |
| global.imagePrefix | string | `"flanksource"` |  |
| global.imagePullSecrets | list | `[]` |  |
| global.imageRegistry | string | `"docker.io"` |  |
| global.labels | object | `{}` |  |
| global.nodeSelector | object | `{}` |  |
| global.otel.collector | string | `""` |  |
| global.otel.labels | string | `""` |  |
| global.serviceAccount.annotations | object | `{}` |  |
| global.serviceAccount.name | string | `""` |  |
| global.serviceMonitor.enabled | bool | `false` |  |
| global.serviceMonitor.labels | object | `{}` |  |
| global.tolerations | list | `[]` |  |
| image.name | string | `"{{.Values.global.imagePrefix}}/config-db"` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.tag | string | `"latest"` |  |
| imagePullSecrets | list | `[]` |  |
| ingress.annotations | object | `{}` |  |
| ingress.enabled | bool | `false` |  |
| ingress.host | string | `"config-db.local"` |  |
| ingress.tls | list | `[]` |  |
| jsonLogs | bool | `true` |  |
| logLevel | string | `""` |  |
| nameOverride | string | `""` |  |
| nodeSelector | object | `{}` |  |
| otel.collector | string | `""` |  |
| otel.labels | string | `""` |  |
| otel.serviceName | string | `"config-db"` |  |
| podSecurityContext.fsGroup | int | `1000` |  |
| properties | object | `{}` |  |
| replicas | int | `1` |  |
| resources.limits.cpu | string | `"500m"` |  |
| resources.limits.memory | string | `"4Gi"` |  |
| resources.requests.cpu | string | `"200m"` |  |
| resources.requests.memory | string | `"1Gi"` |  |
| scrapeRuleConfigMaps[0] | string | `"config-db-rules"` |  |
| securityContext | object | `{}` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `"config-db-sa"` |  |
| serviceAccount.rbac.clusterRole | bool | `true` |  |
| serviceAccount.rbac.configmaps | bool | `true` |  |
| serviceAccount.rbac.exec | bool | `true` |  |
| serviceAccount.rbac.readAll | bool | `true` |  |
| serviceAccount.rbac.secrets | bool | `true` |  |
| serviceAccount.rbac.tokenRequest | bool | `true` |  |
| serviceMonitor.enabled | bool | `false` |  |
| serviceMonitor.labels | object | `{}` |  |
| tolerations | list | `[]` |  |
| upstream.enabled | bool | `false` |  |
| upstream.pageSize | int | `500` |  |
| upstream.secretKeyRef.name | string | `"config-db-upstream"` |  |
| volumeMounts | list | `[]` |  |
| volumes | list | `[]` |  |

