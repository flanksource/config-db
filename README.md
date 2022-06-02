# config-db

**config-db** is developer first, JSON based configuration management database (CMDB).

## Setup

### Setup local db link as environment variable.
```bash
export DB_URL=postgres://<username>@localhost:5432/config
```

### Create `config` database.
```sql
create database config
```

### Scape config and serve

Starting server will run migrations and start scrapping in background (see default-schedule config).

```bash
make build

./bin/config-db serve
```

To explicitly run scrapping.

```bash
./bin/config-db run <scrapper-config.yaml> -vvv
confighub serve
```

See fixtures/ for example scrape configs.

### Migrations

Commands `./bin/config-db serve` or `./bin/config-db run` would run the migrations.

Setup [goose](https://github.com/pressly/goose) for more option on migration. Goose commands need to be run from `db/migrations` directory.

```bash
GOOSE_DRIVER=postgres GOOSE_DBSTRING="user=postgres dbname=config sslmode=disable" goose down
```



## Principles

* **JSON Based** - Configuration is stored in JSON, with changes recorded as JSON patches that enables highly structured search.
* **SPAM Free** - Not all configuration data is useful, and overly verbose change histories are difficult to navigate.
* **GitOps Ready** - Configuration should be stored in Git, config-db enables the extraction of configuration out of Git repositories with branch/environment awareness.
* **Topology Aware** - Configuration can often have an inheritence or override hiearchy.

## Capabilties

* View and search change history in any dimension (node, zone, environment, applictation, technology)
* Compare and diff configuration across environments.

## Configuration Sources

* AWS
  * [x] EC2 (including trusted advisor, compliance and patch reporting)
  * [x] VPC
  * [ ] IAM
* Kubernetes
  [ ] Pods
  [ ] Secrets / ConfigMaps
  [ ] LoadBalancers / Ingress
  [ ] Nodes
* Configuration Files
  [ ] YAML/JSON
  [ ] Properties files
* Dependency Graphs
  [ ] pom.xml
  [ ] package.json
  [ ] go.mod
* Infrastructure as Code
  [ ] Terraform
  [ ] Cloudformation
  [ ] Ansible

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md)
