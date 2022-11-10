package kubernetes

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/ketall"
	"github.com/flanksource/ketall/options"
)

type KubernetesScrapper struct {
}

// Scrape ...
func (kubernetes KubernetesScrapper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {

	results := v1.ScrapeResults{}
	for _, config := range configs.Kubernetes {
		opts := options.NewDefaultCmdOptions()
		opts = updateOptions(opts, config)

		objs := ketall.KetAll(opts)

		for _, obj := range objs {
			createdAt := obj.GetCreationTimestamp().Time
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Name:        obj.GetName(),
				Namespace:   obj.GetNamespace(),
				Type:        obj.GetKind(),
				CreatedAt:   &createdAt,
				Config:      *obj,
				ID:          string(obj.GetUID()),
			})
		}
	}
	return results

}

func updateOptions(opts *options.KetallOptions, config v1.Kubernetes) *options.KetallOptions {
	opts.AllowIncomplete = config.AllowIncomplete
	opts.Namespace = config.Namespace
	opts.Scope = config.Scope
	opts.Selector = config.Selector
	opts.FieldSelector = config.FieldSelector
	opts.UseCache = config.UseCache
	opts.MaxInflight = config.MaxInflight
	opts.Exclusions = config.Exclusions
	opts.Since = config.Since
	//TODO: update kubeconfig reference if provided by user
	// if config.Kubeconfig != nil {
	// 	opts.Kubeconfig = config.Kubeconfig.GetValue()
	// }
	return opts
}
