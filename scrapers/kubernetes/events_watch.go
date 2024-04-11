package kubernetes

import (
	"fmt"
	"strings"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

var (
	// BufferSize is the size of the channel that buffers kubernetes watch events
	BufferSize = 5000

	// WatchEventBuffers stores a sync buffer per kubernetes config
	WatchEventBuffers = make(map[string]chan v1.KubernetesEvent)
)

// WatchEvents watches Kubernetes events for any config changes & fetches
// the referenced config items in batches.
func WatchEvents(ctx api.ScrapeContext, config v1.Kubernetes) error {
	buffer := make(chan v1.KubernetesEvent, ctx.DutyContext().Properties().Int("kubernetes.watch.events.bufferSize", BufferSize))
	WatchEventBuffers[config.Hash()] = buffer

	if config.Kubeconfig != nil {
		var err error
		ctx, err = applyKubeconfig(ctx, *config.Kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to apply kube config")
		}
	}

	watcher, err := ctx.Kubernetes().CoreV1().Events(config.Namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to create a new event watcher: %w", err)
	}
	defer watcher.Stop()

	for watchEvent := range watcher.ResultChan() {
		var event v1.KubernetesEvent
		if err := event.FromObj(watchEvent.Object); err != nil {
			logger.Errorf("failed to unmarshal event (id=%s): %v", event.GetUID(), err)
			continue
		}

		if event.InvolvedObject == nil {
			continue
		}

		if config.Exclusions.Filter(event.InvolvedObject.Name, event.InvolvedObject.Namespace, event.InvolvedObject.Kind, nil) {
			continue
		}

		buffer <- event
	}

	return nil
}

func applyKubeconfig(ctx api.ScrapeContext, kubeConfig types.EnvVar) (api.ScrapeContext, error) {
	val, err := ctx.GetEnvValueFromCache(kubeConfig)
	if err != nil {
		return ctx, fmt.Errorf("failed to get kubeconfig from env: %w", err)
	}

	if strings.HasPrefix(val, "/") {
		kube, err := newKubeClientWithConfigPath(val)
		if err != nil {
			return ctx, fmt.Errorf("failed to initialize kubernetes client from the provided kubeconfig: %w", err)
		}

		ctx.Context = ctx.WithKubernetes(kube)
	} else {
		kube, err := newKubeClientWithConfig(val)
		if err != nil {
			return ctx, fmt.Errorf("failed to initialize kubernetes client from the provided kubeconfig: %w", err)
		}

		ctx.Context = ctx.WithKubernetes(kube)
	}

	return ctx, nil
}

func newKubeClientWithConfigPath(kubeConfigPath string) (kubernetes.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		return fake.NewSimpleClientset(), err
	}

	return kubernetes.NewForConfig(config)
}

func newKubeClientWithConfig(kubeConfig string) (kubernetes.Interface, error) {
	getter := func() (*clientcmdapi.Config, error) {
		clientCfg, err := clientcmd.NewClientConfigFromBytes([]byte(kubeConfig))
		if err != nil {
			return nil, err
		}

		apiCfg, err := clientCfg.RawConfig()
		if err != nil {
			return nil, err
		}

		return &apiCfg, nil
	}

	config, err := clientcmd.BuildConfigFromKubeconfigGetter("", getter)
	if err != nil {
		return fake.NewSimpleClientset(), err
	}

	return kubernetes.NewForConfig(config)
}
