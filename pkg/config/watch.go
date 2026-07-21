package config

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Watch starts watching the config file for changes. Before watching, it
// reads the config file once to determine the resolved file path. On each
// change, it re-reads and validates the config, then calls onReload with
// the new Config. The function blocks until ctx is cancelled.
//
// Uses Viper's WatchConfig which depends on fsnotify.
func Watch(ctx context.Context, onReload func(newCfg *Config)) error {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./config")
	v.AddConfigPath(".")

	v.SetEnvPrefix("YAI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	bindEnv(v)

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("config watch: read: %w", err)
	}

	cfgFile := v.ConfigFileUsed()
	slog.Info("config: watching for changes", "file", cfgFile)

	var reloadMu sync.Mutex // serializes OnConfigChange to prevent viper map race

	v.OnConfigChange(func(e fsnotify.Event) {
		reloadMu.Lock()
		defer reloadMu.Unlock()

		slog.Info("config: file changed, reloading", "event", e.Name)

		// Re-read from disk so viper's internal state reflects the latest file,
		// avoiding a race where successive rapid changes corrupt the cached map.
		if err := v.ReadInConfig(); err != nil {
			slog.Error("config: reload re-read failed", "error", err)
			return
		}

		var newCfg Config
		if err := v.Unmarshal(&newCfg); err != nil {
			slog.Error("config: reload unmarshal failed", "error", err)
			return
		}

		if err := newCfg.Validate(); err != nil {
			slog.Error("config: reload validation failed", "error", err)
			return
		}

		onReload(&newCfg)
	})

	v.WatchConfig()
	<-ctx.Done()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("config watch: %w", err)
	}
	return nil
}
