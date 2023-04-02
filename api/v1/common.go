package v1

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/flanksource/duty/types"
)

type Filter struct {
	JSONPath string `json:"jsonpath,omitempty"`
}

type Script struct {
	GoTemplate string `yaml:"gotemplate,omitempty" json:"gotemplate,omitempty"`
	JSONPath   string `yaml:"jsonpath,omitempty" json:"jsonpath,omitempty"`
	Expression string `yaml:"expr,omitempty" json:"expr,omitempty"`
	Javascript string `yaml:"javascript,omitempty" json:"javascript,omitempty"`
}

func (s Script) IsEmpty() bool {
	return s.GoTemplate == "" && s.JSONPath == "" && s.Expression == "" && s.Javascript == ""
}

func (s Script) String() string {
	if s.GoTemplate != "" {
		return "go: " + s.GoTemplate
	}
	if s.JSONPath != "" {
		return "jsonpath: " + s.JSONPath
	}
	if s.Expression != "" {
		return "expr: " + s.Expression
	}
	if s.Javascript != "" {
		return "js: " + s.Javascript
	}
	return ""
}

type MaskSelector struct {
	// Type is the config type to apply the mask
	Type string `json:"type,omitempty"`
}

func (s MaskSelector) IsEmpty() bool {
	return s.Type == ""
}

func (s MaskSelector) String() string {
	return fmt.Sprintf("type=%s", s.Type)
}

type Mask struct {
	Selector MaskSelector `json:"selector,omitempty"`
	JSONPath string       `json:"jsonpath,omitempty"`
	// Value can be a hash function name or just a string
	Value string `json:"value,omitempty"`
}

func (s Mask) IsEmpty() bool {
	return s.Selector.IsEmpty() && s.JSONPath == "" && s.Value == ""
}

func (s Mask) String() string {
	return fmt.Sprintf("selector=%s json_path=%s value=%s", s.Selector, s.JSONPath, s.Value)
}

type MaskList []Mask

func (s MaskList) IsEmpty() bool {
	for _, m := range s {
		if !m.IsEmpty() {
			return false
		}
	}

	return true
}

func (s MaskList) String() string {
	return fmt.Sprintf("total_masks=%d", len(s))
}

type Transform struct {
	Script  Script   `yaml:",inline" json:",inline"`
	Include []Filter `json:"include,omitempty"`
	// Fields to remove from the config, useful for removing sensitive data and fields
	// that change often without a material impact i.e. Last Scraped Time
	Exclude []Filter `json:"exclude,omitempty"`
	// Masks consist of configurations to replace sensitive fields
	// with hash functions or static string.
	Masks MaskList `json:"mask,omitempty"`
}

func (t Transform) IsEmpty() bool {
	return t.Script.IsEmpty() && len(t.Include) == 0 && len(t.Exclude) == 0 && t.Masks.IsEmpty()
}

func (t Transform) String() string {
	s := ""
	if !t.Script.IsEmpty() {
		s += fmt.Sprintf("script=%s", t.Script)
	}

	if !t.Masks.IsEmpty() {
		s += fmt.Sprintf("masks=%s", t.Masks)
	}

	if len(t.Include) > 0 {
		s += fmt.Sprintf(" include=%s", t.Include)
	}

	if len(t.Exclude) > 0 {
		s += fmt.Sprintf(" exclude=%s", t.Exclude)
	}
	return s
}

type BaseScraper struct {
	// A static value or JSONPath expression to use as the ID for the resource.
	ID string `json:"id,omitempty"`
	// A static value or JSONPath expression to use as the ID for the resource.
	Name string `json:"name,omitempty"`
	// A JSONPath expression to use to extract individual items from the resource,
	// items are extracted first and then the ID,Name,Type and transformations are applied for each item.
	Items string `json:"items,omitempty"`
	// A static value or JSONPath expression to use as the type for the resource.
	Type      string    `json:"type,omitempty"`
	Transform Transform `json:"transform,omitempty"`
	// Format of config item, defaults to JSON, available options are JSON, properties
	Format string `json:"format,omitempty"`

	// TimestampFormat is a Go time format string used to
	// parse timestamps in createFields and DeletedFields.
	// If not specified, the default is RFC3339.
	TimestampFormat string `json:"timestampFormat,omitempty"`

	// CreateFields is a list of JSONPath expression used to identify the created time of the config.
	// If multiple fields are specified, the first non-empty value will be used.
	CreateFields []string `json:"createFields,omitempty"`

	// DeleteFields is a JSONPath expression used to identify the deleted time of the config.
	// If multiple fields are specified, the first non-empty value will be used.
	DeleteFields []string      `json:"deleteFields,omitempty"`
	Tags         JSONStringMap `json:"tags,omitempty"`
}

func (base BaseScraper) String() string {
	s := fmt.Sprintf("id=%s name=%s type=%s", base.ID, base.Name, base.Type)

	if base.Format != "" {
		s += fmt.Sprintf(" format=%s", base.Format)
	}

	if base.Items != "" {
		s += fmt.Sprintf(" items=%s", base.Items)
	}

	if !base.Transform.IsEmpty() {
		s += fmt.Sprintf(" transform=%s", base.Transform)
	}

	return s
}

// Authentication ...
type Authentication struct {
	Username types.EnvVar `yaml:"username" json:"username"`
	Password types.EnvVar `yaml:"password" json:"password"`
}

// IsEmpty ...
func (auth Authentication) IsEmpty() bool {
	return auth.Username.IsEmpty() && auth.Password.IsEmpty()
}

// GetUsername ...
func (auth Authentication) GetUsername() string {
	return auth.Username.Value
}

// GetPassword ...
func (auth Authentication) GetPassword() string {
	return auth.Password.Value
}

// GetDomain ...
func (auth Authentication) GetDomain() string {
	parts := strings.Split(auth.GetUsername(), "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// AWSConnection ...
type AWSConnection struct {
	AccessKey     types.EnvVar `yaml:"accessKey,omitempty" json:"accessKey,omitempty"`
	SecretKey     types.EnvVar `yaml:"secretKey,omitempty" json:"secretKey,omitempty"`
	Region        []string     `yaml:"region,omitempty" json:"region"`
	Endpoint      string       `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	SkipTLSVerify bool         `yaml:"skipTLSVerify,omitempty" json:"skipTLSVerify,omitempty"`
	AssumeRole    string       `yaml:"assumeRole,omitempty" json:"assumeRole,omitempty"`
}

// GCPConnection ...
type GCPConnection struct {
	Endpoint    string        `yaml:"endpoint" json:"endpoint,omitempty"`
	Credentials *types.EnvVar `yaml:"credentials" json:"credentials,omitempty"`
}

type Connection struct {
	Connection     string         `yaml:"connection" json:"connection" template:"true"`
	Authentication Authentication `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// +k8s:deepcopy-gen=false
type Connectable interface {
	GetConnection() string
}

func (c Connection) GetConnection() string {
	return c.Connection
}

func (c Connection) GetEndpoint() string {
	return sanitizeEndpoints(c.Connection)
}

// Obfuscate passwords of the form ' password=xxxxx ' from connectionString since
// connectionStrings are used as metric labels and we don't want to leak passwords
// Returns the Connection string with the password replaced by '###'
func sanitizeEndpoints(connection string) string {
	if _url, err := url.Parse(connection); err == nil {
		if _url.User != nil {
			_url.User = nil
			connection = _url.String()
		}
	}
	//looking for a substring that starts with a space,
	//'password=', then any non-whitespace characters,
	//until an ending space
	re := regexp.MustCompile(`password=([^;]*)`)
	return re.ReplaceAllString(connection, "password=###")
}

type Template struct {
	Template   string `yaml:"template,omitempty" json:"template,omitempty"`
	JSONPath   string `yaml:"jsonPath,omitempty" json:"jsonPath,omitempty"`
	GSONPath   string `yaml:"gsonPath,omitempty" json:"gsonPath,omitempty"`
	Expression string `yaml:"expr,omitempty" json:"expr,omitempty"`
	Javascript string `yaml:"javascript,omitempty" json:"javascript,omitempty"`
}
