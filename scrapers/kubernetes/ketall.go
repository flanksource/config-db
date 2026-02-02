package kubernetes

import (
	gocontext "context"
	"errors"
	"fmt"
	"time"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/pkg/api"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/ketall"
	ketallClient "github.com/flanksource/ketall/client"
	"github.com/flanksource/ketall/options"
	"github.com/sethvargo/go-retry"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func scrape(ctx api.ScrapeContext, config v1.Kubernetes) ([]*unstructured.Unstructured, error) {
	ctx.Context = ctx.WithKubernetes(config.KubernetesConnection)

	opts := options.NewDefaultCmdOptions()
	opts, err := updateOptions(ctx.DutyContext(), opts, config)
	if err != nil {
		return nil, err
	}

	var objs []*unstructured.Unstructured

	backoff := retry.WithMaxRetries(3, retry.NewExponential(time.Second))
	err = retry.Do(ctx, backoff, func(goctx gocontext.Context) error {
		objs, err = ketall.KetAll(ctx, opts)
		if err != nil {
			if errors.Is(err, ketallClient.ErrEmpty) {
				return fmt.Errorf("no resources returned due to insufficient access")
			}
			return err
		}

		if len(objs) == 0 {
			// This scenario happens when new CRDs are introduced but we have a cached
			// restmapper who's discovery information is outdated
			// We reset the internal discovery cache
			k8s, err := ctx.Kubernetes()
			if err != nil {
				return fmt.Errorf("error getting k8s client: %w", err)
			}
			k8s.ResetRestMapper()
			return retry.RetryableError(fmt.Errorf("no resources or error returned"))
		}

		return nil
	})

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
