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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScrapeConfigSpec defines the desired state of ScrapeConfig
type ScrapeConfigSpec struct {
	ConfigScraper `json:",inline"`
}

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

	Spec   ScrapeConfigSpec   `json:"spec,omitempty"`
	Status ScrapeConfigStatus `json:"status,omitempty"`
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
