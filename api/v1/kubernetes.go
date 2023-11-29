package v1

import (
	"errors"
	"strings"

	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
)

// SeverityKeywords is used to identify the severity
// from the Kubernetes Event reason.
type SeverityKeywords struct {
	Warn  []string `json:"warn,omitempty"`
	Error []string `json:"error,omitempty"`
}

type KubernetesEvent struct {
	// Exclusions is a list of keywords that'll be used to exclude
	// event objects based on the reason.
	Exclusions       []string         `json:"exclusions,omitempty"`
	SeverityKeywords SeverityKeywords `json:"severityKeywords,omitempty"`
}

type KubernetesRelationshipLookup struct {
	Expr  string `json:"expr,omitempty"`
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
}

func (t *KubernetesRelationshipLookup) Eval(labels map[string]string, envVar map[string]any) (string, error) {
	if t.Value != "" {
		return t.Value, nil
	}

	if t.Label != "" {
		return labels[t.Label], nil
	}

	if t.Expr != "" {
		res, err := gomplate.RunTemplate(envVar, gomplate.Template{Expression: t.Expr})
		if err != nil {
			return "", err
		}

		return res, nil
	}

	return "", errors.New("unknown kubernetes relationship lookup type")
}

type KubernetesRelationship struct {
	// Kind defines which field to use for the kind lookup
	Kind KubernetesRelationshipLookup `json:"kind" yaml:"kind"`
	// Name defines which field to use for the name lookup
	Name KubernetesRelationshipLookup `json:"name,omitempty" yaml:"name,omitempty"`
	// Namespace defines which field to use for the namespace lookup
	Namespace KubernetesRelationshipLookup `json:"namespace,omitempty" yaml:"namespace,omitempty"`
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
	Kubeconfig      *types.EnvVar   `json:"kubeconfig,omitempty"`
	Event           KubernetesEvent `json:"event,omitempty"`

	// Relationships specify the fields to use to relate Kubernetes objects.
	Relationships []KubernetesRelationship `json:"relationships,omitempty"`
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
