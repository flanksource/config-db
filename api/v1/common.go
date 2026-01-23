package v1

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
	"github.com/samber/lo"
)

// List of types which should not have scraper_id
var ScraperLessTypes = []string{AWSRegion, AWSAvailabilityZone}

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

func (s Script) PrettyShort() api.Text {
	t := clicky.Text("")
	if s.GoTemplate != "" {
		t = t.Append("go: ", "text-green-600").Append(clicky.CodeBlock("go", lo.Ellipsis(s.GoTemplate, 200)))
	} else if s.JSONPath != "" {
		t = t.Append("jsonpath: ", "text-blue-600").Append(clicky.CodeBlock("jsonpath", lo.Ellipsis(s.JSONPath, 200)))
	} else if s.Expression != "" {
		t = t.Append("expr: ", "text-yellow-600").Append(clicky.CodeBlock("cel", lo.Ellipsis(s.Expression, 200)))
	} else if s.Javascript != "" {
		t = t.Append("js: ", "text-purple-600").Append(clicky.CodeBlock("javascript", lo.Ellipsis(s.Javascript, 200)))
	}
	return t
}

func (s Script) Pretty() api.Text {
	t := clicky.Text("")
	if s.GoTemplate != "" {
		t = t.Append("go: ", "text-green-600").Append(clicky.CodeBlock("go", s.GoTemplate))
	} else if s.JSONPath != "" {
		t = t.Append("jsonpath: ", "text-blue-600").Append(clicky.CodeBlock("jsonpath", s.JSONPath))
	} else if s.Expression != "" {
		t = t.Append("expr: ", "text-yellow-600").Append(clicky.CodeBlock("cel", s.Expression))
	} else if s.Javascript != "" {
		t = t.Append("js: ", "text-purple-600").Append(clicky.CodeBlock("javascript", s.Javascript))
	}
	return t
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
	// Severity is the severity to be set on the change
	Severity string `json:"severity,omitempty"`
	// Type is the type to be set on the change
	Type string `json:"type,omitempty"`
	// Action allows performing actions on the corresponding config item
	// based on this change. Example: You can map EC2 instance's "TerminateInstances"
	// change event to delete the corresponding EC2 instance config.
	// 	Allowed actions: "delete", "ignore"
	Action ChangeAction `json:"action,omitempty"`
	// Summary replaces the existing change summary.
	Summary string `json:"summary,omitempty"`
}

type TransformChange struct {
	// Mapping is a list of CEL expressions that maps a change to the specified type
	Mapping []ChangeMapping `json:"mapping,omitempty"`
	// Exclude is a list of CEL expressions that excludes a given change
	Exclude []string `json:"exclude,omitempty"`
}

func (t *TransformChange) IsEmpty() bool {
	return len(t.Exclude) == 0 && len(t.Mapping) == 0
}

type RelationshipConfig struct {
	duty.RelationshipSelectorTemplate `json:",inline"`
	// Alternately, a single cel-expression can be used
	// that returns a list of relationship selector.
	Expr string `json:"expr,omitempty"`
	// Filter is a CEL expression that selects on what config items
	// the relationship needs to be applied
	Filter string `json:"filter,omitempty"`
	// Parent sets all the configs found by the selector
	// as the parent of the configs passed by the filter
	Parent bool `json:"parent,omitempty"`
}

type Transform struct {
	Script `yaml:",inline" json:",inline"`
	// Fields to remove from the config, useful for removing sensitive data and fields
	// that change often without a material impact i.e. Last Scraped Time
	Exclude []ConfigFieldExclusion `json:"exclude,omitempty"`
	// Masks consist of configurations to replace sensitive fields
	// with hash functions or static string.
	Masks MaskList `json:"mask,omitempty"`
	// Relationship allows you to form relationships between config items using selectors.
	Relationship []RelationshipConfig `json:"relationship,omitempty"`
	Change       TransformChange      `json:"changes,omitempty"`

	Locations []LocationOrAlias `json:"locations,omitempty"`
	Aliases   []LocationOrAlias `json:"aliases,omitempty"`
}

func (t Transform) IsEmpty() bool {
	return t.Script.IsEmpty() && t.Change.IsEmpty() && len(t.Exclude) == 0 && t.Masks.IsEmpty() && len(t.Relationship) == 0 &&
		len(t.Locations) == 0 && len(t.Aliases) == 0
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

	if len(t.Locations) > 0 {
		s += fmt.Sprintf(" locations=%d", len(t.Locations))
	}

	if len(t.Aliases) > 0 {
		s += fmt.Sprintf(" aliases=%d", len(t.Aliases))
	}

	return s
}

type ConfigProperties struct {
	types.Property `yaml:",inline" json:",inline"`

	Filter string `json:"filter,omitempty"`
}

type CustomScraperBase struct {
	// A static value or JSONPath expression to use as the ID for the resource.
	ID string `json:"id,omitempty"`

	// A static value or JSONPath expression to use as the Name for the resource.
	Name string `json:"name,omitempty"`

	// A static value or JSONPath expression to use as the description for the resource.
	Description string `json:"description,omitempty"`

	// A JSONPath expression to use to extract individual items from the resource,
	// items are extracted first and then the ID,Name,Type and transformations are applied for each item.
	Items string `json:"items,omitempty"`

	// A static value or JSONPath expression to use as the type for the resource.
	Type string `json:"type,omitempty"`

	// A static value or JSONPath expression to use as the class for the resource.
	Class string `json:"class,omitempty"`

	// Format of config item, defaults to JSON, available options are JSON, properties
	Format string `json:"format,omitempty"`

	// A static value or JSONPath expression to use as the status of the config item
	Status string `json:"status,omitempty"`

	// A static value or JSONPath expression to use as the health of the config item
	Health string `json:"health,omitempty"`

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
}

type BaseScraper struct {
	CustomScraperBase `yaml:",inline" json:",inline"`

	Transform Transform `json:"transform,omitempty"`

	// Labels for each config item.
	Labels JSONStringMap `json:"labels,omitempty"`

	// Tags for each config item.
	// Max allowed: 5
	Tags Tags `json:"tags,omitempty"`

	// Properties are custom templatable properties for the scraped config items
	// grouped by the config type.
	Properties []ConfigProperties `json:"properties,omitempty" template:"true"`
}

func (base BaseScraper) ApplyPlugins(plugins ...ScrapePluginSpec) BaseScraper {
	for _, p := range plugins {
		base.Transform.Change.Exclude = append(base.Transform.Change.Exclude, p.Change.Exclude...)
		base.Transform.Change.Mapping = append(base.Transform.Change.Mapping, p.Change.Mapping...)

		base.Transform.Locations = append(base.Transform.Locations, p.Locations...)
		base.Transform.Aliases = append(base.Transform.Aliases, p.Aliases...)

		base.Transform.Relationship = append(base.Transform.Relationship, p.Relationship...)
		base.Properties = append(base.Properties, p.Properties...)
	}

	return base
}

func (base BaseScraper) WithoutTransform() BaseScraper {
	base.Transform = Transform{}
	return base
}

func (base BaseScraper) String() string {
	s := fmt.Sprintf("id=%s name=%s type=%s", base.ID, base.Name, base.Type)

	if base.Description != "" {
		s += fmt.Sprintf(" description=%s", base.Description)
	}

	if base.Status != "" {
		s += fmt.Sprintf(" status=%s", base.Status)
	}

	if base.Health != "" {
		s += fmt.Sprintf(" health=%s", base.Health)
	}

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

// AWSConnection is a mirror or duty's AWSConnection.
// It has a slice of []region instead of duty's single Region field.
type AWSConnection struct {
	// ConnectionName of the connection. It'll be used to populate the endpoint, accessKey and secretKey.
	ConnectionName string       `yaml:"connection,omitempty" json:"connection,omitempty"`
	AccessKey      types.EnvVar `yaml:"accessKey,omitempty" json:"accessKey,omitempty"`
	SecretKey      types.EnvVar `yaml:"secretKey,omitempty" json:"secretKey,omitempty"`
	AssumeRole     string       `yaml:"assumeRole,omitempty" json:"assumeRole,omitempty"`
	Endpoint       string       `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	// Skip TLS verify when connecting to aws
	SkipTLSVerify bool `yaml:"skipTLSVerify,omitempty" json:"skipTLSVerify,omitempty"`

	Regions []string `yaml:"region,omitempty" json:"region,omitempty"`
}

func (aws AWSConnection) ToDutyAWSConnection(region string) *connection.AWSConnection {
	return &connection.AWSConnection{
		ConnectionName: aws.ConnectionName,
		AccessKey:      aws.AccessKey,
		SecretKey:      aws.SecretKey,
		AssumeRole:     aws.AssumeRole,
		Endpoint:       aws.Endpoint,
		SkipTLSVerify:  aws.SkipTLSVerify,
		Region:         region,
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

func (c Connection) Pretty() api.Text {
	t := clicky.Text("")
	if c.Connection != "" {
		clicky.RedactSecretValues()
		t = t.Append(sanitizeEndpoints(c.Connection))
	}
	return t
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

type ChangeExtractionMapping struct {
	CreatedAt types.ValueExpression `yaml:"createdAt,omitempty" json:"createdAt,omitempty"`
	Severity  types.ValueExpression `yaml:"severity,omitempty" json:"severity,omitempty"`
	Summary   types.ValueExpression `yaml:"summary,omitempty" json:"summary,omitempty"`
	Type      types.ValueExpression `yaml:"type,omitempty" json:"type,omitempty"`

	// Details of the change in json format.
	// Defaults to the text.
	Details types.ValueExpression `yaml:"details,omitempty" json:"details,omitempty"`

	// TimeFormat is the go time format for the `createdAt` field.
	// Defaults to RFC3339.
	TimeFormat string `yaml:"timeFormat,omitempty" json:"timeFormat,omitempty"`
}

type ChangeExtractionRule struct {
	// Regexp to capture the fields from the text.
	// Captured fields are available in the templates.
	Regexp string `yaml:"regexp,omitempty" json:"regexp,omitempty"`

	// Mapping defines the Change to be extracted from the text.
	Mapping *ChangeExtractionMapping `yaml:"mapping,omitempty" json:"mapping,omitempty"`

	// Config is a list of selectors to attach the change to.
	// +kubebuilder:validation:MinItems=1
	Config []types.EnvVarResourceSelector `yaml:"config" json:"config"`
}

type LocationOrAlias struct {
	// Types on which this plugin should run.
	// Supports match expression
	// Example: AWS::*, Kubernetes::Namespace
	Type types.MatchExpression `json:"type"`

	// A Cel expression, when provided, must return true for this filter to apply.
	//
	// Receives the config item as the cel env variable.
	Filter types.CelExpression `json:"filter,omitempty"`

	Values []string `json:"values,omitempty" template:"true"`

	// The type of the parent to be used
	WithParent string `json:"withParent,omitempty"`
}
