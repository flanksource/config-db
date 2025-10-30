package v1

// OpenSSFScorecard scraper fetches security scorecard data from the OpenSSF Scorecard API
type OpenSSFScorecard struct {
	BaseScraper `json:",inline" yaml:",inline"`

	// Repositories is the list of repositories to assess
	Repositories []OpenSSFRepository `yaml:"repositories" json:"repositories"`

	// MinScore optionally filters repositories by minimum score (0-10)
	MinScore *float64 `yaml:"minScore,omitempty" json:"minScore,omitempty"`
}

// OpenSSFRepository specifies a repository to assess
type OpenSSFRepository struct {
	Owner string `yaml:"owner" json:"owner"`
	Repo  string `yaml:"repo" json:"repo"`
}
