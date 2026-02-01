package plugin

import (
	"context"

	pb "github.com/flanksource/config-db/api/plugin/proto"
)

type GRPCHostClient struct {
	client pb.HostServicesClient
}

func NewGRPCHostClient(client pb.HostServicesClient) *GRPCHostClient {
	return &GRPCHostClient{client: client}
}

func (c *GRPCHostClient) HydrateConnection(ctx context.Context, name, namespace string) (*Connection, error) {
	resp, err := c.client.HydrateConnection(ctx, &pb.ConnectionRequest{
		Name:      name,
		Namespace: namespace,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, &PluginError{Message: resp.Error}
	}
	return &Connection{
		Username:   resp.Username,
		Password:   resp.Password,
		URL:        resp.Url,
		Properties: resp.Properties,
	}, nil
}

func (c *GRPCHostClient) GetEnvValue(ctx context.Context, envVar EnvVar, namespace string) (string, error) {
	resp, err := c.client.GetEnvValue(ctx, &pb.EnvVarRequest{
		Name:                         envVar.Name,
		ValueFromConfigmapKeyRefName: envVar.ValueFromConfigMapKeyRefName,
		ValueFromConfigmapKeyRefKey:  envVar.ValueFromConfigMapKeyRefKey,
		ValueFromSecretKeyRefName:    envVar.ValueFromSecretKeyRefName,
		ValueFromSecretKeyRefKey:     envVar.ValueFromSecretKeyRefKey,
		ValueStatic:                  envVar.ValueStatic,
		Namespace:                    namespace,
	})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", &PluginError{Message: resp.Error}
	}
	return resp.Value, nil
}

func (c *GRPCHostClient) FindConfig(ctx context.Context, configType, externalID, scraperID string) (string, bool, error) {
	resp, err := c.client.FindConfig(ctx, &pb.FindConfigRequest{
		ConfigType: configType,
		ExternalId: externalID,
		ScraperId:  scraperID,
	})
	if err != nil {
		return "", false, err
	}
	if resp.Error != "" {
		return "", false, &PluginError{Message: resp.Error}
	}
	return resp.ConfigId, resp.Found, nil
}

type PluginError struct {
	Message string
}

func (e *PluginError) Error() string {
	return e.Message
}
