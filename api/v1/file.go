package v1

// File ...
type File struct {
	ID   string   `json:"id"`
	Type string   `json:"type"`
	Glob []string `json:"path"`
}
