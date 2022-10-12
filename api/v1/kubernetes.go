package v1

import "github.com/flanksource/kommons"

type Kubernetes struct {
	BaseScraper     `json:",inline"`
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
}
