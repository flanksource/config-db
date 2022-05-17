package v1

import (
	"strings"

	"github.com/flanksource/kommons"
)

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
