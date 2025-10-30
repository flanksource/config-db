# OpenSSF Scorecard Scraper Test Fixtures

This directory contains JSON fixture files for testing the OpenSSF Scorecard scraper with mocked API responses.

## Files

### `scorecard-high-score.json`
Sample scorecard with excellent security posture (Score: 8.2/10).

**Repository:** `flanksource/mission-control`

**Highlights:**
- Strong code review process (10/10)
- SAST tools enabled (CodeQL)
- Branch protection configured
- Active maintenance
- Dependency updates automated (Dependabot)

**Failing Checks:**
- CII Best Practices badge (0/10)
- Fuzzing (0/10)
- Signed releases (0/10)

**Use Case:** Testing healthy repository status calculation

### `scorecard-medium-score.json`
Sample scorecard with moderate security gaps (Score: 5.4/10).

**Repository:** `flanksource/config-db`

**Highlights:**
- Active maintenance (10/10)
- License present (10/10)
- No known vulnerabilities (10/10)

**Critical Failures:**
- Poor code review coverage (2/10)
- Weak branch protection (3/10)
- No SAST tools (0/10)
- No dependency update tool (0/10)
- Token permissions too broad (0/10)

**Use Case:** Testing warning status for repositories with security gaps

### `scorecard-low-score.json`
Sample scorecard with severe security issues (Score: 2.8/10).

**Repository:** `example/vulnerable-project`

**Critical Failures (score 0):**
- No code review process
- No branch protection
- No CI tests
- Binary artifacts in source
- Dangerous workflows
- No SAST tools
- No security policy
- Repository unmaintained
- Dependencies not pinned
- Excessive token permissions

**Passing Checks:**
- License present (10/10)
- No known vulnerabilities (10/10)

**Use Case:** Testing unhealthy status for vulnerable repositories

## Scorecard Checks Covered

All fixtures include results for these 19 OpenSSF Scorecard checks:

1. **Binary-Artifacts** - Executable binaries in source
2. **Branch-Protection** - Branch protection rules
3. **CI-Tests** - Continuous integration testing
4. **CII-Best-Practices** - CII Best Practices badge
5. **Code-Review** - PR review requirements
6. **Contributors** - Multi-organization contributors
7. **Dangerous-Workflow** - GitHub Actions security
8. **Dependency-Update-Tool** - Dependabot/Renovate
9. **Fuzzing** - Fuzzing tools
10. **License** - License file presence
11. **Maintained** - Active maintenance
12. **Packaging** - Package publishing
13. **Pinned-Dependencies** - Dependency pinning
14. **SAST** - Static analysis tools
15. **Security-Policy** - Security policy file
16. **Signed-Releases** - Release signing
17. **Token-Permissions** - Workflow permissions
18. **Vulnerabilities** - Known vulnerabilities
19. **Webhooks** - Webhook security

## Compliance Framework Mappings

Each check in the fixtures can be mapped to compliance frameworks using the `compliance.go` module:

- **SOC 2** Trust Service Criteria
- **NIST SSDF** Secure Software Development Framework
- **CIS Controls** Critical Security Controls

## Usage in Tests

```go
import (
    "encoding/json"
    "os"
    "testing"
)

func TestHighScoreScorecard(t *testing.T) {
    data, err := os.ReadFile("testdata/scorecard-high-score.json")
    if err != nil {
        t.Fatal(err)
    }

    var scorecard ScorecardResponse
    if err := json.Unmarshal(data, &scorecard); err != nil {
        t.Fatal(err)
    }

    // Verify score
    if scorecard.Score != 8.2 {
        t.Errorf("expected score 8.2, got %.1f", scorecard.Score)
    }

    // Use in mock HTTP server
    // ...
}
```

## Health Status Expectations

Based on the scraper's health calculation logic:

| Fixture | Score | Critical Failures | Expected Health |
|---------|-------|-------------------|----------------|
| high-score | 8.2 | None | Healthy ✓ |
| medium-score | 5.4 | Code-Review, SAST, Token-Permissions | Unhealthy ✗ |
| low-score | 2.8 | Code-Review, Branch-Protection, SAST, Token-Permissions, Dangerous-Workflow | Unhealthy ✗ |

**Critical Checks** (failures trigger unhealthy status):
- Code-Review
- SAST
- Token-Permissions
- Dangerous-Workflow
- Branch-Protection

## API Endpoint

These fixtures correspond to the OpenSSF Scorecard public API:

**Endpoint:** `GET https://api.securityscorecards.dev/projects/github.com/{org}/{repo}`

**Authentication:** None required (public API)

## Documentation

- [OpenSSF Scorecard Project](https://github.com/ossf/scorecard)
- [Scorecard Checks Documentation](https://github.com/ossf/scorecard/blob/main/docs/checks.md)
- [Scorecard REST API](https://api.securityscorecards.dev)
- [DataSheet Security Reference](https://github.com/flanksource/website-migration/tree/main/mission-control/branding/datasheet)
