// Package config loads daemon configuration from TOML files and WMAPI_* env vars.
package config

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/confmap"
	envprov "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	DataDir string        `koanf:"data_dir"`
	Server  ServerConfig  `koanf:"server"`
	Auth    AuthConfig    `koanf:"auth"`
	Storage StorageConfig `koanf:"storage"`
	Log     LogConfig     `koanf:"log"`
	Events  EventsConfig  `koanf:"events"`
	Metrics MetricsConfig `koanf:"metrics"`
}

type ServerConfig struct {
	Bind string `koanf:"bind"`
	Port int    `koanf:"port"`
}

type AuthConfig struct {
	Token string `koanf:"token"`
}

type StorageConfig struct {
	Backend     string `koanf:"backend"`
	SQLitePath  string `koanf:"sqlite_path"`
	PostgresDSN string `koanf:"postgres_dsn"`
}

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

type EventsConfig struct {
	RetentionHours int `koanf:"retention_hours"`
}

type MetricsConfig struct {
	Enabled bool `koanf:"enabled"`
}

func defaults() map[string]any {
	return map[string]any{
		"data_dir":               "./data",
		"server.bind":            "127.0.0.1",
		"server.port":            8080,
		"auth.token":             "",
		"storage.backend":        "sqlite",
		"storage.sqlite_path":    "./data/whatsmeow-api.db",
		"storage.postgres_dsn":   "",
		"log.level":              "info",
		"log.format":             "text",
		"events.retention_hours": 24,
		"metrics.enabled":        false,
	}
}

// Load reads configuration with the precedence: env > file > defaults.
// Pass "" for path to skip the file source.
func Load(path string) (Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(defaults(), "."), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	if path != "" {
		if err := k.Load(file.Provider(path), toml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load file %q: %w", path, err)
		}
	}

	envP := envprov.Provider(".", envprov.Opt{
		Prefix: "WMAPI_",
		TransformFunc: func(key, value string) (string, any) {
			key = strings.ToLower(strings.TrimPrefix(key, "WMAPI_"))
			key = strings.ReplaceAll(key, "__", ".")
			return key, value
		},
	})
	if err := k.Load(envP, nil); err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}

	var c Config
	if err := k.Unmarshal("", &c); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}
	return c, nil
}

func (c Config) Validate() error {
	if !isLoopbackBind(c.Server.Bind) && c.Auth.Token == "" {
		return errors.New("auth.token is required when server.bind is not a loopback address")
	}
	switch c.Storage.Backend {
	case "sqlite":
		// ok
	case "postgres":
		if c.Storage.PostgresDSN == "" {
			return errors.New(`storage.postgres_dsn is required when storage.backend is "postgres"`)
		}
	default:
		return errors.New(`storage.backend must be "sqlite" or "postgres"`)
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return errors.New(`log.level must be one of debug, info, warn, error`)
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		return errors.New(`log.format must be "text" or "json"`)
	}
	return nil
}

func isLoopbackBind(bind string) bool {
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}
