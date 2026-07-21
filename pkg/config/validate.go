package config

import (
	"errors"
	"fmt"
)

// Validate checks that all required configuration fields are present
// and within valid ranges. Returns nil on success, or an error describing
// the first validation failure found.
func (c *Config) Validate() error {
	var errs []error

	if c.Providers.Chat == nil || c.Providers.Chat.APIKey == "" {
		errs = append(errs, errors.New("providers.chat.api_key is required"))
	}
	if c.Auth.JWTSecret == "" {
		errs = append(errs, errors.New("auth.jwt_secret is required"))
	}
	if c.Server.Port < 1 {
		errs = append(errs, fmt.Errorf("server.port must be >= 1, got %d", c.Server.Port))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config validation failed: %w", errors.Join(errs...))
	}
	return nil
}
