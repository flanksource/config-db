# config-db

[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/flanksource/config-db/badge)](https://securityscorecards.dev/viewer/?uri=github.com/flanksource/config-db)

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

### Diagnose a scraper

Run read-only doctor checks for a local scrape configuration:

```bash
./.bin/config-db doctor fixtures/github-doctor.yaml
```

Pass a persisted scraper UUID instead when a database is configured:

```bash
DB_URL=postgres://<username>:<password>@localhost:5432/<db_name> ./.bin/config-db doctor <scraper-id>
```

The GitHub doctor checks only the endpoint families enabled by the scraper configuration. It reports GitHub's accepted fine-grained permissions and OAuth scopes, any OAuth scopes explicitly reported for the token, and whether each request succeeded, was denied, or was skipped because the feature is disabled. A successful authenticated request is evidence that the configured token can perform that operation; it is not presented as a complete list of fine-grained token grants.

Organization rulesets are checked only when `organizations[].rulesets` is enabled. GitHub requires the fine-grained `organization_administration=write` permission to read organization repository rulesets, so the doctor reports that documented requirement even when a denied response omits the permission header. App installation checks use `GET /orgs/{org}/installations`, which requires `organization_administration=read`; selected repository grants are not inferred because that organization endpoint does not expose them.

Use Clicky output flags for machine-readable or alternate output:

```bash
./.bin/config-db doctor fixtures/github-doctor.yaml --json
./.bin/config-db doctor fixtures/github-doctor.yaml --format markdown
```

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
