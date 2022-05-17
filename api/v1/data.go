package v1

// Host ...
type Host interface {
	GetHostname() string
	GetPlatform() string
	GetId() string
	GetIP() string
	GetPatches() []Patch
}

// Patch ...
type Patch interface {
	GetName() string
	GetVersion() string
	GetTitle() string
	IsInstalled() bool
	IsMissing() bool
	IsPendingReboot() bool
	IsFailed() bool
}

// Properties ...
type Properties []Property

// Property ...
type Property struct {
	Name string `json:"name"`
	// Line comments or description associated with this property
	Description  string        `json:"description,omitempty"`
	Value        string        `json:"value,omitempty"`
	Type         string        `json:"type,omitempty"`
	GitLocation  *GitLocation  `json:"location,omitempty"`
	FileLocation *FileLocation `json:"fileLocation,omitempty"`
	// A path to an OpenAPI spec and fieldRef that describes the field
	OpenAPI *OpenAPIFieldRef `json:"openapiRef,omitempty"`
}

// FileLocation ...
type FileLocation struct {
	Host       string `json:"host,omitempty"`
	FilePath   string `json:"filePath"`
	LineNumber int    `json:"lineNumber"`
}

// GitLocation ...
type GitLocation struct {
	Repository string `json:"repository"`
	FilePath   string `json:"filePath"`
	LineNumber int    `json:"lineNumber"`
	GitRef     string `json:"gitRef"`
}

// OpenAPIFieldRef ...
type OpenAPIFieldRef struct {
	// Location of the OpenAPI spec
	Location string `json:"location,omitempty"`
	// Reference to the field
	FieldRef string `json:"fieldRef,omitempty"`
}
