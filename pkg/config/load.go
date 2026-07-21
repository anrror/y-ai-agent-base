package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Load reads configuration from sources in priority order:
//  1. Command-line flags (via pflag)
//  2. Environment variables (YAI_ prefix, dots become underscores)
//  3. config/config.yaml (or config.yaml in working directory)
//  4. .env file (loaded silently, ignored if missing)
//
// After loading, Resolve() merges global provider defaults into per-role
// configs, and Validate() is called automatically. Returns the parsed
// Config or an error if loading or validation fails.
func Load() (*Config, error) {
	v := viper.New()

	// --- Config file ---
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./config")
	v.AddConfigPath(".")

	// --- Environment variables ---
	v.SetEnvPrefix("YAI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Explicit env var bindings for clarity and discoverability.
	// Viper will also catch these via AutomaticEnv + replacer,
	// but explicit binds make the mapping obvious in godoc.
	bindEnv(v)

	// --- Command-line flags (local FlagSet to avoid global state conflicts) ---
	flags := pflag.NewFlagSet("config", pflag.ContinueOnError)
	flags.Int("server.port", 0, "Server port (env: YAI_SERVER_PORT)")
	flags.String("server.mode", "", "Server mode: development | production (env: YAI_SERVER_MODE)")
	flags.String("providers.api-key", "", "Default API key for all providers (env: YAI_PROVIDERS_API_KEY)")
	flags.String("providers.base-url", "", "Default base URL for all providers (env: YAI_PROVIDERS_BASE_URL)")
	flags.String("providers.chat.api-key", "", "Chat provider API key (env: YAI_PROVIDERS_CHAT_API_KEY)")
	flags.String("providers.chat.base-url", "", "Chat provider base URL (env: YAI_PROVIDERS_CHAT_BASE_URL)")
	flags.String("providers.chat.model", "", "Chat provider model (env: YAI_PROVIDERS_CHAT_MODEL)")
	flags.String("providers.embedding.api-key", "", "Embedding provider API key (env: YAI_PROVIDERS_EMBEDDING_API_KEY)")
	flags.String("providers.embedding.base-url", "", "Embedding provider base URL (env: YAI_PROVIDERS_EMBEDDING_BASE_URL)")
	flags.String("providers.embedding.model", "", "Embedding provider model (env: YAI_PROVIDERS_EMBEDDING_MODEL)")
	flags.String("providers.guard.api-key", "", "Guard provider API key (env: YAI_PROVIDERS_GUARD_API_KEY)")
	flags.String("providers.guard.base-url", "", "Guard provider base URL (env: YAI_PROVIDERS_GUARD_BASE_URL)")
	flags.String("providers.guard.model", "", "Guard provider model (env: YAI_PROVIDERS_GUARD_MODEL)")
	flags.String("database.mysql", "", "MySQL DSN (env: YAI_DATABASE_MYSQL)")
	flags.String("database.postgresql", "", "PostgreSQL DSN (env: YAI_DATABASE_POSTGRESQL)")
	flags.String("database.redis", "", "Redis DSN (env: YAI_DATABASE_REDIS)")
	flags.String("auth.jwt-secret", "", "JWT signing secret (env: YAI_AUTH_JWT_SECRET)")
	flags.String("auth.jwt-expiry", "", "JWT expiry duration (env: YAI_AUTH_JWT_EXPIRY)")
	flags.String("logging.level", "", "Log level: debug | info | warn | error (env: YAI_LOGGING_LEVEL)")
	flags.String("logging.format", "", "Log format: text | json (env: YAI_LOGGING_FORMAT)")

	// ParseErrorsAllowlist allows unknown flags (e.g. go test -test.v).
	flags.ParseErrorsAllowlist = pflag.ParseErrorsAllowlist{UnknownFlags: true}
	if err := flags.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("config: parse flags: %w", err)
	}

	if err := v.BindPFlags(flags); err != nil {
		return nil, fmt.Errorf("config: bind pflags: %w", err)
	}

	// --- .env file (best-effort) ---
	_ = godotenv.Load()

	// --- Read config file ---
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("config: read file: %w", err)
		}
		// Config file not found is OK — env vars / flags may supply everything.
	}

	// --- Unmarshal ---
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	// Read modules section into raw map for ModuleConfig() lookups.
	var modules map[string]any
	if err := v.UnmarshalKey("modules", &modules); err == nil && modules != nil {
		cfg.Modules = modules
	}

	// --- Merge provider defaults ---
	cfg.Providers.Resolve()

	// --- Validate ---
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// bindEnv binds all config keys to their environment variable equivalents.
// Shared by Load() and Watch() to avoid duplicating the binding list.
func bindEnv(v *viper.Viper) {
	_ = v.BindEnv("server.port")
	_ = v.BindEnv("server.mode")
	_ = v.BindEnv("providers.api_key")
	_ = v.BindEnv("providers.base_url")
	_ = v.BindEnv("providers.chat.api_key")
	_ = v.BindEnv("providers.chat.base_url")
	_ = v.BindEnv("providers.chat.model")
	_ = v.BindEnv("providers.embedding.api_key")
	_ = v.BindEnv("providers.embedding.base_url")
	_ = v.BindEnv("providers.embedding.model")
	_ = v.BindEnv("providers.guard.api_key")
	_ = v.BindEnv("providers.guard.base_url")
	_ = v.BindEnv("providers.guard.model")
	_ = v.BindEnv("database.mysql")
	_ = v.BindEnv("database.postgresql")
	_ = v.BindEnv("database.redis")
	_ = v.BindEnv("auth.jwt_secret")
	_ = v.BindEnv("auth.jwt_expiry")
	_ = v.BindEnv("logging.level")
	_ = v.BindEnv("logging.format")
}
