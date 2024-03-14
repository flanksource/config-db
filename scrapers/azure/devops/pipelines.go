package devops

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/gomplate/v3"
)

const PipelineRun = "AzureDevops::PipelineRun"

type AzureDevopsScraper struct {
}

func (ado AzureDevopsScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.AzureDevops) > 0
}

// Scrape ...
func (ado AzureDevopsScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {

	results := v1.ScrapeResults{}
	for _, config := range ctx.ScrapeConfig().Spec.AzureDevops {
		client, err := NewAzureDevopsClient(ctx, config)
		if err != nil {
			results.Errorf(err, "failed to create azure devops client for %s", config.Organization)
			continue
		}

		projects, err := client.GetProjects()
		if err != nil {
			results.Errorf(err, "failed to get projects for %s", config.Organization)
			continue
		}

		for _, project := range projects {
			if !collections.MatchItems(project.Name, config.Projects...) {
				continue
			}

			pipelines, err := client.GetPipelines(project.Name)
			if err != nil {
				results.Errorf(err, "failed to get pipelines for %s", project.Name)
				continue
			}
			logger.Debugf("[%s] found %d pipelines", project.Name, len(pipelines))

			for _, _pipeline := range pipelines {
				var pipeline = _pipeline

				if !collections.MatchItems(pipeline.Name, config.Pipelines...) {
					continue
				}

				var uniquePipelines = make(map[string]Pipeline)

				runs, err := client.GetPipelineRuns(project.Name, pipeline)
				if err != nil {
					results.Errorf(err, "failed to get pipeline runs for %s/%s", project.Name, pipeline.Name)
					continue
				}
				for _, _run := range runs {
					var run = _run
					var pipeline = _pipeline
					pipeline.TemplateParameters = run.TemplateParameters
					pipeline.Variables = run.Variables
					delete(pipeline.Links, "self")
					var id string
					if config.ID != "" {
						env := map[string]any{
							"project":      project,
							"pipeline":     pipeline,
							"organization": config.Organization,
						}

						id, err = gomplate.RunTemplate(env, gomplate.Template{
							Expression: config.ID,
						})
						if err != nil {
							return results.Errorf(err, "failed to render id template for %s/%s", project.Name, pipeline.Name)
						}
					} else {
						id = pipeline.GetID()
					}

					if _, ok := uniquePipelines[id]; !ok {
						uniquePipelines[id] = pipeline
					} else {
						pipeline = uniquePipelines[id]
					}
					run.TemplateParameters = nil
					run.Variables = nil
					delete(run.Links, "self")
					delete(run.Links, "pipeline")
					delete(run.Links, "pipeline.web")
					severity := "info"
					if run.Result != "succeeded" {
						severity = "failed"
					}
					pipeline.Runs = append(pipeline.Runs, v1.ChangeResult{
						ChangeType:       "Deployment",
						CreatedAt:        &run.CreatedDate,
						Severity:         severity,
						ExternalID:       id,
						ConfigType:       PipelineRun,
						Source:           run.Links["web"].Href,
						Summary:          fmt.Sprintf("%s,  %s in %s", run.Name, run.State, time.Millisecond*time.Duration(run.Duration)),
						Details:          v1.NewJSON(run),
						ExternalChangeID: fmt.Sprintf("%s/%d/%d", project.Name, pipeline.ID, run.ID),
					})
					uniquePipelines[id] = pipeline
				}

				for id, pipeline := range uniquePipelines {
					var changes = pipeline.Runs
					pipeline.Runs = nil
					results = append(results, v1.ScrapeResult{
						ConfigClass: "Deployment",
						Config:      pipeline,
						Type:        PipelineRun,
						ID:          id,
						Tags:        pipeline.GetTags(),
						Name:        pipeline.Name,
						Changes:     changes,
						Aliases:     []string{fmt.Sprintf("%s/%d", project.Name, pipeline.ID)},
					})
				}
			}
		}
	}

	return results

}
