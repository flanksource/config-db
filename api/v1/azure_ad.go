package v1

type AzureAD struct {
	BaseScraper      `json:",inline"`
	AzureConnection  `yaml:",inline" json:",inline"`
	Users            AzureUsers            `yaml:"users,omitempty" json:"users,omitempty"`
	Groups           AzureGroups           `yaml:"groups,omitempty" json:"groups,omitempty"`
	AppRegistrations AzureAppRegistrations `yaml:"appRegistrations,omitempty" json:"appRegistrations,omitempty"`
	Logins           AzureLogins           `yaml:"logins,omitempty" json:"logins,omitempty"`
}

type CELFilter string

type MsGraphFilter struct {
	Filter []CELFilter `yaml:"filter,omitempty" json:"filter,omitempty"`
	// MS.Graph query string
	Query string `yaml:"query,omitempty" json:"query,omitempty"`
}

type AzureLogins struct {
	MsGraphFilter `yaml:",inline" json:",inline"`
}

type AzureUsers struct {
	MsGraphFilter `yaml:",inline" json:",inline"`
}

type AzureGroups struct {
	MsGraphFilter `yaml:",inline" json:",inline"`
}

type AzureAppRegistrations struct {
	MsGraphFilter `yaml:",inline" json:",inline"`
}
