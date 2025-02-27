package kubernetes

import (
	"errors"
	"fmt"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/ketall"
	ketallClient "github.com/flanksource/ketall/client"
	"github.com/flanksource/ketall/options"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func scrape(ctx api.ScrapeContext, config v1.Kubernetes) ([]*unstructured.Unstructured, error) {
	ctx.Context = ctx.WithKubernetes(config.KubernetesConnection)

	opts := options.NewDefaultCmdOptions()
	opts, err := updateOptions(ctx.DutyContext(), opts, config)
	if err != nil {
		return nil, err
	}

	objs, err := ketall.KetAll(ctx, opts)
	if err != nil {
		if errors.Is(err, ketallClient.ErrEmpty) {
			return nil, fmt.Errorf("no resources returned due to insufficient access")
		}
		return nil, err
	}

	return objs, nil
}

func updateOptions(ctx context.Context, opts *options.KetallOptions, config v1.Kubernetes) (*options.KetallOptions, error) {
	opts.AllowIncomplete = config.AllowIncomplete
	opts.Namespace = config.Namespace
	opts.Scope = config.Scope
	opts.Selector = config.Selector
	opts.FieldSelector = config.FieldSelector
	opts.UseCache = config.UseCache
	opts.MaxInflight = config.MaxInflight
	opts.Exclusions = append(config.Exclusions.List(), "componentstatuses", "Event")
	opts.Since = config.Since

	k8s, err := ctx.Kubernetes()
	if err != nil {
		return opts, fmt.Errorf("error creating k8s client: %w", err)
	}
	opts.Flags.KubeConfig = k8s.RestConfig()

	rm, err := k8s.GetRestMapper()
	if err != nil {
		return opts, fmt.Errorf("error getting k8s rest mapper: %w", err)
	}
	opts.Flags.RestMapper = rm

	return opts, nil
}
