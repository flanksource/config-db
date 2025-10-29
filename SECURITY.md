# Security Policy

## Reporting a Vulnerability

If you discover any security vulnerabilities within this project, please report them to our team immediately. We appreciate your help in making this project more secure for everyone.

To report a vulnerability, please follow these steps:

1. **Email**: Send an email to our security team at [security@flanksource.com](mailto:security@flanksource.com) with a detailed description of the vulnerability.
2. **Subject Line**: Use the subject line "Security Vulnerability Report" to ensure prompt attention.
3. **Information**: Provide as much information as possible about the vulnerability, including steps to reproduce it and any supporting documentation or code snippets.
4. **Confidentiality**: We prioritize the confidentiality of vulnerability reports. Please avoid publicly disclosing the issue until we have had an opportunity to address it.

Our team will respond to your report as soon as possible and work towards a solution. We appreciate your responsible disclosure and cooperation in maintaining the security of this project.

Thank you for your contribution to the security of this project!

**Note:** This project follows responsible disclosure practices.

## Known Vulnerabilities and Accepted Risks

### AWS SDK Go v1 - S3 Crypto Vulnerabilities

**Status**: Accepted Risk (Not Exploitable in this codebase)

#### Vulnerability Details

- **GO-2022-0635**: In-band key negotiation issue in AWS S3 Crypto SDK
  - More info: https://pkg.go.dev/vuln/GO-2022-0635
  - Found in: `github.com/aws/aws-sdk-go@v1.55.7`
  - Fixed in: N/A (AWS SDK v1 is in maintenance mode)

- **GO-2022-0646**: CBC padding oracle issue in AWS S3 Crypto SDK
  - More info: https://pkg.go.dev/vuln/GO-2022-0646
  - Found in: `github.com/aws/aws-sdk-go@v1.55.7`
  - Fixed in: N/A (AWS SDK v1 is in maintenance mode)

#### Why This is Accepted

1. **Not Called by Our Code**: Analysis with `govulncheck` confirms that our codebase does not call the vulnerable S3 crypto functions (`s3crypto.EncryptionClient`, `s3crypto.DecryptionClient`)

2. **Indirect Dependency**: AWS SDK v1 is pulled in as an indirect dependency by:
   - `flanksource/artifacts`
   - `flanksource/duty`
   - `opensearch-project/opensearch-go/v2`
   - `uber/athenadriver`
   - `gocloud.dev`

3. **No Fix Available**: AWS has not provided patches for these vulnerabilities in SDK v1, as it's in maintenance mode. The recommendation is to migrate to AWS SDK v2.

4. **Using AWS SDK v2**: Our direct AWS integrations use AWS SDK v2 (`github.com/aws/aws-sdk-go-v2`), which is not affected by these vulnerabilities.

#### Verification

Run the following command to verify these vulnerabilities are not exploitable:

```bash
govulncheck ./...
```

Expected output should indicate:
```
Your code is affected by 0 vulnerabilities.
This scan also found 0 vulnerabilities in packages you import and 2
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.
```

#### Mitigation Plan

Long-term mitigation involves:
1. Monitoring for updates to indirect dependencies that might remove AWS SDK v1
2. Evaluating alternatives to dependencies that still require AWS SDK v1
3. Working with upstream `flanksource` libraries to migrate to AWS SDK v2

#### Last Reviewed

Date: 2025-10-29
Reviewed by: Security scan automation
Next review: When indirect dependencies are updated

#### Configuration Files

The following files suppress these vulnerabilities in security scanning tools:

- **`.trivyignore`** - Trivy vulnerability scanner ignore rules
- **`.osv-scanner.toml`** - OSV-Scanner (used by OpenSSF Scorecard) ignore configuration
- **`.github/workflows/scorecard.yml`** - SARIF filtering to remove accepted vulnerabilities before upload

#### GitHub Security Tab

If these vulnerabilities still appear in GitHub Security → Code scanning alerts:

1. Navigate to **Security** → **Code scanning** in your repository
2. Find alerts for GO-2022-0635 and GO-2022-0646
3. Click on each alert
4. Click **Dismiss alert** → Select "Won't fix"
5. Add comment: "Not exploitable - S3 crypto functions not used. See SECURITY.md"

Alternatively, these will be automatically filtered by the Scorecard workflow's SARIF filtering step.
