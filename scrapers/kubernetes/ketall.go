package kubernetes

import (
	"errors"
	"fmt"
	"strings"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/ketall"
	ketallClient "github.com/flanksource/ketall/client"
	"github.com/flanksource/ketall/options"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
)

func scrape(ctx api.ScrapeContext, config v1.Kubernetes) ([]*unstructured.Unstructured, error) {

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
	if config.Kubeconfig != nil {
		val, err := ctx.GetEnvValueFromCache(*config.Kubeconfig, ctx.GetNamespace())
		if err != nil {
			return nil, err
		}

		if strings.HasPrefix(val, "/") {
			opts.Flags.ConfigFlags.KubeConfig = &val
		} else {
			clientCfg, err := clientcmd.NewClientConfigFromBytes([]byte(val))
			if err != nil {
				return nil, fmt.Errorf("failed to create client config: %w", err)
			}

			restConfig, err := clientCfg.ClientConfig()
			if err != nil {
				return nil, err
			}

			opts.Flags.KubeConfig = restConfig
		}
	}

	return opts, nil
}
