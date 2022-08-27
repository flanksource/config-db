package v1

// ConfigScraper ...
type ConfigScraper struct {
	Schedule string `json:"schedule,omitempty"`
	AWS      []AWS  `json:"aws,omitempty" yaml:"aws,omitempty"`
	File     []File `json:"file,omitempty" yaml:"file,omitempty"`
	ICal     ICal   `json:"ical,omitempty" yaml:"ical,omitempty"`
}

// IsEmpty ...
func (c ConfigScraper) IsEmpty() bool {
	return len(c.AWS) == 0 && len(c.File) == 0
}
