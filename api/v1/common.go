package v1

import (
	"strings"

	"github.com/flanksource/kommons"
)

type Filter struct {
	JSONPath string `json:"jsonpath,omitempty"`
}

type Transform struct {
	Include []Filter `json:"include,omitempty"`
	Exclude []Filter `json:"exclude,omitempty"`
}

type BaseScraper struct {
	ID        string    `json:"id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Items     string    `json:"items,omitempty"`
	Type      string    `json:"type,omitempty"`
	Transform Transform `json:"transform,omitempty"`
}

// Authentication ...
type Authentication struct {
	Username kommons.EnvVar `yaml:"username" json:"username"`
	Password kommons.EnvVar `yaml:"password" json:"password"`
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
	AccessKey     kommons.EnvVar `yaml:"accessKey,omitempty" json:"accessKey,omitempty"`
	SecretKey     kommons.EnvVar `yaml:"secretKey,omitempty" json:"secretKey,omitempty"`
	Region        string         `yaml:"region,omitempty" json:"region"`
	Endpoint      string         `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	SkipTLSVerify bool           `yaml:"skipTLSVerify,omitempty" json:"skipTLSVerify,omitempty"`
	AssumeRole    string         `yaml:"assumeRole,omitempty" json:"assumeRole,omitempty"`
}

// GCPConnection ...
type GCPConnection struct {
	Endpoint    string          `yaml:"endpoint" json:"endpoint,omitempty"`
	Credentials *kommons.EnvVar `yaml:"credentials" json:"credentials,omitempty"`
}

type Template struct {
	Template   string `yaml:"template,omitempty" json:"template,omitempty"`
	JSONPath   string `yaml:"jsonPath,omitempty" json:"jsonPath,omitempty"`
	GSONPath   string `yaml:"gsonPath,omitempty" json:"gsonPath,omitempty"`
	Expression string `yaml:"expr,omitempty" json:"expr,omitempty"`
	Javascript string `yaml:"javascript,omitempty" json:"javascript,omitempty"`
}
