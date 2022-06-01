package v1

// File ...
type File struct {
	ID    string   `json:"id,omitempty"`
	Type  string   `json:"type,omitempty"`
	URL   string   `json:"url,omitempty"`
	Paths []string `json:"paths,omitempty"`
}
