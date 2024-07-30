package terraform

type ResourceInstance struct {
	SchemaVersion       int            `json:"schema_version"`
	Attributes          map[string]any `json:"attributes"`
	SensitiveAttributes []any          `json:"sensitive_attributes"`
	Private             string         `json:"private"`
}

type Resource struct {
	Module    string             `json:"module"`
	Mode      string             `json:"mode"`
	Type      string             `json:"type"`
	Name      string             `json:"name"`
	Provider  string             `json:"provider"`
	Instances []ResourceInstance `json:"instances"`
}

type State struct {
	Version          int                    `json:"version"`
	TerraformVersion string                 `json:"terraform_version"`
	Serial           int                    `json:"serial"`
	Lineage          string                 `json:"lineage"`
	Outputs          map[string]interface{} `json:"outputs"`
	Resources        []Resource             `json:"resources"`
}
