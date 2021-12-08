package v1

type ConfigScraper struct {
	AWS []AWS `json:"aws,omitempty" yaml:"aws,omitempty"`
}

func (c ConfigScraper) IsEmpty() bool {
	return len(c.AWS) == 0
}
