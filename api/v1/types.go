package v1

// ConfigScraper ...
type ConfigScraper struct {
	Schedule string `json:"schedule,omitempty"`
	AWS      []AWS  `json:"aws,omitempty" yaml:"aws,omitempty"`
	File     string `json:"file,omitempty" yaml:"file,omitempty"`
}

// IsEmpty ...
func (c ConfigScraper) IsEmpty() bool {
	return len(c.AWS) == 0
}
