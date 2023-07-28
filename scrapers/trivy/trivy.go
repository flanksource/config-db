package trivy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os/exec"

	"github.com/flanksource/commons/deps"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

const (
	trivyBinPath = ".bin"
)

type Scanner struct {
}

func (t Scanner) CanScrape(config v1.ScraperSpec) bool {
	return len(config.Trivy) > 0
}

func (t Scanner) Scrape(ctx *v1.ScrapeContext, configs v1.ScraperSpec) v1.ScrapeResults {
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
					AnalysisType: models.AnalysisTypeSecurity,
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

					view := map[string]any{
						"title":       template.HTML(mdToHTML("**Title:** " + vulnerability.Title)),
						"description": template.HTML(mdToHTML("**Description:** " + vulnerability.Description)),
						"vuln":        vulnerability,
					}

					var msg bytes.Buffer
					if err := trivyVulnTemplate.Execute(&msg, view); err != nil {
						logger.Errorf("failed to execute trivy template: %v", err)
					} else {
						analysis.Messages = append(analysis.Messages, msg.String())
					}
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
						AnalysisType: models.AnalysisTypeSecurity,
						Analyzer:     misconfiguration.Title,
						Messages:     []string{misconfiguration.Description, misconfiguration.Message},
						Severity:     mapSeverity(misconfiguration.Severity),
						Source:       "Trivy",
						Summary:      misconfiguration.Title,
						Status:       models.AnalysisStatusOpen,
					},
				})
			}
		}
	}

	return results
}

func mapSeverity(severity string) models.Severity {
	switch severity {
	case "CRITICAL":
		return models.SeverityCritical
	case "HIGH":
		return models.SeverityHigh
	case "MEDIUM":
		return models.SeverityMedium
	case "LOW":
		return models.SeverityLow
	default:
		return models.SeverityInfo
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

var trivyVulnTemplate *template.Template

func init() {
	tpl := `
{{.title}}
<b>CVE:</b> <a href="{{.vuln.PrimaryURL}}">{{.vuln.VulnerabilityID}}</a><br>
<b>Installed Version:</b> {{.vuln.InstalledVersion}}<br>
<b>Fixed Version:</b> {{.vuln.FixedVersion}}<br>
{{if .vuln.DataSource}}<b>Data source:</b> <a href="{{.vuln.DataSource.URL}}">{{.vuln.DataSource.Name}}</a><br>{{end}}
{{.description}}
`
	trivyVulnTemplate = template.Must(template.New("trivy").Parse(tpl))
}

func mdToHTML(md string) string {
	extensions := parser.CommonExtensions
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse([]byte(md))

	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	return string(markdown.Render(doc, renderer))
}
