package v1

import (
	"strings"
	"time"
)

// AWS ...
type AWS struct {
	*AWSConnection
	PatchStates         bool       `json:"patch_states,omitempty"`
	PatchDetails        bool       `json:"patch_details,omitempty"`
	Inventory           bool       `json:"inventory,omitempty"`
	Compliance          bool       `json:"compliance,omitempty"`
	CloudTrail          CloudTrail `json:"cloudtrail,omitempty"`
	TrustedAdvisorCheck bool       `json:"trusted_advisor_check,omitempty"`
	Include             []string   `json:"include,omitempty"`
	Exclude             []string   `json:"exclude,omitempty"`
	BaseScraper         `json:",inline"`
}

type CloudTrail struct {
	Exclude []string       `json:"exclude,omitempty"`
	MaxAge  *time.Duration `json:"max_age,omitempty"`
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

func (aws AWS) Excludes(resource string) bool {
	if len(aws.Exclude) == 0 {
		return false
	}
	for _, exclude := range aws.Exclude {
		if strings.ToLower(exclude) == strings.ToLower(resource) {
			return true
		}
	}
	return false
}
