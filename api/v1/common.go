package v1

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
)

// ConfigFieldExclusion defines fields with JSONPath that needs to
// be removed from the config.
type ConfigFieldExclusion struct {
	// Optionally specify the config types
	// from which the JSONPath fields need to be removed.
	// If left empty, all config types are considered.
	Types []string `json:"types,omitempty"`

	JSONPath string `json:"jsonpath"`
}

type Script struct {
	GoTemplate string `yaml:"gotemplate,omitempty" json:"gotemplate,omitempty"`
	JSONPath   string `yaml:"jsonpath,omitempty" json:"jsonpath,omitempty"`
	Expression string `yaml:"expr,omitempty" json:"expr,omitempty"`
	Javascript string `yaml:"javascript,omitempty" json:"javascript,omitempty"`
}

func (s Script) ToGomplate() gomplate.Template {
	return gomplate.Template{
		Template:   s.GoTemplate,
		JSONPath:   s.JSONPath,
		Javascript: s.Javascript,
		Expression: s.Expression,
	}
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

type Mask struct {
	// Selector is a CEL expression that selects on what config items to apply the mask.
	Selector string `json:"selector,omitempty"`
	// JSONPath specifies what field in the config needs to be masked
	JSONPath string `json:"jsonpath,omitempty"`
	// Value can be a hash function name or just a string
	Value string `json:"value,omitempty"`
}

func (s Mask) IsEmpty() bool {
	return s.Selector == "" && s.JSONPath == "" && s.Value == ""
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

type ChangeMapping struct {
	// Filter selects what change to apply the mapping to
	Filter string `json:"filter,omitempty"`
	// Type is the type to be set on the change
	Type string `json:"type,omitempty"`
}

type TransformChange struct {
	// Mapping is a list of CEL expressions that maps a change to the specified type
	Mapping []ChangeMapping `json:"mapping,omitempty"`
	// Exclude is a list of CEL expressions that excludes a given change
	Exclude []string `json:"exclude,omitempty"`
}

func (t *TransformChange) IsEmpty() bool {
	return len(t.Exclude) == 0
}

// RelationshipLookup offers different ways to specify a lookup value
type RelationshipLookup struct {
	Expr  string `json:"expr,omitempty"`
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
}

func (t *RelationshipLookup) Eval(labels map[string]string, envVar map[string]any) (string, error) {
	if t.Value != "" {
		return t.Value, nil
	}

	if t.Label != "" {
		return labels[t.Label], nil
	}

	if t.Expr != "" {
		res, err := gomplate.RunTemplate(envVar, gomplate.Template{Expression: t.Expr})
		if err != nil {
			return "", err
		}

		return res, nil
	}

	return "", nil
}

func (t RelationshipLookup) IsEmpty() bool {
	return t.Value == "" && t.Label == "" && t.Expr == ""
}

// RelationshipSelector is the evaluated output of RelationshipSelector.
type RelationshipSelector struct {
	ID     string            `json:"id,omitempty"`
	Name   string            `json:"name,omitempty"`
	Type   string            `json:"type,omitempty"`
	Agent  string            `json:"agent,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (t *RelationshipSelector) IsEmpty() bool {
	return t.ID == "" && t.Name == "" && t.Type == "" && t.Agent == "" && len(t.Labels) == 0
}

func (t *RelationshipSelector) ToResourceSelector() types.ResourceSelector {
	var labelSelector string
	for k, v := range t.Labels {
		labelSelector += fmt.Sprintf("%s=%s,", k, v)
	}
	labelSelector = strings.TrimSuffix(labelSelector, ",")

	return types.ResourceSelector{
		ID:            t.ID,
		Name:          t.Name,
		Types:         []string{t.Type},
		Agent:         t.Agent,
		LabelSelector: labelSelector,
	}
}

type RelationshipSelectorTemplate struct {
	ID   RelationshipLookup `json:"id,omitempty"`
	Name RelationshipLookup `json:"name,omitempty"`
	Type RelationshipLookup `json:"type,omitempty"`
	// Agent can be one of
	//  - agent id
	//  - agent name
	//  - 'self' (no agent)
	Agent  RelationshipLookup `json:"agent,omitempty"`
	Labels map[string]string  `json:"labels,omitempty"`
}

func (t *RelationshipSelectorTemplate) IsEmpty() bool {
	return t.ID.IsEmpty() && t.Name.IsEmpty() && t.Type.IsEmpty() && t.Agent.IsEmpty() && len(t.Labels) == 0
}

// Eval evaluates the template and returns a RelationshipSelector.
// If any of the filter returns an empty value, the evaluation results to a nil selector.
// i.e. if a lookup is non-empty, it must return a non-empty value.
func (t *RelationshipSelectorTemplate) Eval(labels map[string]string, env map[string]any) (*RelationshipSelector, error) {
	if t.IsEmpty() {
		return nil, nil
	}

	var output RelationshipSelector
	var err error

	if !t.ID.IsEmpty() {
		if output.ID, err = t.ID.Eval(labels, env); err != nil {
			return nil, fmt.Errorf("failed to evaluate id: %v for config relationship: %w", t.ID, err)
		} else if output.ID == "" {
			return nil, nil
		}
	}

	if !t.Name.IsEmpty() {
		if output.Name, err = t.Name.Eval(labels, env); err != nil {
			return nil, fmt.Errorf("failed to evaluate name: %v for config relationship: %w", t.Name, err)
		} else if output.Name == "" {
			return nil, nil
		}
	}

	if !t.Type.IsEmpty() {
		if output.Type, err = t.Type.Eval(labels, env); err != nil {
			return nil, fmt.Errorf("failed to evaluate type: %v for config relationship: %w", t.Type, err)
		} else if output.Type == "" {
			return nil, nil
		}
	}

	if !t.Agent.IsEmpty() {
		if output.Agent, err = t.Agent.Eval(labels, env); err != nil {
			return nil, fmt.Errorf("failed to evaluate agent_id: %v for config relationship: %w", t.Agent, err)
		} else if output.Agent == "" {
			return nil, nil
		}
	}

	return &output, nil
}

type RelationshipConfig struct {
	RelationshipSelectorTemplate `json:",inline"`
	// Alternately, a single cel-expression can be used
	// that returns a list of relationship selector.
	Expr string `json:"expr,omitempty"`
	// Filter is a CEL expression that selects on what config items
	// the relationship needs to be applied
	Filter string `json:"filter,omitempty"`
}

type Transform struct {
	Script Script `yaml:",inline" json:",inline"`
	// Fields to remove from the config, useful for removing sensitive data and fields
	// that change often without a material impact i.e. Last Scraped Time
	Exclude []ConfigFieldExclusion `json:"exclude,omitempty"`
	// Masks consist of configurations to replace sensitive fields
	// with hash functions or static string.
	Masks MaskList `json:"mask,omitempty"`
	// Relationship allows you to form relationships between config items using selectors.
	Relationship []RelationshipConfig `json:"relationship,omitempty"`
	Change       TransformChange      `json:"changes,omitempty"`
}

func (t Transform) IsEmpty() bool {
	return t.Script.IsEmpty() && t.Change.IsEmpty() && len(t.Exclude) == 0 && t.Masks.IsEmpty() && len(t.Relationship) == 0
}

func (t Transform) String() string {
	s := ""
	if !t.Script.IsEmpty() {
		s += fmt.Sprintf("script=%s", t.Script)
	}

	if !t.Masks.IsEmpty() {
		s += fmt.Sprintf(" masks=%s", t.Masks)
	}

	if len(t.Exclude) > 0 {
		s += fmt.Sprintf(" exclude=%s", t.Exclude)
	}

	if !t.Change.IsEmpty() {
		s += fmt.Sprintf(" change=%s", t.Change)
	}

	s += fmt.Sprintf(" relationships=%d", len(t.Relationship))
	return s
}

type ConfigProperties struct {
	types.Property `yaml:",inline" json:",inline"`

	Filter string `json:"filter,omitempty"`
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
	Type string `json:"type,omitempty"`
	// A static value or JSONPath expression to use as the class for the resource.
	Class string `json:"class,omitempty"`

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
	DeleteFields []string `json:"deleteFields,omitempty"`

	// Tags allow you to set custom tags on the scraped config items.
	Tags JSONStringMap `json:"tags,omitempty"`

	// Properties are custom templatable properties for the scraped config items
	// grouped by the config type.
	Properties []ConfigProperties `json:"properties,omitempty" template:"true"`
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
	return auth.Username.ValueStatic
}

// GetPassword ...
func (auth Authentication) GetPassword() string {
	return auth.Password.ValueStatic
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
	// ConnectionName of the connection. It'll be used to populate the endpoint, accessKey and secretKey.
	ConnectionName string       `yaml:"connection,omitempty" json:"connection,omitempty"`
	AccessKey      types.EnvVar `yaml:"accessKey,omitempty" json:"accessKey,omitempty"`
	SecretKey      types.EnvVar `yaml:"secretKey,omitempty" json:"secretKey,omitempty"`
	Region         []string     `yaml:"region,omitempty" json:"region"`
	Endpoint       string       `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	SkipTLSVerify  bool         `yaml:"skipTLSVerify,omitempty" json:"skipTLSVerify,omitempty"`
	AssumeRole     string       `yaml:"assumeRole,omitempty" json:"assumeRole,omitempty"`
}

func (aws AWSConnection) GetModel() *models.Connection {
	return &models.Connection{
		URL:      aws.Endpoint,
		Username: aws.AccessKey.String(),
		Password: aws.SecretKey.String(),
		Properties: types.JSONStringMap{
			"region":     strings.Join(aws.Region, ","),
			"assumeRole": aws.AssumeRole,
		},
		InsecureTLS: aws.SkipTLSVerify,
	}
}

// GCPConnection ...
type GCPConnection struct {
	Endpoint    string        `yaml:"endpoint" json:"endpoint,omitempty"`
	Credentials *types.EnvVar `yaml:"credentials" json:"credentials,omitempty"`
}

func (gcp GCPConnection) GetModel() *models.Connection {
	return &models.Connection{
		URL:         gcp.Endpoint,
		Certificate: gcp.Credentials.String(),
	}
}

type Connection struct {
	// Connection is either the name of the connection to lookup
	// or the connection string itself.
	Connection     string         `yaml:"connection" json:"connection" template:"true"`
	Authentication Authentication `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// +k8s:deepcopy-gen=false
type Connectable interface {
	GetConnection() string
}

func (c Connection) GetModel() *models.Connection {
	return &models.Connection{
		URL:      c.Connection,
		Username: c.Authentication.Username.String(),
		Password: c.Authentication.Password.String(),
	}
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
