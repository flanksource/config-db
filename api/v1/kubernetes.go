package v1

import (
	"strings"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty/types"
)

// SeverityKeywords is used to identify the severity
// from the Kubernetes Event reason.
type SeverityKeywords struct {
	Warn  []string `json:"warn,omitempty"`
	Error []string `json:"error,omitempty"`
}

type KubernetesEventExclusions struct {
	Names      []string `json:"name" yaml:"name"`
	Namespaces []string `json:"namespace" yaml:"namespace"`
	Reasons    []string `json:"reason" yaml:"reason"`
}

// Filter returns true if the given input matches any of the exclusions.
func (t *KubernetesEventExclusions) Filter(name, namespace, reason string) bool {
	if name != "" && len(t.Names) != 0 {
		if collections.MatchItems(name, t.Names...) {
			return true
		}
	}

	if namespace != "" && len(t.Namespaces) != 0 {
		if collections.MatchItems(namespace, t.Namespaces...) {
			return true
		}
	}

	if reason != "" && len(t.Reasons) != 0 {
		if collections.MatchItems(reason, t.Reasons...) {
			return true
		}
	}

	return false
}

type KubernetesEvent struct {
	Exclusions       KubernetesEventExclusions `json:"exclusions,omitempty"`
	SeverityKeywords SeverityKeywords          `json:"severityKeywords,omitempty"`
}

type KubernetesConfigExclusions struct {
	Names      []string          `json:"name" yaml:"name"`
	Kinds      []string          `json:"kind" yaml:"kind"`
	Namespaces []string          `json:"namespace" yaml:"namespace"`
	Labels     map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// List returns the union of the exclusions.
func (t *KubernetesConfigExclusions) List() []string {
	result := append(t.Names, t.Kinds...)
	result = append(result, t.Namespaces...)
	return result
}

// Filter returns true if the given input matches any of the exclusions.
func (t *KubernetesConfigExclusions) Filter(name, namespace, kind string, labels map[string]string) bool {
	if name != "" && len(t.Names) != 0 {
		if collections.MatchItems(name, t.Names...) {
			return true
		}
	}

	if namespace != "" && len(t.Namespaces) != 0 {
		if collections.MatchItems(namespace, t.Namespaces...) {
			return true
		}
	}

	if kind != "" && len(t.Kinds) != 0 {
		if collections.MatchItems(kind, t.Kinds...) {
			return true
		}
	}

	if len(labels) != 0 {
		for k, v := range t.Labels {
			qVal, ok := labels[k]
			if !ok {
				continue
			}

			if collections.MatchItems(qVal, v) {
				return true
			}
		}
	}

	return false
}

type Kubernetes struct {
	BaseScraper     `json:",inline"`
	ClusterName     string                     `json:"clusterName,omitempty"`
	Namespace       string                     `json:"namespace,omitempty"`
	UseCache        bool                       `json:"useCache,omitempty"`
	AllowIncomplete bool                       `json:"allowIncomplete,omitempty"`
	Scope           string                     `json:"scope,omitempty"`
	Since           string                     `json:"since,omitempty"`
	Selector        string                     `json:"selector,omitempty"`
	FieldSelector   string                     `json:"fieldSelector,omitempty"`
	MaxInflight     int64                      `json:"maxInflight,omitempty"`
	Kubeconfig      *types.EnvVar              `json:"kubeconfig,omitempty"`
	Event           KubernetesEvent            `json:"event,omitempty"`
	Exclusions      KubernetesConfigExclusions `json:"exclusions,omitempty"`
}

type KubernetesFile struct {
	BaseScraper `json:",inline"`
	Selector    ResourceSelector `json:"selector" yaml:"selector"`
	Container   string           `json:"container,omitempty" yaml:"container,omitempty"`
	Files       []PodFile        `json:"files,omitempty" yaml:"files,omitempty"`
}

type PodFile struct {
	Path   []string `json:"path,omitempty"`
	Format string   `json:"format,omitempty"`
}

func (p PodFile) String() string {
	return strings.Join(p.Path, ",")
}

type ResourceSelector struct {
	Namespace     string `json:"namespace,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Name          string `yaml:"name,omitempty" json:"name,omitempty"`
	LabelSelector string `json:"labelSelector,omitempty" yaml:"labelSelector,omitempty"`
	FieldSelector string `json:"fieldSelector,omitempty" yaml:"fieldSelector,omitempty"`
}

func (r ResourceSelector) IsEmpty() bool {
	return r.Name == "" && r.LabelSelector == "" && r.FieldSelector == ""
}

func (r ResourceSelector) String() string {
	s := r.Kind
	if r.Namespace != "" {
		s += "/" + r.Namespace
	}
	if r.Name != "" {
		return s + "/" + r.Name
	}
	if r.LabelSelector != "" {
		s += " labels=" + r.LabelSelector
	}
	if r.FieldSelector != "" {
		s += " fields=" + r.FieldSelector
	}
	return s
}
