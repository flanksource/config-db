# config-db

**config-db** is developer first, JSON based configuration management database (CMDB).

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
  * [ ] VPC
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
