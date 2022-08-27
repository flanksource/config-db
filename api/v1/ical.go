package v1

type ICal struct {
	BaseScraper `json:",inline"`
	URL         string `json:"url,omitempty"`
}

type ICalConfig struct {
	Events []Event `json:"events"`
}

type Event struct {
	Summary string `json:"description"`
	Date    string `json:"date"`
}
