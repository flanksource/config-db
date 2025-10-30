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
	"fmt"
	"strings"

	"github.com/flanksource/duty/models"
	"github.com/flanksource/kopper"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

type LastRunStatus struct {
	Success   int         `json:"success,omitempty"`
	Error     int         `json:"error,omitempty"`
	Errors    []string    `json:"errors,omitempty"`
	Timestamp metav1.Time `json:"timestamp,omitempty"`
}

// ScrapeConfigStatus defines the observed state of ScrapeConfig
type ScrapeConfigStatus struct {
	ObservedGeneration int64         `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
	LastRun            LastRunStatus `json:"lastRun,omitempty"`
}

var ScrapeConfigReconciler kopper.Reconciler[ScrapeConfig, *ScrapeConfig]

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ScrapeConfig is the Schema for the scrapeconfigs API
type ScrapeConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScraperSpec        `json:"spec,omitempty"`
	Status ScrapeConfigStatus `json:"status,omitempty"`
}

func (t ScrapeConfig) LoggerName() string {
	return fmt.Sprintf("kubernetes.%s.%s", t.Namespace, t.Name)
}

func (t ScrapeConfig) GetContext() map[string]any {
	return map[string]any{
		"namespace":  t.Namespace,
		"name":       t.Name,
		"scraper_id": t.GetPersistedID(),
	}
}

func (t ScrapeConfig) NamespaceScope() string {
	return t.Namespace
}

func (t *ScrapeConfig) Type() string {
	if len(t.Spec.GCP) != 0 {
		return "gcp"
	}
	if len(t.Spec.AWS) != 0 {
		return "aws"
	}
	if len(t.Spec.File) != 0 {
		return "file"
	}
	if len(t.Spec.Kubernetes) != 0 {
		return "kubernetes"
	}
	if len(t.Spec.KubernetesFile) != 0 {
		return "kubernetesfile"
	}
	if len(t.Spec.AzureDevops) != 0 {
		return "azuredevops"
	}
	if len(t.Spec.GithubActions) != 0 {
		return "githubactions"
	}
	if len(t.Spec.GitHubSecurity) != 0 {
		return "githubsecurity"
	}
	if len(t.Spec.OpenSSFScorecard) != 0 {
		return "openssf"
	}
	if len(t.Spec.Azure) != 0 {
		return "azure"
	}
	if len(t.Spec.SQL) != 0 {
		return "sql"
	}
	if len(t.Spec.Slack) != 0 {
		return "slack"
	}
	if len(t.Spec.Trivy) != 0 {
		return "trivy"
	}
	if len(t.Spec.Terraform) != 0 {
		return "terraform"
	}
	if len(t.Spec.HTTP) != 0 {
		return "http"
	}
	return ""
}

// IsCustom returns true if the scraper is custom
//
// Custom scrapers are user crafted scrapers
// Example: file scraper, SQL scraper, ...
func (t *ScrapeConfig) IsCustom() bool {
	return len(t.Spec.AWS) == 0 && len(t.Spec.Kubernetes) == 0 && len(t.Spec.KubernetesFile) == 0 &&
		len(t.Spec.Azure) == 0 && len(t.Spec.AzureDevops) == 0
}

func (t *ScrapeConfig) ToModel() (models.ConfigScraper, error) {
	spec, err := json.Marshal(t.Spec)
	if err != nil {
		return models.ConfigScraper{}, err
	}

	agentID := uuid.Nil
	if id, err := uuid.Parse(t.Annotations["agent_id"]); err == nil {
		agentID = id
	}

	return models.ConfigScraper{
		Name:    t.Name,
		Spec:    string(spec),
		Source:  t.Annotations["source"],
		AgentID: agentID,
	}, nil
}

func ScrapeConfigFromModel(m models.ConfigScraper) (ScrapeConfig, error) {
	var spec ScraperSpec
	if err := json.Unmarshal([]byte(m.Spec), &spec); err != nil {
		return ScrapeConfig{}, err
	}

	name := m.Name
	namespace := "default"

	// For CRDs we keep DB name as namespace/name
	if s := strings.Split(m.Name, "/"); len(s) == 2 {
		namespace, name = s[0], s[1]
	}

	sc := ScrapeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				"source":   m.Source,
				"agent_id": m.AgentID.String(),
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
