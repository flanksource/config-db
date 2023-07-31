/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"encoding/json"

	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// ScrapeConfigStatus defines the observed state of ScrapeConfig
type ScrapeConfigStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ScrapeConfig is the Schema for the scrapeconfigs API
type ScrapeConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScraperSpec        `json:"spec,omitempty"`
	Status ScrapeConfigStatus `json:"status,omitempty"`
}

func (t *ScrapeConfig) ToModel() (models.ConfigScraper, error) {
	spec, err := json.Marshal(t.Spec)
	if err != nil {
		return models.ConfigScraper{}, err
	}

	return models.ConfigScraper{
		Name:   t.Name,
		Spec:   string(spec),
		Source: t.Annotations["source"],
	}, nil
}

func ScrapeConfigFromModel(m models.ConfigScraper) (ScrapeConfig, error) {
	var spec ScraperSpec
	if err := json.Unmarshal([]byte(m.Spec), &spec); err != nil {
		return ScrapeConfig{}, err
	}

	sc := ScrapeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: m.Name,
			Annotations: map[string]string{
				"source": m.Source,
			},
			UID:               k8stypes.UID(m.ID.String()),
			CreationTimestamp: metav1.Time{Time: m.CreatedAt},
		},
		Spec: spec,
	}

	if m.DeletedAt != nil {
		sc.ObjectMeta.DeletionTimestamp = &metav1.Time{Time: *m.DeletedAt}
	}

	return sc, nil
}

func (t *ScrapeConfig) GenerateName() (string, error) {
	return utils.Hash(t)
}

func (t *ScrapeConfig) GetPersistedID() *uuid.UUID {
	if t.GetUID() == "" {
		return nil
	}

	u, _ := uuid.Parse(string(t.GetUID()))
	return &u
}

//+kubebuilder:object:root=true

// ScrapeConfigList contains a list of ScrapeConfig
type ScrapeConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScrapeConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScrapeConfig{}, &ScrapeConfigList{})
}
