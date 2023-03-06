package v1

import (
	"strings"

	"github.com/flanksource/kommons"
)

// SeverityKeywords is used to identify the severity
// from the Kubernetes Event reason.
type SeverityKeywords struct {
	Warn  []string `json:"warn,omitempty"`
	Error []string `json:"error,omitempty"`
}

type KubernetesEvent struct {
	SeverityKeywords SeverityKeywords `json:"severityKeywords,omitempty"`
}

type Kubernetes struct {
	BaseScraper     `json:",inline"`
	ClusterName     string          `json:"clusterName,omitempty"`
	Namespace       string          `json:"namespace,omitempty"`
	UseCache        bool            `json:"useCache,omitempty"`
	AllowIncomplete bool            `json:"allowIncomplete,omitempty"`
	Scope           string          `json:"scope,omitempty"`
	Since           string          `json:"since,omitempty"`
	Selector        string          `json:"selector,omitempty"`
	FieldSelector   string          `json:"fieldSelector,omitempty"`
	MaxInflight     int64           `json:"maxInflight,omitempty"`
	Exclusions      []string        `json:"exclusions,omitempty"`
	Kubeconfig      *kommons.EnvVar `json:"kubeconfig,omitempty"`
	Event           KubernetesEvent `json:"event,omitempty"`
}

type KubernetesFile struct {
	BaseScraper `json:",inline"`
	Selector    ResourceSelector `json:"selector,inline"`
	Container   string           `json:"container,omitempty"`
	Files       []PodFile        `json:"files,omitempty"`
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
