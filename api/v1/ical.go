package v1

type ICal struct {
	BaseScraper `json:",inline"`
	URL         string `json:"url,omitempty"`
}

type ICalConfig struct {
	URL string `json:"url,omitempty"`
}
