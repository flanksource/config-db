package v1

import "strings"

// AWS ...
type AWS struct {
	*AWSConnection
	PatchStates         bool     `json:"patch_states,omitempty"`
	PatchDetails        bool     `json:"patch_details,omitempty"`
	Inventory           bool     `json:"inventory,omitempty"`
	Compliance          bool     `json:"compliance,omitempty"`
	TrustedAdvisorCheck bool     `json:"trusted_advisor_check,omitempty"`
	Include             []string `json:"include,omitempty"`
	BaseScraper         `json:",inline"`
}

func (aws AWS) Includes(resource string) bool {
	if len(aws.Include) == 0 {
		return true
	}
	for _, include := range aws.Include {
		if strings.ToLower(include) == strings.ToLower(resource) {
			return true
		}
	}
	return false
}
