# config-db

![Version: 0.3.0](https://img.shields.io/badge/Version-0.3.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.5](https://img.shields.io/badge/AppVersion-0.0.5-informational?style=flat-square)

A Helm chart for config-db

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for pod scheduling |
| clickhouse.enabled | bool | `false` | Enable ClickHouse for analytics |
| clickhouse.image.name | string | `"clickhouse/clickhouse-server"` | ClickHouse image name |
| clickhouse.image.tag | string | `"25.4"` | ClickHouse image tag |
| clickhouse.properties | object | `{"keep_alive_timeout":"300","mark_cache_size":"67108864","max_concurrent_queries":"10","max_connections":"4","uncompressed_cache_size":"134217728"}` | ClickHouse server properties |
| clickhouse.resources.limits.cpu | string | `"1"` |  |
| clickhouse.resources.limits.memory | string | `"4Gi"` |  |
| clickhouse.resources.requests.cpu | string | `"100m"` |  |
| clickhouse.resources.requests.memory | string | `"2Gi"` |  |
| configAnalysisRetentionDays | int | `60` | Retention days for config analysis |
| configChangeRetentionDays | int | `60` | Retention days for config changes |
| db.embedded.persist | bool | `true` | If the database is embedded, setting this to true will persist the contents of the database through a persistent volume |
| db.embedded.storage | string | `"20Gi"` | Storage size for the embedded database persistent volume |
| db.embedded.storageClass | string | `""` | Storage class for the embedded database persistent volume |
| db.external.create | bool | `false` | Setting create:true will create   - a postgres stateful set   - the secret &   - the service to expose the postgres stateful set. By default, the generated secret will use 'postgres' as the username and a randomly generated password. If you need to set a custom username and password, you can populate a secret named 'postgres-connection' before install with POSTGRES_USER and POSTGRES_PASSWORD  If create:false, a preexisting secret containing the URI to an existing postgres database must be provided The URI must be in the format 'postgresql://"$user":"$password"@"$host"/"$database"' |
| db.external.enabled | bool | `false` | Setting enabled to true will use an external postgres DB. You can either use the embedded db or an external db. If both is enabled, then embedded db will take precedence. |
| db.external.secretKeyRef.key | string | `"DB_URL"` | This is the key that we look for in the secret. |
| db.external.secretKeyRef.name | string | `"config-db-postgresql"` | The name of the secret to look for. |
| db.external.storage | string | `"20Gi"` | Storage size for the external database persistent volume (if create=true) |
| db.external.storageClass | string | `""` | Storage class for the external database persistent volume (if create=true) |
| db.runMigrations | bool | `true` | Run database migrations on startup |
| disablePostgrest | bool | `false` | Set to true if you want to disable the postgrest service |
| env | object | `{}` |  |
| extra | object | `{}` |  |
| extraArgs | object | `{}` |  |
| extraEnvFrom | list | `[]` |  |
| global.affinity | object | `{}` |  |
| global.db.connectionPooler.enabled | bool | `false` |  |
| global.db.connectionPooler.secretKeyRef.key | string | `"DB_URL"` |  |
| global.db.connectionPooler.secretKeyRef.name | string | `"mission-control-connection-pooler"` |  |
| global.imagePrefix | string | `"flanksource"` | Global image prefix to use for all images |
| global.imagePullSecrets | list | `[]` | Global image pull secrets |
| global.imageRegistry | string | `"docker.io"` | Global image registry to use for all images |
| global.labels | object | `{}` |  |
| global.nodeSelector | object | `{}` |  |
| global.otel.collector | string | `""` | OpenTelemetry gRPC collector endpoint in host:port format |
| global.otel.labels | string | `""` | labels in "a=b,c=d" format |
| global.serviceAccount.annotations | object | `{}` |  |
| global.serviceAccount.name | string | `""` | Note unlike other globals, the global serviceAccount.name overrides the local value |
| global.serviceMonitor.enabled | bool | `false` | Set to true to enable prometheus service monitor globally |
| global.serviceMonitor.labels | object | `{}` |  |
| global.tolerations | list | `[]` |  |
| image.name | string | `"{{.Values.global.imagePrefix}}/config-db"` | Name of the main application image |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.tag | string | `"latest"` | Overrides the image tag whose default is the chart appVersion. |
| imagePullSecrets | list | `[]` | Image pull secrets for the main application image |
| imageRegistry | string | `""` | Image registry for the main application image. Overrides global.imageRegistry if set. |
| ingress.annotations | object | `{}` |  |
| ingress.enabled | bool | `false` | Enable ingress for the application |
| ingress.host | string | `"config-db.local"` | Hostname for the ingress |
| ingress.tls | list | `[]` | TLS configuration for the ingress |
| jsonLogs | bool | `true` | Enable JSON formatted logs |
| logLevel | string | `""` | Log level for the application (e.g., info, debug, error) |
| nameOverride | string | `""` | Use this only if you want to replace the default that is .Chart.Name as the name of all the objects. |
| nodeSelector | object | `{}` | Node selector for pod assignment |
| otel.collector | string | `""` | OpenTelemetry gRPC collector endpoint in host:port format |
| otel.labels | string | `""` | labels in "a=b,c=d" format |
| otel.serviceName | string | `"config-db"` | OpenTelemetry service name |
| podSecurityContext.fsGroup | int | `1000` |  |
| properties | object | `{}` |  |
| replicas | int | `1` | Number of replicas for the deployment |
| resources.limits.cpu | string | `"500m"` |  |
| resources.limits.memory | string | `"4Gi"` |  |
| resources.requests.cpu | string | `"200m"` |  |
| resources.requests.memory | string | `"1Gi"` |  |
| scrapeRuleConfigMaps | list | `["config-db-rules"]` | a list of configmaps to load scrape rules from, the configmap should have a single entry called "config.yaml" |
| securityContext | object | `{}` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` | Create a new service account |
| serviceAccount.name | string | `"config-db-sa"` | Name of the service account to use or create |
| serviceAccount.rbac.clusterRole | bool | `true` | Whether to create cluster-wide or namespaced roles |
| serviceAccount.rbac.configmaps | bool | `true` | for secret management with valueFrom |
| serviceAccount.rbac.exec | bool | `true` | for kubernetesFile lookups |
| serviceAccount.rbac.readAll | bool | `true` | for use with kubernetes resource lookups |
| serviceAccount.rbac.secrets | bool | `true` | for secret management with valueFrom |
| serviceAccount.rbac.tokenRequest | bool | `true` | for secret management with valueFrom |
| serviceMonitor.enabled | bool | `false` | Set to true to enable prometheus service monitor for this service |
| serviceMonitor.labels | object | `{}` |  |
| tolerations | list | `[]` | Tolerations for pod scheduling |
| upstream.enabled | bool | `false` | Enable upstream configuration |
| upstream.pageSize | int | `500` | Page size for upstream communication |
| upstream.secretKeyRef.name | string | `"config-db-upstream"` | Name of the secret containing upstream credentials. Must contain: AGENT_NAME, UPSTREAM_USER, UPSTREAM_PASSWORD & UPSTREAM_HOST |
| volumeMounts | list | `[]` | Additional volumeMounts on the output Deployment definition. |
| volumes | list | `[]` | Additional volumes on the output Deployment definition. |

