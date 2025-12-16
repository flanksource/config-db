# GitHub Security Scraper Test Fixtures

This directory contains JSON fixture files for testing the GitHub Security scraper with mocked API responses.

## Files

### `dependabot-alerts.json`
Sample Dependabot vulnerability alerts.

**Contents:**
- 2 alerts (critical and high severity)
- lodash prototype pollution (CVE-2020-8203, CVSS 9.1)
- axios CSRF vulnerability (CVE-2023-45857, CVSS 8.1)

**Fields:**
- GHSA and CVE identifiers
- Severity levels and CVSS scores
- CWE mappings
- Vulnerable version ranges
- Package ecosystem information

### `code-scanning-alerts.json`
Sample code scanning alerts from CodeQL SAST tool.

**Contents:**
- 2 alerts (high and medium severity)
- SQL injection vulnerability (js/sql-injection)
- Path injection vulnerability (js/path-injection)

**Fields:**
- Alert numbers and states
- File locations with line/column numbers
- Rule IDs and descriptions
- Tool information (CodeQL version)
- Commit SHAs

### `secret-scanning-alerts.json`
Sample secret scanning alerts for exposed credentials.

**Contents:**
- 2 alerts (both open)
- GitHub Personal Access Token (active validity)
- AWS Access Key ID (unknown validity)

**Fields:**
- Secret types and display names
- Redacted secret values
- Validity status (active/unknown/inactive)
- Push protection status

### `security-advisories.json`
Sample published security advisories.

**Contents:**
- 1 critical severity advisory
- SQL injection in authentication module
- CVE-2024-12345 with CVSS 10.0

**Fields:**
- GHSA and CVE identifiers
- Vulnerability descriptions
- Affected packages and versions
- CVSS scores and CWE mappings
- Credits and publishing information

## Usage in Tests

```go
import (
    "encoding/json"
    "os"
    "testing"
)

func TestDependabotAlerts(t *testing.T) {
    data, err := os.ReadFile("testdata/security/dependabot-alerts.json")
    if err != nil {
        t.Fatal(err)
    }

    var alerts []*github.DependabotAlert
    if err := json.Unmarshal(data, &alerts); err != nil {
        t.Fatal(err)
    }

    // Use alerts in mock HTTP server
    // ...
}
```

## API Endpoints

These fixtures correspond to the following GitHub REST API endpoints:

- Dependabot: `GET /repos/{owner}/{repo}/dependabot/alerts`
- Code Scanning: `GET /repos/{owner}/{repo}/code-scanning/alerts`
- Secret Scanning: `GET /repos/{owner}/{repo}/secret-scanning/alerts`
- Advisories: `GET /repos/{owner}/{repo}/security-advisories`

## Documentation

- [GitHub REST API - Dependabot](https://docs.github.com/en/rest/dependabot/alerts)
- [GitHub REST API - Code Scanning](https://docs.github.com/en/rest/code-scanning)
- [GitHub REST API - Secret Scanning](https://docs.github.com/en/rest/secret-scanning)
- [GitHub REST API - Security Advisories](https://docs.github.com/en/rest/security-advisories)
