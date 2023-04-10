# config-db

**config-db** is developer first, JSON based configuration management database (CMDB).

## Principles

* **JSON Based** - Configuration is stored in JSON, with changes recorded as JSON patches that enables highly structured search.
* **SPAM Free** - Not all configuration data is useful, and overly verbose change histories are difficult to navigate.
* **GitOps Ready** - Configuration should be stored in Git, config-db enables the extraction of configuration out of Git repositories with branch/environment awareness.
* **Topology Aware** - Configuration can often have an inheritance or override hierarchy.

## Capabilities

* View and search change history in any dimension (node, zone, environment, application, technology)
* Compare and diff configuration across environments.

## Quick Start

Before installing Config-DB , please ensure you have the [prerequisites installed](docs/prereqs.md) on your Kubernetes cluster.

The recommended method for installing Config-DB is using [helm](https://helm.sh/)

### Install Helm

The following steps will install the latest version of helm

```bash
curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
chmod 700 get_helm.sh
./get_helm.sh
```

### Add the Flanksource helm repository

```bash
helm repo add flanksource https://flanksource.github.io/charts
helm repo update
```

### Configurable fields

See the [values file](chart/values.yaml) for the full list of configurable fields.  Mandatory configuration values are for the configuration of the database, and it is recommended to also configure the UI ingress.

#### DB

ConfigDB requires a Postgres server to function.  A basic postgres server can be installed by the helm chart.

##### Chart-managed Server

|                     |        |
|---------------------|--------|
| db.external.create  | `true` |
| db.external.storageClass | Set to name of a storageclass available in the cluster |
| db.external.storage | Set to volume of storage to request |

The helm chart will create a postgres server statefulset, with a random password and default port, along with a configdb database hosted on the server.

To specify a username and password for the chart-managed Postgres server, create a secret in the namespace that the chart will install to, named `postgres-connection`, which contains `POSTGRES_USER` and `POSTGRES_PASSWORD` keys.  If no pre-existing secret is created, a user called 'postgres' will be given a random password.

##### Prexisting Server

In order to connect to an existing Postgres server, a database must be created on the server, along with a user that has admin permissions

|                     |         |
|---------------------|---------|
| db.external.create  | `false` |
| db.external.secretKeyRef.name | Set to name of name of secret that contains a key containging the postgres connection URI |
| db.external.secretKeyRef.key | Set to the name of the key in the secret that contains the postgres connection URI |

The connection URI must be specified in the format `postgresql://"$user":"$password"@"$host"/"$database"`

#### Ingress 

In order to view the ConfigDB UI, it must be exposed  using an ingress:

|                     |                   |
|---------------------|-------------------|
| ingress.host | URL at which the UI will be accessed |
| ingress.annotations | Map of annotations required by the ingress controller or certificate issuer |
| ingress.tls | Map of configuration options for TLS |

More details regarding ingress configuration can be found in the [kubernetes documentation](https://kubernetes.io/docs/concepts/services-networking/ingress/)

### Deploy using Helm

To install into a new `config-db` namespace, run

```bash
helm install config-db-demo --wait -n config-db --create-namespace flanksource/config-db -f values.yaml
```

where `values.yaml` contains the configuration options detailed above.  eg

```yaml
db:
  create: true
  storageClass: default
  storage: 30Gi
ingress:
  host: config-db.flanksource.com
  annotations:
    kubernetes.io/ingress.class: nginx
    kubernetes.io/tls-acme: "true"
  tls:
    - secretName: config-db-tls
      hosts:
      - config-db.flanksource.com
```


## Configuration Sources

* AWS
  * [x] EC2 (including trusted advisor, compliance and patch reporting)
  * [x] VPC
  * [ ] IAM
* Kubernetes
  * [ ] Pods
  * [ ] Secrets / ConfigMaps
  * [ ] LoadBalancers / Ingress
  * [ ] Nodes
* Configuration Files
  * [ ] YAML/JSON
  * [ ] Properties files
* Dependency Graphs
  * [ ] pom.xml
  * [ ] package.json
  * [ ] go.mod
* Infrastructure as Code
  * [ ] Terraform
  * [ ] CloudFormation
  * [ ] Ansible

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md)
