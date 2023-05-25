package trivy

import (
	"time"
)

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
	Target            string                  `json:"Target"`
	Class             string                  `json:"Class"`
	Type              string                  `json:"Type"`
	Vulnerabilities   DetectedVulnerabilities `json:"Vulnerabilities"`
	MisconfSummary    *MisconfSummary         `json:"MisconfSummary"`
	Misconfigurations []Misconfiguration      `json:"Misconfigurations"`
}

// SourceID represents data source such as NVD.
type SourceID string

type Severity int

type VendorSeverity map[SourceID]Severity

type CVSS struct {
	V2Vector string  `json:"V2Vector,omitempty"`
	V3Vector string  `json:"V3Vector,omitempty"`
	V2Score  float64 `json:"V2Score,omitempty"`
	V3Score  float64 `json:"V3Score,omitempty"`
}

type VendorCVSS map[SourceID]CVSS

type Vulnerability struct {
	Title            string         `json:",omitempty"`
	Description      string         `json:",omitempty"`
	Severity         string         `json:",omitempty"` // Selected from VendorSeverity, depending on a scan target
	CweIDs           []string       `json:",omitempty"` // e.g. CWE-78, CWE-89
	VendorSeverity   VendorSeverity `json:",omitempty"`
	CVSS             VendorCVSS     `json:",omitempty"`
	References       []string       `json:",omitempty"`
	PublishedDate    *time.Time     `json:",omitempty"` // Take from NVD
	LastModifiedDate *time.Time     `json:",omitempty"` // Take from NVD

	// Custom is basically for extensibility and is not supposed to be used in OSS
	Custom any `json:",omitempty"`
}

type DetectedVulnerability struct {
	VulnerabilityID  string   `json:",omitempty"`
	VendorIDs        []string `json:",omitempty"`
	PkgID            string   `json:",omitempty"` // It is used to construct dependency graph.
	PkgName          string   `json:",omitempty"`
	PkgPath          string   `json:",omitempty"` // It will be filled in the case of language-specific packages such as egg/wheel and gemspec
	InstalledVersion string   `json:",omitempty"`
	FixedVersion     string   `json:",omitempty"`
	Layer            Layer    `json:",omitempty"`
	SeveritySource   string   `json:",omitempty"`
	PrimaryURL       string   `json:",omitempty"`
	Ref              string   `json:",omitempty"`

	// DataSource holds where the advisory comes from
	DataSource *DataSource `json:",omitempty"`

	// Custom is for extensibility and not supposed to be used in OSS
	Custom any `json:",omitempty"`

	// Embed vulnerability details
	Vulnerability
}

type DetectedVulnerabilities []DetectedVulnerability

func (t DetectedVulnerabilities) GroupByPkg() map[string][]DetectedVulnerability {
	output := make(map[string][]DetectedVulnerability)
	for _, v := range t {
		output[v.PkgName] = append(output[v.PkgName], v)
	}

	return output
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
