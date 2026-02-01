package plugin

import (
	"github.com/flanksource/duty/types"
	"github.com/hashicorp/go-plugin"
)

const (
	PluginName      = "scraper"
	ProtocolVersion = 1
)

var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "CONFIG_DB_PLUGIN",
	MagicCookieValue: "scraper",
}

type Connection struct {
	Username   string
	Password   string
	URL        string
	Properties map[string]string
}

type EnvVar struct {
	Name                         string
	ValueFromConfigMapKeyRefName string
	ValueFromConfigMapKeyRefKey  string
	ValueFromSecretKeyRefName    string
	ValueFromSecretKeyRefKey     string
	ValueStatic                  string
}

func EnvVarFromDuty(e types.EnvVar) EnvVar {
	ev := EnvVar{
		ValueStatic: e.ValueStatic,
	}
	if e.ValueFrom != nil && e.ValueFrom.ConfigMapKeyRef != nil {
		ev.ValueFromConfigMapKeyRefName = e.ValueFrom.ConfigMapKeyRef.Name
		ev.ValueFromConfigMapKeyRefKey = e.ValueFrom.ConfigMapKeyRef.Key
	}
	if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
		ev.ValueFromSecretKeyRefName = e.ValueFrom.SecretKeyRef.Name
		ev.ValueFromSecretKeyRefKey = e.ValueFrom.SecretKeyRef.Key
	}
	return ev
}

func (e EnvVar) ToDuty() types.EnvVar {
	ev := types.EnvVar{
		ValueStatic: e.ValueStatic,
	}
	if e.ValueFromConfigMapKeyRefName != "" || e.ValueFromSecretKeyRefName != "" {
		ev.ValueFrom = &types.EnvVarSource{}
	}
	if e.ValueFromConfigMapKeyRefName != "" {
		ev.ValueFrom.ConfigMapKeyRef = &types.ConfigMapKeySelector{
			LocalObjectReference: types.LocalObjectReference{Name: e.ValueFromConfigMapKeyRefName},
			Key:                  e.ValueFromConfigMapKeyRefKey,
		}
	}
	if e.ValueFromSecretKeyRefName != "" {
		ev.ValueFrom.SecretKeyRef = &types.SecretKeySelector{
			LocalObjectReference: types.LocalObjectReference{Name: e.ValueFromSecretKeyRefName},
			Key:                  e.ValueFromSecretKeyRefKey,
		}
	}
	return ev
}

type PluginInfo struct {
	Name           string
	Version        string
	SupportedTypes []string
}

type HostServices interface {
	HydrateConnection(name, namespace string) (*Connection, error)
	GetEnvValue(envVar EnvVar, namespace string) (string, error)
	FindConfig(configType, externalID, scraperID string) (configID string, found bool, err error)
}
