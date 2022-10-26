package kubernetes

import (
	"fmt"
	"path"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type KubernetesFileScrapper struct {
}

type pod struct {
	Name      string
	ID        string
	Namespace string
	Config    v1.KubernetesFile
	Container string
	Labels    map[string]string
}

func newPod(p k8sv1.Pod, config v1.KubernetesFile, labels map[string]string) pod {
	return pod{
		Name:      p.Name,
		ID:        p.Namespace + "/pod/" + p.Name,
		Config:    config,
		Namespace: p.Namespace,
		Container: config.Container,
		Labels:    labels,
	}
}

func startsWith(name, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(name), prefix)
}

// Scrape ...
func (kubernetes KubernetesFileScrapper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {

	results := v1.ScrapeResults{}
	if len(configs.KubernetesFile) == 0 {
		return results
	}

	client, err := ctx.Kommons.GetClientset()
	if err != nil {
		results.Errorf(err, "failed to get clientset")
		return results
	}

	var pods []pod

	for _, config := range configs.KubernetesFile {
		if config.Selector.Kind == "" {
			config.Selector.Kind = "Pod"
		}

		if startsWith(config.Selector.Kind, "pod") {
			podList, err := findPods(ctx, client, config.Selector)
			if err != nil {
				results.Errorf(err, "failed to find pods")
				continue
			}
			for _, p := range podList {
				pods = append(pods, newPod(p, config, p.Labels))
			}
		} else if startsWith(config.Selector.Kind, "deployment") {
			if config.Selector.Name != "" {
				deployment, err := client.AppsV1().Deployments(config.Selector.Namespace).Get(ctx, config.Selector.Name, metav1.GetOptions{})
				if errors.IsNotFound(err) {
					continue
				} else if err != nil {
					results.Errorf(err, "failed to get deployment")
					continue
				}

				podsList, err := findPods(ctx, client, v1.ResourceSelector{
					Namespace:     config.Selector.Namespace,
					LabelSelector: metav1.FormatLabelSelector(deployment.Spec.Selector),
				})
				if err != nil {
					results.Errorf(err, "failed to find pods for deployment %s/%s", config.Selector.Namespace, config.Selector.Name)
					continue
				}
				if len(podsList) == 0 {
					continue
				}

				pod := newPod(podsList[0], config, podsList[0].Labels)
				pod.ID = config.Selector.Namespace + "/deployment/" + config.Selector.Name
				pods = append(pods, pod)
			} else {
				results.Errorf(fmt.Errorf("deployment name is required"), "failed to get deployment")
				continue
			}
		} else if startsWith(config.Selector.Kind, "statefulset") {
			if config.Selector.Name != "" {
				statefulset, err := client.AppsV1().StatefulSets(config.Selector.Namespace).Get(ctx, config.Selector.Name, metav1.GetOptions{})
				if errors.IsNotFound(err) {
					continue
				} else if err != nil {
					results.Errorf(err, "failed to get statefukset")
					continue
				}

				podsList, err := findPods(ctx, client, v1.ResourceSelector{
					Namespace:     config.Selector.Namespace,
					LabelSelector: metav1.FormatLabelSelector(statefulset.Spec.Selector),
				})
				if err != nil {
					results.Errorf(err, "failed to find pods for statefulset %s/%s", config.Selector.Namespace, config.Selector.Name)
					continue
				}
				if len(podsList) == 0 {
					continue
				}

				pod := newPod(podsList[0], config, podsList[0].Labels)
				pod.ID = config.Selector.Namespace + "/statefulset/" + config.Selector.Name
				pods = append(pods, pod)
			} else {
				results.Errorf(fmt.Errorf("statefulset name is required"), "failed to get statefulset")
				continue
			}
		} else {
			results.Errorf(fmt.Errorf("kind %s is not supported", config.Selector.Kind), "failed to get resource")
			continue
		}
	}

	for _, pod := range pods {
		for _, file := range pod.Config.Files {
			for _, p := range file.Path {
				logger.Infof("Scraping %s/%s/%s/%s", pod.Namespace, pod.Name, pod.Container, p)
				stdout, _, err := ctx.Kommons.ExecutePodf(pod.Namespace, pod.Name, pod.Container, "cat", p)
				if err != nil {
					results.Errorf(err, "Failed to fetch %s/%s/%s:%s: %v", pod.Namespace, pod.Name, pod.Container, p)
					continue
				}

				if _, ok := pod.Labels["namespace"]; !ok {
					pod.Labels["namespace"] = pod.Namespace
				}
				if _, ok := pod.Labels["pod"]; !ok {
					pod.Labels["pod"] = pod.Name
				}
				pod.Labels = stripLabels(pod.Labels, "-hash")
				results = append(results, v1.ScrapeResult{
					BaseScraper: pod.Config.BaseScraper,
					Tags:        pod.Labels,
					Format:      file.Format,
					Type:        "File",
					ID:          pod.ID + "/" + p,
					Name:        path.Base(p),
					Config:      stdout,
				})
			}
		}
	}
	return results
}

func findPods(ctx *v1.ScrapeContext, client *kubernetes.Clientset, config v1.ResourceSelector) ([]k8sv1.Pod, error) {

	logger.Infof("Finding pods for %s name=%s labels=%s fields=%s", config.Namespace, config.Name, config.LabelSelector, config.FieldSelector)

	if config.IsEmpty() {
		return nil, fmt.Errorf("resource selector is empty")
	}

	podsV1 := client.CoreV1().Pods(config.Namespace)

	if config.Name != "" {
		pod, err := podsV1.Get(ctx, config.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		return []k8sv1.Pod{*pod}, nil
	}
	list, err := podsV1.List(ctx, metav1.ListOptions{LabelSelector: config.LabelSelector, FieldSelector: config.FieldSelector})
	if errors.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func stripLabels(labels map[string]string, strip ...string) map[string]string {
	var toDelete []string
	for k, _ := range labels {
		for _, s := range strip {
			if strings.HasSuffix(k, s) || strings.HasPrefix(k, s) {
				toDelete = append(toDelete, k)
			}
		}
	}
	for _, k := range toDelete {
		delete(labels, k)
	}
	return labels
}
