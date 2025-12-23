# Exec Scraper Fixtures

This directory contains example ScrapeConfig YAML files demonstrating various use cases of the exec scraper.

## Fixtures Overview

### `exec-simple.yaml`
Basic example with inline bash script outputting JSON array.
- Demonstrates: Simple script execution, JSON output parsing
- Runtime: Bash

### `exec-aws-connection.yaml`
AWS EC2 inventory using AWS CLI with connection credential injection.
- Demonstrates: AWS connection integration, credential injection
- Runtime: Bash
- Features: AWS CLI usage with automatic credential handling

### `exec-git-checkout.yaml`
Execute script from a Git repository.
- Demonstrates: Git checkout functionality, working directory context
- Runtime: Bash
- Features: Repository cloning, script execution in repo context

### `exec-python.yaml`
Kubernetes node inventory using Python and kubectl.
- Demonstrates: Python runtime with shebang detection, Kubernetes connection
- Runtime: Python 3
- Features: Subprocess execution, Kubernetes credential injection

### `exec-nodejs.yaml`
AWS RDS database inventory using Node.js.
- Demonstrates: Node.js runtime with shebang detection
- Runtime: Node.js
- Features: AWS connection integration

### `exec-env-transform.yaml`
Custom resource inventory with environment variables and transforms.
- Demonstrates: Environment variable injection, secret references, transforms
- Runtime: Bash
- Features: Custom env vars, secret resolution, CEL transforms, relationships

### `exec-yaml-output.yaml`
Cluster information output as YAML (auto-converted to JSON).
- Demonstrates: YAML output parsing
- Runtime: Bash
- Features: YAML to JSON conversion

### `exec-comprehensive.yaml`
Comprehensive example showing multiple exec configs with various features.
- Demonstrates: All major features in one config
- Runtimes: Bash, Python, Node.js
- Features:
  - AWS/Kubernetes connections
  - Git checkout
  - Environment variables
  - Multiple script types
  - Transforms and relationships

## Key Features Demonstrated

### 1. Script Execution
- **Inline scripts**: Scripts defined directly in YAML
- **Shebang detection**: Automatic runtime selection based on `#!/path/to/interpreter`
- **Multiple runtimes**: Bash, Python, Node.js, PowerShell

### 2. Connection Support
- **AWS**: Automatic credential injection (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, etc.)
- **GCP**: GCP credential configuration
- **Azure**: Azure credential injection
- **Kubernetes**: KUBECONFIG and context setup

### 3. Git Integration
- **Repository checkout**: Clone repos before script execution
- **Branch selection**: Specify branch/tag/commit
- **Working directory**: Scripts execute in cloned repo context
- **Private repos**: Support for authenticated access via connections

### 4. Output Formats
- **JSON arrays**: Multiple config items from single script
- **JSON objects**: Single config item
- **YAML**: Automatic conversion to JSON
- **Plain text**: Fallback for non-structured output

### 5. Environment Variables
- **Custom env vars**: Define variables in YAML
- **Secret references**: Load values from Kubernetes secrets
- **Connection injection**: Automatic credential environment variables

### 6. Transformations
- **Field extraction**: JSONPath-based ID, name, type extraction
- **Exclusions**: Remove sensitive or unnecessary fields
- **Relationships**: Define relationships to other config items
- **CEL expressions**: Complex transformations using CEL

## Usage Patterns

### Pattern 1: Cloud Inventory
Use exec scraper to run cloud CLI tools (AWS CLI, gcloud, az) with automatic credential injection:

```yaml
exec:
  - name: cloud-inventory
    script: |
      #!/bin/bash
      aws ec2 describe-instances --output json
    connections:
      aws:
        connection: connection://aws/prod
```

### Pattern 2: Custom Scripts from Git
Execute inventory/audit scripts stored in version-controlled repositories:

```yaml
exec:
  - name: security-audit
    script: ./scripts/audit.sh
    checkout:
      url: https://github.com/org/security-scripts
      branch: main
```

### Pattern 3: Data Transformation
Use Python/Node.js for complex data processing before outputting config items:

```yaml
exec:
  - name: processed-metrics
    script: |
      #!/usr/bin/env python3
      # Fetch, process, and output structured data
      import json
      # ... processing logic ...
      print(json.dumps(results))
```

### Pattern 4: Multi-Source Aggregation
Combine data from multiple sources in a single script:

```yaml
exec:
  - name: aggregated-inventory
    script: |
      #!/bin/bash
      # Fetch from API, augment with AWS data, output combined
      curl -s $API_URL | jq -s 'add'
    env:
      - name: API_URL
        value: https://api.example.com/inventory
```

## Best Practices

1. **Use shebang lines**: Always include `#!/path/to/interpreter` for proper runtime detection
2. **Output structured data**: Prefer JSON or YAML output for proper parsing
3. **Leverage connections**: Use connection credential injection instead of hardcoding credentials
4. **Error handling**: Scripts should exit with non-zero code on errors
5. **Idempotency**: Scripts should be safe to run repeatedly
6. **Performance**: Consider script execution time when setting schedule intervals
7. **Git checkout**: Use for versioned, auditable scripts that can be reviewed via PRs

## Scheduling

All examples use cron-like scheduling:
- `@every 5m` - Every 5 minutes
- `@every 10m` - Every 10 minutes
- `@every 1h` - Every hour
- `0 */4 * * *` - Every 4 hours (cron syntax)

Choose intervals based on:
- Data freshness requirements
- API rate limits
- Script execution time
- System load considerations
