# v0.0.1180 (since [v0.0.1008](https://github.com/flanksource/config-db/releases/tag/v0.0.1008))

## Highlights

### Azure DevOps Classic Release Pipelines <sup>Mission Control</sup>

Config-db now scrapes Azure DevOps classic (non-YAML) release pipelines alongside existing YAML pipeline support. Each release definition is stored as a `AzureDevops::Release` config item with its full definition JSON. Environment deployments, pre/post-deploy approvals, and deploy steps are emitted as change events with full metadata including variables, artifacts, and trigger reasons.

```yaml
azureDevops:
  - connection: connection://Azure Devops/Flanksource
    projects:
      - Demo1
    pipelines:
      - "adhoc-release"
    releases:
      - "Production Deploy"
      - "Staging *"
    permissions:
      enabled: true
      rateLimit: "12h"
```

| Change Type | Source | Details |
|---|---|---|
| Environment name (e.g. "Production") | Deployment completion | Variables, artifacts, deploy steps, trigger reason |
| `Approved` | Pre/post-deploy approval | Approver identity, comments |
| `Rejected` | Pre/post-deploy rejection | Approver identity, comments, severity: high |

Pipeline YAML definitions are now fetched from the repository and stored as the config body with `format: yaml`, replacing the previous JSON config map approach.

([9bf9c22](https://github.com/flanksource/config-db/commit/9bf9c222), [820d849](https://github.com/flanksource/config-db/commit/820d8491), [bf429f2](https://github.com/flanksource/config-db/commit/bf429f2f), [fce2a7b](https://github.com/flanksource/config-db/commit/fce2a7be), [3299ad7](https://github.com/flanksource/config-db/commit/3299ad73))

---

### External Entity Tracking & RBAC Access <sup>Mission Control</sup>

A new external entity system tracks users, groups, and roles from external systems (AWS, Azure, GCP, Kubernetes) and links them to config items. Entities are matched across scrapers using **aliases** — enabling the same real-world user to be identified under multiple names (email, ARN, username) from different sources.

Two access types are supported:
- **`config_access`** — static RBAC permission grants (e.g. "user X has role Y on resource Z")
- **`access_logs`** — time-stamped audit events (e.g. "user X assumed role Y at time T")

```json
{
  "external_users": [
    { "name": "John Doe", "user_type": "human", "aliases": ["john-doe", "jdoe@example.com"] }
  ],
  "external_groups": [
    { "name": "Administrators", "group_type": "security", "aliases": ["admins-group"] }
  ],
  "external_roles": [
    { "name": "Admin", "role_type": "builtin", "aliases": ["admin-role"] }
  ]
}
```

| Source | Entities Extracted | Access Type |
|---|---|---|
| Kubernetes RBAC | Roles, Users, ServiceAccounts, Groups | `config_access` |
| Azure Active Directory | Users, Groups, App Roles | `config_access` |
| GCP IAM | Users, Roles | `config_access` |
| AWS CloudTrail | Users (via AssumeRole) | `access_logs` |
| Azure DevOps | Pipeline deployers, approvers | `access_logs` |
| HTTP/Transformation | Any via CEL expressions | Both |

When entities with overlapping aliases are discovered, they are automatically merged — a PostgreSQL stored procedure remaps all FK references (`config_access`, `access_logs`) to the winning entity and soft-deletes duplicates. Transitive merges (A→B→C) are handled in a single pass.

([9346ee9](https://github.com/flanksource/config-db/commit/9346ee9d), [dcff3ba](https://github.com/flanksource/config-db/commit/dcff3ba6), [aa1ea9f](https://github.com/flanksource/config-db/commit/aa1ea9fd), [6bf4c62](https://github.com/flanksource/config-db/commit/6bf4c622), [36f6d48](https://github.com/flanksource/config-db/commit/36f6d487), [41d3cf6](https://github.com/flanksource/config-db/commit/41d3cf6e))

---

### HTTP Pagination

The HTTP scraper supports paginated API responses using CEL expressions for page traversal and result accumulation. Automatic retry on HTTP 429 with `Retry-After` header support is included.

```yaml
http:
  - url: "https://graph.microsoft.com/v1.0/groups?$top=100"
    connection: connection://monitoring/azure-bearer
    pagination:
      nextPageExpr: >
        "@odata.nextLink" in response.body && response.body["@odata.nextLink"] != ""
        ? string(response.body["@odata.nextLink"]) : ""
      maxPages: 5
    type: Azure::Group
    transform:
      expr: |
        dyn(config).map(group, { ... })
```

| Mode | Behavior |
|---|---|
| Merge (default) | Accumulates all pages into a single array, auto-extracts `value`/`items` keys from OData responses |
| `perPage: true` | Yields one `ScrapeResult` per page |
| `reduceExpr` | Custom CEL accumulator: `acc + page.value` |
| `maxPages` | Cap on total pages fetched |
| `delay` | Throttle between page requests (e.g. `500ms`) |

([45e8397](https://github.com/flanksource/config-db/commit/45e83974))

---

### Change Actions: move-up, copy-up, copy, move

Change mapping rules now support four directional actions that route changes to related config items, enabling scenarios like surfacing pod-level events on their parent deployment.

```yaml
changes:
  mapping:
    # Surface deployment changes on the parent project
    - filter: 'change_type == "Deployment"'
      action: move-up
      ancestor_type: AzureDevops::Project

    # Duplicate diff changes to selector-resolved targets
    - filter: 'change_type == "diff"'
      action: copy
      target:
        type: Kubernetes::Deployment
        name:
          expr: change.summary
```

| Action | Original Change | Target Change |
|---|---|---|
| `move-up` | Reassigned to ancestor | — |
| `copy-up` | Kept on original | Copy created on ancestor |
| `move` | Assigned to first target | Copies to remaining targets |
| `copy` | Kept on original | Copies to all targets |

`move-up` / `copy-up` walk the `parent_id` chain up to 50 levels. `copy` / `move` resolve targets via relationship selectors.

([7a7a485](https://github.com/flanksource/config-db/commit/7a7a4855), [f9e029c](https://github.com/flanksource/config-db/commit/f9e029c3))

---

### Exec Scraper with Connection & Git Checkout

A new `exec` scraper runs arbitrary scripts with connection credential injection and optional git repository checkout. The working directory is set to the cloned repo root, enabling scripts that operate on repository files.

```yaml
exec:
  - name: $.reg_no
    type: Custom::Config
    id: $.reg_no
    script: |
      #!/bin/bash
      cat fixtures/data/car.json
    checkout:
      url: https://github.com/flanksource/config-db
      branch: main
    connections:
      aws:
        connection: connection://aws/production
    setup:
      # Install dependencies before script execution
      script: pip install -r requirements.txt
```

Supported connection types: `aws`, `gcp`, `azure`, `kubernetes`. Output parsing: JSON → YAML → plain text, with arrays yielding one result per item.

([6c22a7b](https://github.com/flanksource/config-db/commit/6c22a7b7), [37fc01b](https://github.com/flanksource/config-db/commit/37fc01bb))

---

### GitHub & OpenSSF Scrapers

Scrape GitHub repositories for security alerts (Dependabot, code scanning, secret scanning) and OpenSSF Scorecard assessments. Each repository is stored as a `GitHub::Repository` config item with security analyses attached.

```yaml
github:
  - security: true
    openssf: true
    repositories:
      - owner: flanksource
        repo: canary-checker
```

| Property | Source | Details |
|---|---|---|
| Critical/High/Medium/Low Alerts | Dependabot + code scanning + secret scanning | Filterable by severity, state, maxAge |
| OpenSSF Score | api.securityscorecards.dev | 24h cache, 0-10 scale |
| OpenSSF Badge | Scorecard | Link to scorecard badge |
| Health | Computed | `>=7.0` + no critical = healthy, `<4.0` or critical = unhealthy |

Code scanning alerts that overlap with OpenSSF check names are deduplicated.

([7400158](https://github.com/flanksource/config-db/commit/74001586))

---

### ScrapePlugin: Retention & Locations

`ScrapePlugin` is a new CRD that applies cross-cutting concerns to scrapers without modifying the scraper config itself. Plugins support change exclusion/mapping, retention policies, relationship rules, properties, locations, and aliases.

```yaml
apiVersion: configs.flanksource.com/v1
kind: ScrapePlugin
metadata:
  name: k8s-locations-and-aliases
spec:
  aliases:
    - type: Kubernetes::Cluster
      values:
        - cluster://kubernetes/{{.name}}
    - type: Kubernetes::Namespace
      values:
        - namespace://kubernetes/{{tags.cluster}}/{{.name}}

  locations:
    - type: Kubernetes::*
      values:
        - cluster://kubernetes/{{.tags.cluster}}
    - type: Kubernetes::*
      filter: has(tags.namespace)
      values:
        - namespace://kubernetes/{{tags.cluster}}/{{.tags.namespace}}

  retention:
    changes:
      - name: diff
        age: 30d
        count: 100
    staleItemAge: "7d"
```

Plugins are merged into each scraper's spec at runtime via `ApplyPlugins()`.

([68a4f7b](https://github.com/flanksource/config-db/commit/68a4f7b5), [68be2ad](https://github.com/flanksource/config-db/commit/68be2ad8))

---

### System Scraper: Playbooks, People & Teams <sup>Mission Control</sup>

The system scraper now discovers Mission Control's own internal entities — playbooks, people, teams, and job histories — and maps them to config items and external entities for unified visibility.

| Entity | Config Type | External Entity |
|---|---|---|
| Agents | `MissionControl::Agent` | — |
| Playbooks | `MissionControl::Playbook` | — |
| Job histories | `MissionControl::Job` | — |
| People | — | `ExternalUser` with alias `people:<id>` |
| Teams | — | `ExternalGroup` with alias `team:<id>` |
| Playbook roles | — | `ExternalRole` for `playbook:run` / `playbook:approve` |

([1292dbf](https://github.com/flanksource/config-db/commit/1292dbfb))

---

## Features

- **feat:** add `last_scrape_summary` to template environment ([a218ae8](https://github.com/flanksource/config-db/commit/a218ae81)) [#1924](https://github.com/flanksource/config-db/pull/1924)
- **feat:** scraper run now detached from HTTP request context but still waits ([75f9ba0](https://github.com/flanksource/config-db/commit/75f9ba0d)) [#1826](https://github.com/flanksource/config-db/pull/1826)
- **feat:** store orphaned changes config IDs in the summary ([006de2a](https://github.com/flanksource/config-db/commit/006de2a3))
- **feat:** CloudWatch LogStream ARN derivation from CloudTrail events ([adafbd0](https://github.com/flanksource/config-db/commit/adafbd02))
- **feat:** change ignored tracking by type vs by action ([53a366c](https://github.com/flanksource/config-db/commit/53a366c1))
- **feat:** slim build tag and CLI improvements ([e990d3c](https://github.com/flanksource/config-db/commit/e990d3ce))
- **feat:** support kubernetes connection reference on Kubernetes scraper ([25d2274](https://github.com/flanksource/config-db/commit/25d22749))
- **feat(azure):** Azure logs scraping ([9297bba](https://github.com/flanksource/config-db/commit/9297bba6)) [#1940](https://github.com/flanksource/config-db/pull/1940)
- **feat(cli):** add LogBlock rendering, ensureScraper, and save summary ([bd20434](https://github.com/flanksource/config-db/commit/bd20434a))
- **feat(db):** emit ConfigChange on permission grant/revoke ([41d3cf6](https://github.com/flanksource/config-db/commit/41d3cf6e))
- **feat(test):** unified fixture framework with e2e DB test runner ([6a73ebb](https://github.com/flanksource/config-db/commit/6a73ebb7))

## Bug Fixes

- **fix(kubernetes):** use dynamic informer factory to support CRD watches ([0f5ab69](https://github.com/flanksource/config-db/commit/0f5ab698))
- **fix:** add adaptive retry for CloudTrail API throttling ([b9428fd](https://github.com/flanksource/config-db/commit/b9428fd0))
- **fix(azure):** always emit pipeline config item even when all runs are skipped ([7f47574](https://github.com/flanksource/config-db/commit/7f47574b))
- **fix(azure):** fetch pipeline YAML definition and remove run params from labels ([3299ad7](https://github.com/flanksource/config-db/commit/3299ad73))
- **fix(db):** skip stale deletion during incremental scrapes ([6f6b6f0](https://github.com/flanksource/config-db/commit/6f6b6f0b))
- **fix(changes):** ignore volatile k8s metadata in fingerprinting ([0c08ce3](https://github.com/flanksource/config-db/commit/0c08ce3a))
- **fix:** deduplicate access logs before batch insert ([f2ddd06](https://github.com/flanksource/config-db/commit/f2ddd06b))
- **fix:** FK errors on external entity upsert ([2048700](https://github.com/flanksource/config-db/commit/20487001), [a9fec4f](https://github.com/flanksource/config-db/commit/a9fec4f1))
- **fix:** entity-only items should not produce config items ([36715c0](https://github.com/flanksource/config-db/commit/36715c09))
- **fix:** ADO pipeline ID uniqueness ([ab16f37](https://github.com/flanksource/config-db/commit/ab16f37f))
- **fix:** restore kubernetes scraper tags lost in refactor ([eb54dce](https://github.com/flanksource/config-db/commit/eb54dce0))
- **fix:** use correct primary key columns in config_access_logs OnConflict ([deb6a87](https://github.com/flanksource/config-db/commit/deb6a875)) [#1877](https://github.com/flanksource/config-db/pull/1877)
- **fix:** ignore linked config_items when deleting config scraper ([2bed7b5](https://github.com/flanksource/config-db/commit/2bed7b55))
- **fix:** kubernetes file scraper kubeconfig default ([0c28734](https://github.com/flanksource/config-db/commit/0c28734e))
- **fix:** AWS EFS ARN as aliases ([2fc6879](https://github.com/flanksource/config-db/commit/2fc68790))
- **fix:** extract CloudTrail event resources ([5542c7e](https://github.com/flanksource/config-db/commit/5542c7e3))
- **fix:** add external_change_id to fingerprinting logic ([d3dfbe0](https://github.com/flanksource/config-db/commit/d3dfbe08)) [#1797](https://github.com/flanksource/config-db/pull/1797)
- **fix:** consider first change while deduping changes ([4ea7ce0](https://github.com/flanksource/config-db/commit/4ea7ce0f)) [#1798](https://github.com/flanksource/config-db/pull/1798)
- **fix:** handle config scraper deletion/un-deletion and cleanup ([47f0efd](https://github.com/flanksource/config-db/commit/47f0efd8)) [#1749](https://github.com/flanksource/config-db/pull/1749)
- **fix:** make tempcache store thread safe ([ea72465](https://github.com/flanksource/config-db/commit/ea72465f)) [#1733](https://github.com/flanksource/config-db/pull/1733)
- **fix:** azure devops pipelines with non-string template vars ([de8439c](https://github.com/flanksource/config-db/commit/de8439cf))
- **fix(gcp):** handle missing ID for Servicenetworking::Connection type ([46f5973](https://github.com/flanksource/config-db/commit/46f59731))
- **fix:** concurrent r/w error in argo hook ([b53367e](https://github.com/flanksource/config-db/commit/b53367e4))

## Refactors

- **chore:** make status.incremental a pointer ([bf01aac](https://github.com/flanksource/config-db/commit/bf01aac4)) [#2010](https://github.com/flanksource/config-db/pull/2010)
- **chore:** update CRD status for incremental scrape ([169250e](https://github.com/flanksource/config-db/commit/169250e4)) [#1986](https://github.com/flanksource/config-db/pull/1986)
- **chore:** config access batching + reduce k8s records ([81d70b8](https://github.com/flanksource/config-db/commit/81d70b8b)) [#1970](https://github.com/flanksource/config-db/pull/1970)
- **chore:** add logs, HAR and config data in HTML output ([2a0727c](https://github.com/flanksource/config-db/commit/2a0727c2)) [#1946](https://github.com/flanksource/config-db/pull/1946)
- **chore:** link EKS cluster to EC2 instance ([012d9d1](https://github.com/flanksource/config-db/commit/012d9d1b)) [#1825](https://github.com/flanksource/config-db/pull/1825)
- **chore:** increment count column in config access logs ([6dc15a8](https://github.com/flanksource/config-db/commit/6dc15a80)) [#1912](https://github.com/flanksource/config-db/pull/1912)
- **refactor(azure-devops):** migrate from resty to commons/http ([8c90051](https://github.com/flanksource/config-db/commit/8c900517))

---

## Dependency Changes

### [duty](https://github.com/flanksource/duty) v1.0.1037 → v1.0.1230

### [commons](https://github.com/flanksource/commons) v1.40.0 → v1.48.3

### [gomplate](https://github.com/flanksource/gomplate) v3.24.60 → v3.24.74

### [is-healthy](https://github.com/flanksource/is-healthy) v1.0.78 → v1.0.86

### [artifacts](https://github.com/flanksource/artifacts) v1.0.14 → v1.0.21

### [kopper](https://github.com/flanksource/kopper) v1.0.11 → v1.0.18

### New Dependencies

| Package | Version |
|---|---|
| [clicky](https://github.com/flanksource/clicky) | v1.19.0 |
| [deps](https://github.com/flanksource/deps) | v1.0.24 |
| [sandbox-runtime](https://github.com/flanksource/sandbox-runtime) | v1.0.1 |
