package v1

import (
	"fmt"

	"github.com/flanksource/duty"
	"github.com/flanksource/duty/types"
)

type AzureDevops struct {
	BaseScraper         `json:",inline"`
	ConnectionName      string       `yaml:"connection,omitempty" json:"connection,omitempty"`
	Organization        string       `yaml:"organization,omitempty" json:"organization,omitempty"`
	PersonalAccessToken types.EnvVar `yaml:"personalAccessToken,omitempty" json:"personalAccessToken,omitempty"`
	Projects            []string     `yaml:"projects" json:"projects"`
	Pipelines           []string     `yaml:"pipelines" json:"pipelines"`
}

type Azure struct {
	BaseScraper    `json:",inline"`
	ConnectionName string       `yaml:"connection,omitempty" json:"connection,omitempty"`
	SubscriptionID string       `yaml:"subscriptionID" json:"subscriptionID"`
	Organisation   string       `yaml:"organisation" json:"organisation"`
	ClientID       types.EnvVar `yaml:"clientID,omitempty" json:"clientID,omitempty"`
	ClientSecret   types.EnvVar `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	TenantID       string       `yaml:"tenantID" json:"tenantID"`
}

// HydrateConnection populates the credentials in Azure from the connection name (if available)
// else it'll try to fetch the credentials from kubernetes secrets.
func (t *Azure) HydrateConnection(ctx *ScrapeContext) error {
	if t.ConnectionName != "" {
		connection, err := ctx.HydrateConnectionByURL(t.ConnectionName)
		if err != nil {
			return fmt.Errorf("could not hydrate connection: %w", err)
		} else if connection == nil {
			return fmt.Errorf("connection %s not found", t.ConnectionName)
		}

		t.ClientID.ValueStatic = connection.Username
		t.ClientSecret.ValueStatic = connection.Password
		t.TenantID = connection.Properties["tenant"]
		return nil
	}

	var err error
	t.ClientID.ValueStatic, err = duty.GetEnvValueFromCache(ctx.Kubernetes, t.ClientID, ctx.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get client id: %w", err)
	}

	t.ClientSecret.ValueStatic, err = duty.GetEnvValueFromCache(ctx.Kubernetes, t.ClientSecret, ctx.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get client secret: %w", err)
	}

	return nil
}
