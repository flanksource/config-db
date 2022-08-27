package v1

type ICal struct {
	BaseScraper `json:",inline"`
	URL         string `json:"url,omitempty"`
}

type ICalConfig struct {
	ChangeType string  `json:"change_type"`
	Events     []Event `json:"events"`
}

type Event struct {
	Summary string `json:"description"`
	Date    string `json:"date"`
}
