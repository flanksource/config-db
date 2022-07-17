package v1

// File ...
type File struct {
	BaseScraper `json:",inline"`
	URL         string   `json:"url,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	Ignore      []string `json:"ignore,omitempty"`
}
