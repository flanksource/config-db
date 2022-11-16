package v1

type SQL struct {
	BaseScraper `json:",inline"`
	Connection  `json:",inline"`
	Driver      string `json:"driver,omitempty"`
	Query       string `json:"query"`
}
