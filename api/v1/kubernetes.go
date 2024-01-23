package v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
	"github.com/flanksource/mapstructure"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
func (t *KubernetesEventExclusions) Filter(event KubernetesEvent) bool {
	if event.InvolvedObject.Name != "" && len(t.Names) != 0 {
		if collections.MatchItems(event.InvolvedObject.Name, t.Names...) {
			return true
		}
	}

	if event.InvolvedObject.Namespace != "" && len(t.Namespaces) != 0 {
		if collections.MatchItems(event.InvolvedObject.Namespace, t.Namespaces...) {
			return true
		}
	}

	if event.Reason != "" && len(t.Reasons) != 0 {
		if collections.MatchItems(event.Reason, t.Reasons...) {
			return true
		}
	}

	return false
}

type KubernetesEventConfig struct {
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

func (t KubernetesRelationshipLookup) IsEmpty() bool {
	if t.Value == "" && t.Label == "" && t.Expr == "" {
		return true
	}
	return false
}

type KubernetesRelationship struct {
	// Kind defines which field to use for the kind lookup
	Kind KubernetesRelationshipLookup `json:"kind" yaml:"kind"`
	// Name defines which field to use for the name lookup
	Name KubernetesRelationshipLookup `json:"name" yaml:"name"`
	// Namespace defines which field to use for the namespace lookup
	Namespace KubernetesRelationshipLookup `json:"namespace" yaml:"namespace"`
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
	Kubeconfig      *types.EnvVar              `json:"kubeconfig,omitempty"`
	MaxInflight     int64                      `json:"maxInflight,omitempty"`
	Event           KubernetesEventConfig      `json:"event,omitempty"`
	Exclusions      KubernetesConfigExclusions `json:"exclusions,omitempty"`

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

type InvolvedObject coreV1.ObjectReference

// KubernetesEvent represents a Kubernetes KubernetesEvent object
type KubernetesEvent struct {
	Reason         string             `json:"reason,omitempty"`
	Message        string             `json:"message,omitempty"`
	Source         map[string]string  `json:"source,omitempty"`
	Metadata       *metav1.ObjectMeta `json:"metadata,omitempty" mapstructure:"metadata"`
	InvolvedObject *InvolvedObject    `json:"involvedObject,omitempty"`
}

func (t *KubernetesEvent) GetUID() string {
	return string(t.Metadata.UID)
}

func (t *KubernetesEvent) AsMap() (map[string]any, error) {
	eventJSON, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event object: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(eventJSON, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event object: %v", err)
	}

	return result, nil
}

func (t *KubernetesEvent) FromObj(obj any) error {
	conf := mapstructure.DecoderConfig{
		TagName: "json", // Need to set this to json because when `obj` is v1.Event there's no mapstructure struct tag.
		Result:  t,
	}

	decoder, err := mapstructure.NewDecoder(&conf)
	if err != nil {
		return err
	}

	return decoder.Decode(obj)
}

func (t *KubernetesEvent) FromObjMap(obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, t)
}
