package devops

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

const PipelineRun = "AzureDevops::PipelineRun"

type AzureDevopsScraper struct {
}

func (ado AzureDevopsScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.AzureDevops) > 0
}

// Scrape ...
func (ado AzureDevopsScraper) Scrape(ctx *v1.ScrapeContext) v1.ScrapeResults {

	results := v1.ScrapeResults{}
	for _, config := range ctx.ScrapeConfig.Spec.AzureDevops {
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

			if !utils.MatchItems(project.Name, config.Projects...) {
				continue
			}

			fmt.Printf("%v\n", project.Name)
			pipelines, err := client.GetPipelines(project.Name)
			if err != nil {
				results.Errorf(err, "failed to get pipelines for %s", project.Name)
				continue
			}
			for _, _pipeline := range pipelines {
				var pipeline = _pipeline

				if !utils.MatchItems(pipeline.Name, config.Pipelines...) {
					continue
				}

				var uniquePipelines = make(map[string]Pipeline)
				fmt.Printf("\t->%v\n", pipeline.Name)

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
					var id = pipeline.GetID()

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
