package trivy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/flanksource/commons/deps"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

const (
	trivyBinPath = ".bin"
)

type Scanner struct {
}

func (t Scanner) CanScrape(config v1.ConfigScraper) bool {
	return len(config.Trivy) > 0
}

func (t Scanner) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {
	var results v1.ScrapeResults

	for i, config := range configs.Trivy {
		if config.IsEmpty() {
			logger.Debugf("Trivy config [%d] is empty. Skipping ...", i+1)
			continue
		}

		// Ensure that trivy binary is available
		if err := deps.InstallDependency("trivy", config.Version, ".bin"); err != nil {
			var result = v1.NewScrapeResult(config.BaseScraper)
			results = append(results, result.Errorf("failed to install trivy: %w", err))
			continue
		}
		trivyBinPath := fmt.Sprintf("%s/trivy", trivyBinPath)

		if config.Kubernetes != nil {
			var result = v1.NewScrapeResult(config.BaseScraper)
			output, err := runCommand(ctx, trivyBinPath, config.GetK8sArgs())
			if err != nil {
				results = append(results, result.SetError(err))
				continue
			}

			var trivyResponse TrivyResponse
			if err := json.Unmarshal(output, &trivyResponse); err != nil {
				results = append(results, result.Errorf("failed to unmarshal trivy output: %w", err))
				continue
			}

			for _, resource := range trivyResponse.Vulnerabilities {
				for _, result := range resource.Results {
					for _, vulnerability := range result.Vulnerabilities {
						analysis, err := utils.ToJSONMap(vulnerability)
						if err != nil {
							logger.Errorf("failed to extract analysis: %v", err)
						}

						results.Add(v1.ScrapeResult{
							AnalysisResult: &v1.AnalysisResult{
								ConfigType:   fmt.Sprintf("Kubernetes::%s", resource.Kind),
								ExternalID:   fmt.Sprintf("Kubernetes/%s/%s/%s", resource.Kind, resource.Namespace, resource.Name),
								Analysis:     analysis,
								AnalysisType: v1.AnalysisTypeSecurity, // It's always security related.
								Analyzer:     fmt.Sprintf("%s [%s]", vulnerability.PkgName, vulnerability.VulnerabilityID),
								Messages:     []string{vulnerability.Description},
								Severity:     mapSeverity(vulnerability.Severity),
								Source:       "Trivy",
								Summary:      vulnerability.Title,
							},
						})
					}
				}
			}
		}
	}

	return results
}

func mapSeverity(severity string) v1.Severity {
	switch severity {
	case "CRITICAL":
		return v1.SeverityCritical
	case "HIGH":
		return v1.SeverityHigh
	case "MEDIUM":
		return v1.SeverityMedium
	case "LOW":
		return v1.SeverityLow
	default:
		return v1.SeverityInfo
	}
}

func runCommand(ctx context.Context, command string, args []string) ([]byte, error) {
	logger.Tracef("Running command: %s %s", command, args)

	cmd := exec.CommandContext(ctx, command, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run command: %s %s (%s): %w", command, args, stderr.String(), err)
	}

	return output, nil
}
