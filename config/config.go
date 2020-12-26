package config

import (
	"fmt"

	"github.com/ieee0824/getenv"
)

type BackendConnecterConfig struct {
	RelayServerConfig
	BackendHostName string
	Scheme          string
	DeveloperName   string
}

func NewBackendConnecterConfig() *BackendConnecterConfig {
	return &BackendConnecterConfig{
		RelayServerConfig: RelayServerConfig{
			Host: getenv.String("RELAY_SERVER_HOST"),
			Port: getenv.String("RELAY_SERVER_PORT"),
		},
		BackendHostName: getenv.String("BACKEND_HOST_NAME"),
		Scheme:          getenv.String("BACKEND_SCHEME", "http"),
		DeveloperName:   getenv.String("DEVELOPER_NAME"),
	}
}

type RelayServerConfig struct {
	Host string
	Port string
}

func NewRelayServerConfig() *RelayServerConfig {
	return &RelayServerConfig{
		Host: getenv.String("RELAY_SERVER_HOST"),
		Port: getenv.String("RELAY_SERVER_PORT"),
	}
}

func (r *RelayServerConfig) Addr() string {
	return fmt.Sprintf("%s:%s", r.Host, r.Port)
}

type ClientConfig struct {
	RelayServerConfig
	ProxyPort          string
	EnableTLS          bool
	SslCertFileName    string
	SslCertKeyFileName string
}

func (c *ClientConfig) Addr() string {
	return fmt.Sprintf(":%s", c.ProxyPort)
}

func NewClientConfig() *ClientConfig {
	return &ClientConfig{
		RelayServerConfig: RelayServerConfig{
			Host: getenv.String("RELAY_SERVER_HOST"),
			Port: getenv.String("RELAY_SERVER_PORT"),
		},
		ProxyPort:          getenv.String("PROXY_PORT"),
		SslCertFileName:    getenv.String("SSL_CERT_FILE_NAME"),
		SslCertKeyFileName: getenv.String("SSL_CERT_KEY_FILE_NAME"),
	}
}
