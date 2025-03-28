package v1

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ReservedAnnotations
const (
	// AnnotationIgnoreConfig excludes the object from being scraped
	AnnotationIgnoreConfig = "config-db.flanksource.com/ignore"

	// AnnotationIgnoreChangeByType contains the list of change types to ignore
	AnnotationIgnoreChangeByType = "config-db.flanksource.com/ignore-changes"

	// AnnotationIgnoreChangeBySeverity contains the list of severity for the change types to ignore
	AnnotationIgnoreChangeBySeverity = "config-db.flanksource.com/ignore-change-severity"

	// AnnotationCustomTags contains the list of tags to add to the scraped config
	AnnotationCustomTags = "config-db.flanksource.com/tags"
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
	Kind duty.Lookup `json:"kind" yaml:"kind"`
	// Name defines which field to use for the name lookup
	Name duty.Lookup `json:"name" yaml:"name"`
	// Namespace defines which field to use for the namespace lookup
	Namespace duty.Lookup `json:"namespace" yaml:"namespace"`
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

var DefaultWatchKinds = []KubernetesResourceToWatch{
	{ApiVersion: "apps/v1", Kind: "DaemonSet"},
	{ApiVersion: "apps/v1", Kind: "Deployment"},
	{ApiVersion: "apps/v1", Kind: "ReplicaSet"},
	{ApiVersion: "apps/v1", Kind: "StatefulSet"},
	{ApiVersion: "batch/v1", Kind: "CronJob"},
	{ApiVersion: "batch/v1", Kind: "Job"},
	{ApiVersion: "v1", Kind: "Node"},
	{ApiVersion: "v1", Kind: "Event"},
	{ApiVersion: "v1", Kind: "Pod"},
}

type KubernetesResourcesToWatch []KubernetesResourceToWatch

func (krws KubernetesResourcesToWatch) String() string {
	var str string
	for _, krw := range krws {
		str += krw.String()
	}
	return str
}

func (krws KubernetesResourcesToWatch) Contains(elem KubernetesResourceToWatch) bool {
	for _, krw := range krws {
		if krw.Kind == elem.Kind && krw.ApiVersion == elem.ApiVersion {
			return true
		}
	}
	return false
}

type KubernetesResourceToWatch struct {
	ApiVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

func (krw KubernetesResourceToWatch) String() string {
	return fmt.Sprintf("ApiVersion=%s,Kind=%s", krw.ApiVersion, krw.Kind)
}

func AddEventResourceToWatch(watches []KubernetesResourceToWatch) []KubernetesResourceToWatch {
	for _, w := range watches {
		if w.ApiVersion == "v1" && w.Kind == "Event" {
			return watches
		}
	}

	watches = append(watches, KubernetesResourceToWatch{
		ApiVersion: "v1",
		Kind:       "Event",
	})

	return watches
}

type Kubernetes struct {
	BaseScraper                     `json:",inline"`
	connection.KubernetesConnection `json:",inline"`

	ClusterName     string `json:"clusterName"`
	Namespace       string `json:"namespace,omitempty"`
	UseCache        bool   `json:"useCache,omitempty"`
	AllowIncomplete bool   `json:"allowIncomplete,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Since           string `json:"since,omitempty"`
	Selector        string `json:"selector,omitempty"`
	FieldSelector   string `json:"fieldSelector,omitempty"`
	MaxInflight     int64  `json:"maxInflight,omitempty"`

	// Watch specifies which Kubernetes resources should be watched.
	// This allows for near real-time updates of the config items
	// without having to wait for the scraper on the specified interval.
	Watch []KubernetesResourceToWatch `json:"watch,omitempty"`

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
	Kubeconfig  *types.EnvVar    `json:"kubeconfig,omitempty"`
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

func InvolvedObjectFromObj(obj unstructured.Unstructured) InvolvedObject {
	return InvolvedObject{
		Name:            obj.GetName(),
		Namespace:       obj.GetNamespace(),
		UID:             obj.GetUID(),
		ResourceVersion: obj.GetResourceVersion(),
		APIVersion:      obj.GetAPIVersion(),
		Kind:            obj.GetKind(),
	}
}

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

func (t *KubernetesEvent) FromObjMap(obj any) error {
	if v, ok := obj.(coreV1.Event); ok {
		// Don't go through marshalling/unmarshalling.
		// Map manually.

		t.InvolvedObject = (*InvolvedObject)(&v.InvolvedObject)
		t.Metadata = &v.ObjectMeta
		t.Reason = v.Reason
		t.Message = v.Message
		t.Source = map[string]string{
			"component": v.Source.Component,
			"host":      v.Source.Host,
		}

		return nil
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, t)

}
