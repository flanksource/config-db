# config-db

**config-db** is developer first, JSON based configuration management database (CMDB)

## Setup

```bash
make build
```

### Run as server

Starting the server will run the migrations and start scraping in background (The `default-schedule` configuration will run scraping every 60 minutes if configuration is not explicitly specified).

```bash
DB_URL=postgres://<username>:<password>@localhost:5432/<db_name> ./.bin/config-db serve --db-migrations
```

### Scape config

To explicitly run scraping with a particular configuration:

```bash
./.bin/config-db run <scrapper-config.yaml> -vvv
```

See `fixtures/` for example scraping configurations.

## Principles

* **JSON Based** - Configuration is stored in JSON, with changes recorded as JSON patches that enables highly structured search.
* **SPAM Free** - Not all configuration data is useful, and overly verbose change histories are difficult to navigate.
* **GitOps Ready** - Configuration should be stored in Git, config-db enables the extraction of configuration out of Git repositories with branch/environment awareness.
* **Topology Aware** - Configuration can often have an inheritance or override hierarchy.

## Capabilities

* View and search change history in any dimension (node, zone, environment, application, technology)
* Compare and diff configuration across environments.

## Configuration Sources

* AWS
  * [x] EC2 (including trusted advisor, compliance and patch reporting)
  * [x] VPC
  * [x] IAM
* Azure
* Kubernetes
  * [x] Pods
  * [x] Secrets / ConfigMaps
  * [x] LoadBalancers / Ingress
  * [x] Nodes
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
