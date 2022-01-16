package v1

type AWS struct {
	*AWSConnection
	PatchStates         bool `json:"patch_states,omitempty"`
	PatchDetails        bool `json:"patch_details,omitempty"`
	Inventory           bool `json:"inventory,omitempty"`
	Compliance          bool `json:"compliance,omitempty"`
	TrustedAdvisorCheck bool `json:"trusted_advisor_check,omitempty"`
}
