package jobs

import (
	"fmt"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/duty/job"
)

// ConsumeKubernetesWatchEventsJobFunc returns a job that consumes kubernetes watch events
// for the given config of the scrapeconfig.
func ConsumeKubernetesWatchEventsJobFunc(scrapeConfig v1.ScrapeConfig, config v1.Kubernetes) *job.Job {
	return &job.Job{
		Name:       "ConsumeKubernetesWatchEvents",
		JobHistory: true,
		Singleton:  false, // This job is run per scrapeconfig per kubernetes config
		Retention:  job.RetentionShort,
		RunNow:     true,
		Schedule:   "@every 15s",
		Fn: func(ctx job.JobRuntime) error {
			ctx.History.ResourceType = job.ResourceTypeScraper
			ctx.History.ResourceID = string(scrapeConfig.GetUID())

			buffer, ok := kubernetes.WatchEventBuffers[config.Hash()]
			if !ok {
				return fmt.Errorf("no watcher found for config (scrapeconfig: %s) %s", scrapeConfig.GetUID(), config.Hash())
			}
			events := buffer.Drain()

			cc := api.NewScrapeContext(ctx.Context, ctx.DB(), ctx.Pool()).WithScrapeConfig(&scrapeConfig)
			results, err := scrapers.RunK8IncrementalScraper(cc, config, events)
			if err != nil {
				return err
			}

			for i := range results {
				if results[i].Error != nil {
					ctx.History.AddError(results[i].Error.Error())
				}
			}

			if err := scrapers.SaveResults(cc, results); err != nil {
				return fmt.Errorf("failed to save results: %w", err)
			}

			ctx.History.SuccessCount = len(events)
			return nil
		},
	}
}
