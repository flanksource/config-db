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

			results.Add(getAnalysis(trivyResponse)...)
		}
	}

	return results
}

// getAnalysis returns the ScrapeResults obtained by extracting the analysis from the TrivyResponse vulnerabilities.
func getAnalysis(trivyResponse TrivyResponse) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, resource := range trivyResponse.Vulnerabilities {
		for _, result := range resource.Results {
			for pkg, vulnerabilities := range result.Vulnerabilities.GroupByPkg() {
				analysis := &v1.AnalysisResult{
					AnalysisType: v1.AnalysisTypeSecurity,
					Analyzer:     pkg,
					ConfigType:   fmt.Sprintf("Kubernetes::%s", resource.Kind),
					ExternalID:   fmt.Sprintf("Kubernetes/%s/%s/%s", resource.Kind, resource.Namespace, resource.Name),
					Source:       "Trivy",
					Summary:      pkg,
				}

				analysis.Analysis = make(map[string]any)
				for _, vulnerability := range vulnerabilities {
					vulnerabilityJSON, err := utils.ToJSONMap(vulnerability)
					if err != nil {
						logger.Errorf("failed to marshall analysis: %v", err)
					} else {
						analysis.Analysis[vulnerability.Title] = vulnerabilityJSON
					}

					if v1.IsMoreSevere(mapSeverity(vulnerability.Severity), analysis.Severity) {
						analysis.Severity = mapSeverity(vulnerability.Severity)
					}

					analysis.Messages = append(analysis.Messages, vulnerability.Description)
				}

				results.Add(v1.ScrapeResult{AnalysisResult: analysis})
			}
		}
	}

	for _, resource := range trivyResponse.Misconfigurations {
		for _, result := range resource.Results {
			for _, misconfiguration := range result.Misconfigurations {
				misconfigurationJSON, err := utils.ToJSONMap(misconfiguration)
				if err != nil {
					logger.Errorf("failed to marshall misconfiguration: %v", err)
				}

				results.Add(v1.ScrapeResult{
					AnalysisResult: &v1.AnalysisResult{
						ConfigType:   fmt.Sprintf("Kubernetes::%s", resource.Kind),
						ExternalID:   fmt.Sprintf("Kubernetes/%s/%s/%s", resource.Kind, resource.Namespace, resource.Name),
						Analysis:     misconfigurationJSON,
						AnalysisType: v1.AnalysisTypeSecurity,
						Analyzer:     misconfiguration.Title,
						Messages:     []string{misconfiguration.Description, misconfiguration.Message},
						Severity:     mapSeverity(misconfiguration.Severity),
						Source:       "Trivy",
						Summary:      misconfiguration.Title,
					},
				})
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
