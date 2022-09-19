package v1

// ConfigScraper ...
type ConfigScraper struct {
	LogLevel string `json:"logLevel,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	AWS      []AWS  `json:"aws,omitempty" yaml:"aws,omitempty"`
	File     []File `json:"file,omitempty" yaml:"file,omitempty"`
}

// IsEmpty ...
func (c ConfigScraper) IsEmpty() bool {
	return len(c.AWS) == 0 && len(c.File) == 0
}

func (c ConfigScraper) IsTrace() bool {
	return c.LogLevel == "trace"
}
