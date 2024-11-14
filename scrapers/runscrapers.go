package scrapers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/scrapers/processors"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// RunK8sObjectScraper extracts & saves the given kubernetes object.
func RunK8sObjectScraper(ctx api.ScrapeContext, config v1.Kubernetes, namespace, name string, gvk schema.GroupVersionKind) ([]v1.ScrapeResult, error) {
	// create the dynamic client for the gvk

	// get the object
	objs := []*unstructured.Unstructured{}

	// run the scraper
	var results v1.ScrapeResults
	var scraper kubernetes.KubernetesScraper
	res := scraper.IncrementalScrape(ctx, config, objs)
	for i := range res {
		scraped := processScrapeResult(ctx, res[i])
		results = append(results, scraped...)
	}

	return results, nil
}

// RunK8sObjectsScraper extracts & saves the given kubernetes objects.
func RunK8sObjectsScraper(ctx api.ScrapeContext, config v1.Kubernetes, objs []*unstructured.Unstructured) ([]v1.ScrapeResult, error) {
	var results v1.ScrapeResults
	var scraper kubernetes.KubernetesScraper
	res := scraper.IncrementalScrape(ctx, config, objs)
	for i := range res {
		scraped := processScrapeResult(ctx, res[i])
		results = append(results, scraped...)
	}

	return results, nil
}

// RunK8IncrementalScraper scrapes the involved objects in the given events.
func RunK8IncrementalScraper(ctx api.ScrapeContext, config v1.Kubernetes, events []v1.KubernetesEvent) ([]v1.ScrapeResult, error) {
	var results v1.ScrapeResults
	var scraper kubernetes.KubernetesScraper
	for _, result := range scraper.IncrementalEventScrape(ctx, config, events) {
		scraped := processScrapeResult(ctx, result)
		results = append(results, scraped...)
	}

	return results, nil
}

// Run ...
func Run(ctx api.ScrapeContext) ([]v1.ScrapeResult, error) {
	var results v1.ScrapeResults
	for _, scraper := range All {
		if !scraper.CanScrape(ctx.ScrapeConfig().Spec) {
			continue
		}

		ctx.Debugf("Starting scraper")
		for _, result := range scraper.Scrape(ctx) {
			scraped := processScrapeResult(ctx, result)

			for i := range scraped {
				if scraped[i].Error != nil {
					ctx.Errorf("Error scraping %s: %v", scraped[i].ID, scraped[i].Error)
					ctx.JobHistory().AddError(scraped[i].Error.Error())
				}
			}

			if !scraped.HasErr() {
				ctx.JobHistory().IncrSuccess()
			}

			results = append(results, scraped...)
		}
	}
	return results, nil
}

// Add a list of changed json paths. If multiple changed then the highest level.
func summarizeChanges(changes []v1.ChangeResult) []v1.ChangeResult {
	for i, change := range changes {
		if change.Patches == "" {
			continue
		}

		var patch map[string]any
		if err := json.Unmarshal([]byte(change.Patches), &patch); err != nil {
			logger.Errorf("failed to unmarshal patches as map[string]any: %v %v", change.Patches, err)
			continue
		}

		paths := utils.ExtractLeafNodesAndCommonParents(patch)
		if len(paths) == 0 {
			continue
		}

		changes[i].Summary += strings.Join(paths, ", ")
	}

	return changes
}

// processScrapeResult extracts possibly more configs from the result
func processScrapeResult(ctx api.ScrapeContext, result v1.ScrapeResult) v1.ScrapeResults {
	spec := ctx.ScrapeConfig().Spec

	if result.AnalysisResult != nil {
		if rule, ok := analysis.Rules[result.AnalysisResult.Analyzer]; ok {
			result.AnalysisResult.AnalysisType = models.AnalysisType(rule.Category)
			result.AnalysisResult.Severity = models.Severity(rule.Severity)
		}
	}

	// TODO: Decide if this can be removed here. It's newly placed on func updateChange.
	// changes.ProcessRules(&result, result.BaseScraper.Transform.Change.Mapping...)

	result.Changes = summarizeChanges(result.Changes)

	// No config means we don't need to extract anything
	if result.Config == nil {
		return []v1.ScrapeResult{result}
	}

	extractor, err := processors.NewExtractor(result.BaseScraper)
	if err != nil {
		result.Error = err
		return []v1.ScrapeResult{result}
	}

	scraped, err := extractor.Extract(ctx, result)
	if err != nil {
		result.Error = err
		return []v1.ScrapeResult{result}
	}

	// In full mode, we extract all configs and changes from the result.
	if spec.Full {
		for i := range scraped {
			extractedConfig, changeRes, err := extractConfigChangesFromConfig(scraped[i].Config)
			if err != nil {
				scraped[i].Error = err
				continue
			}

			for _, cr := range changeRes {
				cr.ExternalID = scraped[i].ID
				cr.ConfigType = scraped[i].Type

				if cr.ExternalID == "" && cr.ConfigType == "" {
					continue
				}
				scraped[i].Changes = append(scraped[i].Changes, cr)
			}

			// The original config should be replaced by the extracted config (could also be nil)
			scraped[i].Config = extractedConfig
		}

		return scraped
	}

	return scraped
}

// extractChangesFromConfig will attempt to extract config & changes from
// the scraped config.
//
// The scraped config is expected to have fields "config" & "changes".
func extractConfigChangesFromConfig(config any) (any, []v1.ChangeResult, error) {
	configMap, ok := config.(map[string]any)
	if !ok {
		return nil, nil, errors.New("config is not a map")
	}

	var (
		extractedConfig  any
		extractedChanges []v1.ChangeResult
	)

	if eConf, ok := configMap["config"]; ok {
		extractedConfig = eConf
	}

	changes, ok := configMap["changes"].([]any)
	if !ok {
		return nil, nil, errors.New("changes is not a slice of map")
	}

	raw, err := json.Marshal(changes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal changes: %v", err)
	}

	if err := json.Unmarshal(raw, &extractedChanges); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal changes map into []v1.ChangeResult: %v", err)
	}

	return extractedConfig, extractedChanges, nil
}
