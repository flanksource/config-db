package v1

type Postgres struct {
	BaseScraper `yaml:",inline" json:",inline"`
	Connection  `yaml:",inline" json:",inline"`

	// Permissions enables scraping PostgreSQL roles, memberships, and effective privileges.
	Permissions bool `json:"permissions,omitempty" yaml:"permissions,omitempty"`
}
