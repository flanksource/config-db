package v1

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/mapstructure"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SeverityKeywords is used to identify the severity
// from the Kubernetes Event reason.
type SeverityKeywords struct {
	Warn  []string `json:"warn,omitempty"`
	Error []string `json:"error,omitempty"`
}

type KubernetesEventExclusions struct {
	Names      []string `json:"name,omitempty" yaml:"name,omitempty"`
	Namespaces []string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Reasons    []string `json:"reason,omitempty" yaml:"reason,omitempty"`
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
	// Exclusions defines what events needs to be dropped.
	Exclusions KubernetesEventExclusions `json:"exclusions,omitempty"`

	SeverityKeywords SeverityKeywords `json:"severityKeywords,omitempty"`
}

type KubernetesExclusionConfig struct {
	Names      []string          `json:"name,omitempty" yaml:"name,omitempty"`
	Kinds      []string          `json:"kind,omitempty" yaml:"kind,omitempty"`
	Namespaces []string          `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Labels     map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// List returns the union of the exclusions.
func (t *KubernetesExclusionConfig) List() []string {
	result := append(t.Names, t.Kinds...)
	result = append(result, t.Namespaces...)
	return result
}

// Filter returns true if the given input matches any of the exclusions.
func (t *KubernetesExclusionConfig) Filter(name, namespace, kind string, labels map[string]string) bool {
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

type KubernetesRelationshipSelector struct {
	Kind      string `json:"kind" yaml:"kind"`
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
}

type KubernetesRelationshipSelectorTemplate struct {
	// Kind defines which field to use for the kind lookup
	Kind RelationshipLookup `json:"kind" yaml:"kind"`
	// Name defines which field to use for the name lookup
	Name RelationshipLookup `json:"name" yaml:"name"`
	// Namespace defines which field to use for the namespace lookup
	Namespace RelationshipLookup `json:"namespace" yaml:"namespace"`
}

func (t *KubernetesRelationshipSelectorTemplate) IsEmpty() bool {
	return t.Kind.IsEmpty() && t.Name.IsEmpty() && t.Namespace.IsEmpty()
}

// Eval evaluates the template and returns a KubernetesRelationshipSelector.
// If any of the filter returns an empty value, the evaluation results to a nil selector.
// i.e. if a lookup is non-empty, it must return a non-empty value.
func (t *KubernetesRelationshipSelectorTemplate) Eval(labels map[string]string, env map[string]any) (*KubernetesRelationshipSelector, error) {
	if t.IsEmpty() {
		return nil, nil
	}

	var output KubernetesRelationshipSelector
	var err error

	if !t.Name.IsEmpty() {
		if output.Name, err = t.Name.Eval(labels, env); err != nil {
			return nil, fmt.Errorf("failed to evaluate Name: %s: %w", t.Name, err)
		} else if output.Name == "" {
			return nil, nil
		}
	}

	if !t.Kind.IsEmpty() {
		if output.Kind, err = t.Kind.Eval(labels, env); err != nil {
			return nil, fmt.Errorf("failed to evaluate kind: %s: %w", t.Kind, err)
		} else if output.Kind == "" {
			return nil, nil
		}
	}

	if !t.Namespace.IsEmpty() {
		if output.Namespace, err = t.Namespace.Eval(labels, env); err != nil {
			return nil, fmt.Errorf("failed to evaluate namespace: %s: %w", t.Namespace, err)
		} else if output.Namespace == "" {
			return nil, nil
		}
	}

	return &output, nil
}

var DefaultWatchKinds = []string{"Pod", "Deployment", "StatefulSet", "Daemonset", "ReplicaSet", "CronJob", "Job"}

type Kubernetes struct {
	BaseScraper     `json:",inline"`
	ClusterName     string        `json:"clusterName,omitempty"`
	Namespace       string        `json:"namespace,omitempty"`
	UseCache        bool          `json:"useCache,omitempty"`
	AllowIncomplete bool          `json:"allowIncomplete,omitempty"`
	Scope           string        `json:"scope,omitempty"`
	Since           string        `json:"since,omitempty"`
	Selector        string        `json:"selector,omitempty"`
	FieldSelector   string        `json:"fieldSelector,omitempty"`
	MaxInflight     int64         `json:"maxInflight,omitempty"`
	Kubeconfig      *types.EnvVar `json:"kubeconfig,omitempty"`

	WatchKinds []string `json:"watchKinds,omitempty"`

	// Event specifies how the Kubernetes event should be handled.
	Event KubernetesEventConfig `json:"event,omitempty"`

	// Exclusions excludes certain kubernetes objects from being scraped.
	Exclusions KubernetesExclusionConfig `json:"exclusions,omitempty"`

	// Relationships specify the fields to use to relate Kubernetes objects.
	Relationships []KubernetesRelationshipSelectorTemplate `json:"relationships,omitempty"`
}

// Hash returns an identifier to uniquely identify this kubernetes config
func (t *Kubernetes) Hash() string {
	return t.ClusterName
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

func (t *KubernetesEvent) ToUnstructured() (*unstructured.Unstructured, error) {
	b, err := t.AsMap()
	if err != nil {
		return nil, err
	}

	b["kind"] = "Event"

	return &unstructured.Unstructured{Object: b}, nil
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
