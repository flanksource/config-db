package trivy

import "time"

type TrivyResponse struct {
	ClusterName       string     `json:"ClusterName"`
	Vulnerabilities   []Resource `json:"Vulnerabilities"`
	Misconfigurations []Resource `json:"Misconfigurations"`
}

type Resource struct {
	Namespace string   `json:"Namespace"`
	Kind      string   `json:"Kind"`
	Name      string   `json:"Name"`
	Results   []Result `json:"Results,omitempty"`
}

type Result struct {
	Target            string             `json:"Target"`
	Class             string             `json:"Class"`
	Type              string             `json:"Type"`
	Vulnerabilities   []Vulnerability    `json:"Vulnerabilities"`
	MisconfSummary    MisconfSummary     `json:"MisconfSummary"`
	Misconfigurations []Misconfiguration `json:"Misconfigurations"`
}

type Vulnerability struct {
	VulnerabilityID  string          `json:"VulnerabilityID"`
	PkgID            string          `json:"PkgID"`
	PkgName          string          `json:"PkgName"`
	InstalledVersion string          `json:"InstalledVersion"`
	Layer            Layer           `json:"Layer"`
	SeveritySource   string          `json:"SeveritySource"`
	PrimaryURL       string          `json:"PrimaryURL"`
	DataSource       DataSource      `json:"DataSource"`
	Title            string          `json:"Title,omitempty"`
	Description      string          `json:"Description"`
	Severity         string          `json:"Severity"`
	CweIDs           []string        `json:"CweIDs,omitempty"`
	Cvss             map[string]Cvss `json:"CVSS,omitempty"`
	References       []string        `json:"References"`
	PublishedDate    time.Time       `json:"PublishedDate"`
	LastModifiedDate time.Time       `json:"LastModifiedDate"`
}

type Layer struct {
	Digest string `json:"Digest"`
	DiffID string `json:"DiffID"`
}

type DataSource struct {
	ID   string `json:"ID"`
	Name string `json:"Name"`
	URL  string `json:"URL"`
}

type Cvss struct {
	V2Vector string  `json:"V2Vector"`
	V3Vector string  `json:"V3Vector"`
	V2Score  float64 `json:"V2Score"`
	V3Score  float64 `json:"V3Score"`
}

type MisconfSummary struct {
	Successes  int `json:"Successes"`
	Failures   int `json:"Failures"`
	Exceptions int `json:"Exceptions"`
}

type Misconfiguration struct {
	Type          string        `json:"Type"`
	ID            string        `json:"ID"`
	Avdid         string        `json:"AVDID"`
	Title         string        `json:"Title"`
	Description   string        `json:"Description"`
	Message       string        `json:"Message"`
	Namespace     string        `json:"Namespace"`
	Query         string        `json:"Query"`
	Resolution    string        `json:"Resolution"`
	Severity      string        `json:"Severity"`
	PrimaryURL    string        `json:"PrimaryURL"`
	References    []string      `json:"References"`
	Status        string        `json:"Status"`
	Layer         struct{}      `json:"Layer"`
	CauseMetadata CauseMetadata `json:"CauseMetadata"`
}

type CauseMetadata struct {
	Provider  string `json:"Provider"`
	Service   string `json:"Service"`
	StartLine int    `json:"StartLine"`
	EndLine   int    `json:"EndLine"`
	Code      Code   `json:"Code"`
}

type Code struct {
	Lines []CodeLine `json:"Lines"`
}

type CodeLine struct {
	Number     int    `json:"Number"`
	Content    string `json:"Content"`
	IsCause    bool   `json:"IsCause"`
	Annotation string `json:"Annotation"`
	Truncated  bool   `json:"Truncated"`
	FirstCause bool   `json:"FirstCause"`
	LastCause  bool   `json:"LastCause"`
}
