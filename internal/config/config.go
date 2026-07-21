// Package config provides server-specific configuration derived from the
// shared pkg/config.Config.
package config

import (
	"fmt"
	"time"

	appcfg "github.com/anrror/y-ai-agent-base/pkg/config"
)

// ServerConfig holds the effective runtime configuration for the HTTP server.
type ServerConfig struct {
	Port      int
	Mode      string
	JWTSecret string
	JWTExpiry time.Duration
	LogLevel  string
	LogFormat string
	RateLimit RateLimitConfig
}

// RateLimitConfig controls per-IP rate limiting.
type RateLimitConfig struct {
	Enabled        bool
	RequestsPerMin int
	Burst          int
}

// Defaults returns a ServerConfig with sensible defaults prefilled.
func Defaults() ServerConfig {
	return ServerConfig{
		Port:      8080,
		Mode:      "development",
		LogLevel:  "info",
		LogFormat: "json",
		RateLimit: RateLimitConfig{
			Enabled:        true,
			RequestsPerMin: 100,
			Burst:          20,
		},
	}
}

// FromAppConfig derives a ServerConfig from the application-level Config.
// Missing or zero values fall back to Defaults().
func FromAppConfig(cfg *appcfg.Config) ServerConfig {
	sc := Defaults()
	if cfg == nil {
		return sc
	}

	if cfg.Server.Port > 0 {
		sc.Port = cfg.Server.Port
	}
	if cfg.Server.Mode != "" {
		sc.Mode = cfg.Server.Mode
	}
	if cfg.Auth.JWTSecret != "" {
		sc.JWTSecret = cfg.Auth.JWTSecret
	}
	if cfg.Auth.JWTExpiry > 0 {
		sc.JWTExpiry = cfg.Auth.JWTExpiry
	}
	if cfg.Logging.Level != "" {
		sc.LogLevel = cfg.Logging.Level
	}
	if cfg.Logging.Format != "" {
		sc.LogFormat = cfg.Logging.Format
	}

	return sc
}

// Addr returns the listen address string (e.g. ":8080").
func (sc ServerConfig) Addr() string {
	return fmt.Sprintf(":%d", sc.Port)
}
