// Package config loads and validates application configuration from YAML, env, and flags.
package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// Config holds all configuration for the AI agent application.
// Secrets (APIKey, JWTSecret) use json:"-" to prevent leaking into JSON
// but remain readable from YAML via Viper (no mapstructure:"-").
type Config struct {
	Server    ServerConfig    `json:"server"    mapstructure:"server"`
	Providers ProvidersConfig `json:"providers" mapstructure:"providers"`
	Database  DatabaseConfig  `json:"database" mapstructure:"database"`
	Auth      AuthConfig      `json:"auth"     mapstructure:"auth"`
	Logging   LoggingConfig   `json:"logging"  mapstructure:"logging"`
	Modules   map[string]any  `json:"-"        mapstructure:"modules"`
}

// ModuleConfig unmarshals the modules.<id> section into out (must be a
// pointer to a struct). Returns nil when the module section does not exist
// (out is left as the zero value).
func (c *Config) ModuleConfig(id string, out any) error {
	raw, ok := c.Modules[id]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("module %q marshal: %w", id, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("module %q unmarshal: %w", id, err)
	}
	return nil
}

// ProviderConfig holds configuration for a single provider role.
type ProviderConfig struct {
	Type    string `json:"type"     mapstructure:"type"`
	APIKey  string `json:"-"        mapstructure:"api_key"`
	BaseURL string `json:"base_url" mapstructure:"base_url"`
	Model   string `json:"model"    mapstructure:"model"`
}

// ProvidersConfig holds per-role provider configurations with a global default.
type ProvidersConfig struct {
	// Global defaults — inherited by all roles when per-role field is empty.
	APIKey    string          `json:"-"        mapstructure:"api_key"`
	BaseURL   string          `json:"base_url" mapstructure:"base_url"`
	Chat      *ProviderConfig `json:"chat"      mapstructure:"chat"`
	Embedding *ProviderConfig `json:"embedding" mapstructure:"embedding"`
	Guard     *ProviderConfig `json:"guard"     mapstructure:"guard"`
}

// Resolve merges global defaults into each configured role.
// Called after Load() completes. If a role's APIKey/BaseURL is empty,
// it inherits from the global level. Does NOT modify fields that are set.
func (p *ProvidersConfig) Resolve() {
	if p.Chat != nil {
		if p.Chat.APIKey == "" {
			p.Chat.APIKey = p.APIKey
		}
		if p.Chat.BaseURL == "" {
			p.Chat.BaseURL = p.BaseURL
		}
	}
	if p.Embedding != nil {
		if p.Embedding.APIKey == "" {
			p.Embedding.APIKey = p.APIKey
		}
		if p.Embedding.BaseURL == "" {
			p.Embedding.BaseURL = p.BaseURL
		}
	}
	if p.Guard != nil {
		if p.Guard.APIKey == "" {
			p.Guard.APIKey = p.APIKey
		}
		if p.Guard.BaseURL == "" {
			p.Guard.BaseURL = p.BaseURL
		}
	}
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Port int    `json:"port" mapstructure:"port"`
	Mode string `json:"mode" mapstructure:"mode"`
}

// DatabaseConfig holds database connection DSNs.
type DatabaseConfig struct {
	MySQL      string `json:"mysql"      mapstructure:"mysql"`
	PostgreSQL string `json:"postgresql" mapstructure:"postgresql"`
	Redis      string `json:"redis"      mapstructure:"redis"`
}

// AuthConfig holds authentication configuration.
type AuthConfig struct {
	JWTSecret string        `json:"-"          mapstructure:"jwt_secret"`
	JWTExpiry time.Duration `json:"jwt_expiry"  mapstructure:"jwt_expiry"`
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level  string `json:"level"  mapstructure:"level"`
	Format string `json:"format" mapstructure:"format"`
}

// --- safe serialization shapes -------------------------------------------------

// safeConfig is the JSON-serializable shape that omits secret fields.
type safeConfig struct {
	Server    safeServerConfig    `json:"server"`
	Providers safeProvidersConfig `json:"providers"`
	Database  DatabaseConfig      `json:"database"`
	Auth      safeAuthConfig      `json:"auth"`
	Logging   LoggingConfig       `json:"logging"`
}

type safeServerConfig struct {
	Port int    `json:"port"`
	Mode string `json:"mode"`
}

type safeProvidersConfig struct {
	BaseURL   string              `json:"base_url"`
	Chat      *safeProviderConfig `json:"chat,omitempty"`
	Embedding *safeProviderConfig `json:"embedding,omitempty"`
	Guard     *safeProviderConfig `json:"guard,omitempty"`
}

type safeProviderConfig struct {
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

type safeAuthConfig struct {
	JWTExpiry time.Duration `json:"jwt_expiry"`
}

// MarshalJSON serializes Config without leaking secret fields.
func (c Config) MarshalJSON() ([]byte, error) {
	sp := safeProvidersConfig{
		BaseURL: c.Providers.BaseURL,
	}
	if c.Providers.Chat != nil {
		sp.Chat = &safeProviderConfig{
			Type:    c.Providers.Chat.Type,
			BaseURL: c.Providers.Chat.BaseURL,
			Model:   c.Providers.Chat.Model,
		}
	}
	if c.Providers.Embedding != nil {
		sp.Embedding = &safeProviderConfig{
			Type:    c.Providers.Embedding.Type,
			BaseURL: c.Providers.Embedding.BaseURL,
			Model:   c.Providers.Embedding.Model,
		}
	}
	if c.Providers.Guard != nil {
		sp.Guard = &safeProviderConfig{
			Type:    c.Providers.Guard.Type,
			BaseURL: c.Providers.Guard.BaseURL,
			Model:   c.Providers.Guard.Model,
		}
	}

	sc := safeConfig{
		Server: safeServerConfig{
			Port: c.Server.Port,
			Mode: c.Server.Mode,
		},
		Providers: sp,
		Database:  c.Database,
		Auth: safeAuthConfig{
			JWTExpiry: c.Auth.JWTExpiry,
		},
		Logging: c.Logging,
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return nil, fmt.Errorf("config marshal: %w", err)
	}
	return data, nil
}
