package trivy

import (
	"encoding/json"
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
)

type Scanner struct {
}

func (t Scanner) CanScrape(config v1.ConfigScraper) bool {
	return true // TODO:
}

func (t Scanner) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {
	// TODO: Ensure that trivy binary is available

	var results v1.ScrapeResults

	for _, config := range configs.Trivy {
		if config.Kubernetes != nil {
			var result = v1.NewScrapeResult(config.BaseScraper)
			output, err := runCommand(ctx, "/home/gunners/Downloads/trivy/trivy", config.GetKubernetesArgs())
			if err != nil {
				results = append(results, result.Errorf("failed to run trivy: %w", err))
				continue
			}

			var trivyResponse TrivyResponse
			if err := json.Unmarshal(output, &trivyResponse); err != nil {
				results = append(results, result.Errorf("failed to unmarshal trivy output: %w", err))
				continue
			}

			for _, vulnerability := range trivyResponse.Vulnerabilities {
				for _, result := range vulnerability.Results {
					for _, vulnerabilityDetail := range result.Vulnerabilities {
						results.Add(v1.ScrapeResult{
							// TODO: complete this mapping
							AnalysisResult: &v1.AnalysisResult{
								ExternalType: fmt.Sprintf("Kubernetes::%s", vulnerability.Kind),
								ExternalID:   fmt.Sprintf("Kubernetes/%s/%s/%s", vulnerability.Kind, vulnerability.Namespace, vulnerability.Name),
								Analyzer:     "trivy",
								Summary:      vulnerabilityDetail.Title,
							},
						})
					}
				}
			}
		}
	}

	return results
}
