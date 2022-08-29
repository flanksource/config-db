package v1

import "fmt"

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

func (e Event) String() string {
	return fmt.Sprintf("%s (%s)", e.Summary, e.Date)
}
