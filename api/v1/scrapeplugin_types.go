package v1

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScrapePluginStatus defines the observed state of Plugin
type ScrapePluginStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ScrapePlugin is the Schema for the scraper plugins
type ScrapePlugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScrapePluginSpec   `json:"spec,omitempty"`
	Status ScrapePluginStatus `json:"status,omitempty"`
}

type ScrapePluginSpec struct {
	Change TransformChange `json:"changes,omitempty"`

	// Retention config for changes, types, and stale items.
	Retention *RetentionSpec `json:"retention,omitempty"`

	// Relationship allows you to form relationships between config items using selectors.
	Relationship []RelationshipConfig `json:"relationship,omitempty"`

	// Properties are custom templatable properties for the scraped config items
	// grouped by the config type.
	Properties []ConfigProperties `json:"properties,omitempty" template:"true"`

	Locations []LocationOrAlias `json:"locations,omitempty"`

	Aliases []LocationOrAlias `json:"aliases,omitempty"`
}

func (t ScrapePlugin) ToModel() (*models.ScrapePlugin, error) {
	var id uuid.UUID
	if v, err := uuid.Parse(string(t.GetUID())); err == nil {
		id = v
	}

	specJSON, err := json.Marshal(t.Spec)
	if err != nil {
		return nil, err
	}

	return &models.ScrapePlugin{
		ID:        id,
		Name:      t.Name,
		Namespace: t.Namespace,
		Spec:      specJSON,
		CreatedAt: t.CreationTimestamp.Time,
	}, nil
}

func (t ScrapePlugin) LoggerName() string {
	return fmt.Sprintf("plugin.%s.%s", t.Namespace, t.Name)
}

func (t ScrapePlugin) GetContext() map[string]any {
	return map[string]any{
		"namespace":  t.Namespace,
		"name":       t.Name,
		"scraper_id": t.GetPersistedID(),
	}
}

func (t *ScrapePlugin) GetPersistedID() *uuid.UUID {
	if t.GetUID() == "" {
		return nil
	}

	u, _ := uuid.Parse(string(t.GetUID()))
	return &u
}

//+kubebuilder:object:root=true

// ScrapePluginList contains a list of Plugin
type ScrapePluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScrapePlugin `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScrapePlugin{}, &ScrapePluginList{})
}
