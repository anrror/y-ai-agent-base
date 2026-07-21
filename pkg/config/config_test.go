package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Validate ---

func TestValidate_Success_when_allRequiredFieldsSet(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			Chat: &ProviderConfig{APIKey: "sk-test"},
		},
		Auth:   AuthConfig{JWTSecret: "super-secret"},
		Server: ServerConfig{Port: 8080},
	}
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidate_Fails_when_APIKeyMissing(t *testing.T) {
	cfg := &Config{
		Auth:   AuthConfig{JWTSecret: "super-secret"},
		Server: ServerConfig{Port: 8080},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "providers.chat.api_key is required")
}

func TestValidate_Fails_when_JWTSecretMissing(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			Chat: &ProviderConfig{APIKey: "sk-test"},
		},
		Server: ServerConfig{Port: 8080},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth.jwt_secret is required")
}

func TestValidate_Fails_when_PortInvalid(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			Chat: &ProviderConfig{APIKey: "sk-test"},
		},
		Auth:   AuthConfig{JWTSecret: "super-secret"},
		Server: ServerConfig{Port: 0},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.port")
}

func TestValidate_ReportsMultipleErrors(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Port: -1},
	}
	err := cfg.Validate()
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "providers.chat.api_key")
	assert.Contains(t, msg, "auth.jwt_secret")
	assert.Contains(t, msg, "server.port")
}

// --- YAML Loading ---

func TestLoad_ValidYAML(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "config.yaml"), `
server:
  port: 9090
  mode: production
providers:
  api_key: sk-from-yaml
  base_url: https://custom.api/v1
  chat:
    model: gpt-4o-mini
database:
  mysql: "user:pass@tcp(localhost:3306)/db"
  postgresql: "postgres://user:pass@localhost:5432/db"
  redis: "redis://localhost:6379/0"
auth:
  jwt_secret: yaml-jwt-secret
  jwt_expiry: 12h
logging:
  level: debug
  format: json
`)

	t.Setenv("YAI_PROVIDERS_CHAT_API_KEY", "") // clear any ambient env

	cfg, err := testLoad(tmp)
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, "production", cfg.Server.Mode)
	require.NotNil(t, cfg.Providers.Chat)
	assert.Equal(t, "sk-from-yaml", cfg.Providers.Chat.APIKey)
	assert.Equal(t, "https://custom.api/v1", cfg.Providers.Chat.BaseURL)
	assert.Equal(t, "gpt-4o-mini", cfg.Providers.Chat.Model)
	assert.Equal(t, "user:pass@tcp(localhost:3306)/db", cfg.Database.MySQL)
	assert.Equal(t, "yaml-jwt-secret", cfg.Auth.JWTSecret)
	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
}

// --- Env Var Override ---

func TestLoad_EnvOverridesYAML(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "config.yaml"), `
server:
  port: 3000
providers:
  api_key: from-yaml
  chat:
    model: gpt-4o
auth:
  jwt_secret: from-yaml
`)

	t.Setenv("YAI_SERVER_PORT", "5001")
	t.Setenv("YAI_PROVIDERS_CHAT_API_KEY", "sk-from-env")
	t.Setenv("YAI_AUTH_JWT_SECRET", "env-jwt-secret")
	t.Setenv("YAI_SERVER_MODE", "production")
	t.Setenv("YAI_PROVIDERS_CHAT_MODEL", "claude-3-opus")
	t.Setenv("YAI_LOGGING_LEVEL", "warn")

	cfg, err := testLoad(tmp)
	require.NoError(t, err)

	assert.Equal(t, 5001, cfg.Server.Port)
	assert.Equal(t, "production", cfg.Server.Mode)
	require.NotNil(t, cfg.Providers.Chat)
	assert.Equal(t, "sk-from-env", cfg.Providers.Chat.APIKey)
	assert.Equal(t, "claude-3-opus", cfg.Providers.Chat.Model)
	assert.Equal(t, "env-jwt-secret", cfg.Auth.JWTSecret)
	assert.Equal(t, "warn", cfg.Logging.Level)
}

func TestLoad_EnvOnly_NoConfigFile(t *testing.T) {
	tmp := t.TempDir() // empty dir — no config.yaml

	t.Setenv("YAI_PROVIDERS_CHAT_API_KEY", "sk-env-only")
	t.Setenv("YAI_AUTH_JWT_SECRET", "env-only-jwt")
	t.Setenv("YAI_SERVER_PORT", "7070")

	cfg, err := testLoad(tmp)
	require.NoError(t, err)

	assert.Equal(t, 7070, cfg.Server.Port)
	require.NotNil(t, cfg.Providers.Chat)
	assert.Equal(t, "sk-env-only", cfg.Providers.Chat.APIKey)
	assert.Equal(t, "env-only-jwt", cfg.Auth.JWTSecret)
}

func TestLoad_EnvOnly_MissingRequired_Fails(t *testing.T) {
	tmp := t.TempDir()

	// Only set port — missing APIKey and JWTSecret
	t.Setenv("YAI_SERVER_PORT", "8080")

	_, err := testLoad(tmp)
	require.Error(t, err)
}

// --- Provider Default Merging (Resolve) ---

func TestResolve_GlobalDefaultsInherited(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			APIKey:  "sk-global",
			BaseURL: "https://global.api/v1",
			Chat:    &ProviderConfig{Model: "gpt-4o"},
		},
	}
	cfg.Providers.Resolve()

	assert.Equal(t, "sk-global", cfg.Providers.Chat.APIKey,
		"chat.api_key should inherit from global")
	assert.Equal(t, "https://global.api/v1", cfg.Providers.Chat.BaseURL,
		"chat.base_url should inherit from global")
	assert.Equal(t, "gpt-4o", cfg.Providers.Chat.Model,
		"chat.model should be preserved")
}

func TestResolve_PerRoleOverridesGlobal(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			APIKey:  "sk-global",
			BaseURL: "https://global.api/v1",
			Chat: &ProviderConfig{
				APIKey:  "sk-chat-override",
				BaseURL: "https://chat.api/v1",
				Model:   "gpt-4o",
			},
		},
	}
	cfg.Providers.Resolve()

	assert.Equal(t, "sk-chat-override", cfg.Providers.Chat.APIKey,
		"chat.api_key should keep its explicit value, not global")
	assert.Equal(t, "https://chat.api/v1", cfg.Providers.Chat.BaseURL,
		"chat.base_url should keep its explicit value, not global")
}

func TestResolve_PartialOverride(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			APIKey:  "sk-global",
			BaseURL: "https://global.api/v1",
			Chat: &ProviderConfig{
				Model: "gpt-4o",
				// APIKey empty → inherit
				// BaseURL empty → inherit
			},
		},
	}
	cfg.Providers.Resolve()

	assert.Equal(t, "sk-global", cfg.Providers.Chat.APIKey,
		"empty chat.api_key should inherit from global")
	assert.Equal(t, "https://global.api/v1", cfg.Providers.Chat.BaseURL,
		"empty chat.base_url should inherit from global")
	assert.Equal(t, "gpt-4o", cfg.Providers.Chat.Model)
}

func TestResolve_NilGuardRole(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			APIKey: "sk-global",
			Chat:   &ProviderConfig{Model: "gpt-4o"},
			// Guard is nil — not configured
		},
	}
	cfg.Providers.Resolve()

	assert.Nil(t, cfg.Providers.Guard,
		"guard should remain nil when not configured")
	require.NotNil(t, cfg.Providers.Chat)
	assert.Equal(t, "sk-global", cfg.Providers.Chat.APIKey)
}

func TestResolve_GlobalURLOnly(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			BaseURL: "https://proxy.company.com/v1",
			Chat:    &ProviderConfig{APIKey: "sk-chat", Model: "gpt-4o"},
			Embedding: &ProviderConfig{
				APIKey: "sk-emb",
				// BaseURL empty → inherit global
			},
		},
	}
	cfg.Providers.Resolve()

	assert.Equal(t, "https://proxy.company.com/v1", cfg.Providers.Chat.BaseURL,
		"chat.base_url should inherit from global when empty")
	assert.Equal(t, "https://proxy.company.com/v1", cfg.Providers.Embedding.BaseURL,
		"embedding.base_url should inherit from global when empty")
}

// --- MarshalJSON ---

func TestMarshalJSON_OmitsSecrets(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Port: 8080, Mode: "development"},
		Providers: ProvidersConfig{
			APIKey:  "sk-global-secret",
			BaseURL: "https://api.example.com",
			Chat: &ProviderConfig{
				Type:    "openai",
				APIKey:  "sk-secret",
				BaseURL: "https://api.example.com",
				Model:   "gpt-4o",
			},
		},
		Database: DatabaseConfig{MySQL: "mysql://", PostgreSQL: "pg://", Redis: "redis://"},
		Auth:     AuthConfig{JWTSecret: "jwt-secret-value", JWTExpiry: 3600000000000}, // 1h
		Logging:  LoggingConfig{Level: "info", Format: "text"},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	raw := string(data)

	// Secret fields MUST NOT appear
	assert.NotContains(t, raw, "sk-global-secret")
	assert.NotContains(t, raw, "sk-secret")
	assert.NotContains(t, raw, "jwt-secret-value")

	// Non-secret fields SHOULD appear
	assert.Contains(t, raw, `"port":8080`)
	assert.Contains(t, raw, `"mode":"development"`)
	assert.Contains(t, raw, `"base_url":"https://api.example.com"`)
	assert.Contains(t, raw, `"model":"gpt-4o"`)
	assert.Contains(t, raw, `"type":"openai"`)
	assert.Contains(t, raw, `"mysql":"mysql://"`)
	assert.Contains(t, raw, `"level":"info"`)
	assert.Contains(t, raw, `"format":"text"`)
}

func TestMarshalJSON_ValidJSON(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Port: 8080},
		Providers: ProvidersConfig{
			APIKey:  "sk-global",
			BaseURL: "https://api.example.com",
			Chat: &ProviderConfig{
				Type:    "openai",
				APIKey:  "sk-abc",
				BaseURL: "https://api.example.com",
				Model:   "gpt-4o",
			},
		},
		Auth:    AuthConfig{JWTSecret: "secret123", JWTExpiry: 7200000000000},
		Logging: LoggingConfig{Level: "info"},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	// Roundtrip — valid JSON can be unmarshaled into a map
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	// Verify top-level keys exist
	for _, key := range []string{"server", "providers", "database", "auth", "logging"} {
		_, ok := m[key]
		assert.True(t, ok, "expected key %q in JSON output", key)
	}
}

// --- Helpers ---

// testLoad calls Load() with the working directory set to configDir
// so Viper can find config.yaml in the temp directory.
func testLoad(configDir string) (*Config, error) {
	oldWd, _ := os.Getwd()
	_ = os.Chdir(configDir)
	defer func() { _ = os.Chdir(oldWd) }()

	return Load()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	// Strip common leading whitespace (preserving relative indent).
	content = strings.TrimPrefix(content, "\n")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
