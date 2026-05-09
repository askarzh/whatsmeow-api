package config_test

import (
	"os"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	c, err := config.Load("")
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", c.Server.Bind)
	assert.Equal(t, 8080, c.Server.Port)
	assert.Equal(t, "", c.Auth.Token)
	assert.Equal(t, "sqlite", c.Storage.Backend)
	assert.Equal(t, "./data/whatsmeow-api.db", c.Storage.SQLitePath)
	assert.Equal(t, "", c.Storage.PostgresDSN)
	assert.Equal(t, "./data", c.DataDir)
	assert.Equal(t, "info", c.Log.Level)
	assert.Equal(t, "text", c.Log.Format)
	assert.Equal(t, 24, c.Events.RetentionHours)
	assert.False(t, c.Metrics.Enabled)
	assert.Equal(t, int64(100*1024*1024), c.HTTP.MaxBodyBytes)
}

func TestLoadFromTOML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c.toml"
	require.NoError(t, os.WriteFile(path, []byte(`
data_dir = "/tmp/wm"
[server]
bind = "0.0.0.0"
port = 9000
[auth]
token = "secret"
[storage]
backend = "postgres"
postgres_dsn = "postgres://x"
[log]
level = "debug"
format = "json"
`), 0o600))

	c, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "/tmp/wm", c.DataDir)
	assert.Equal(t, "0.0.0.0", c.Server.Bind)
	assert.Equal(t, 9000, c.Server.Port)
	assert.Equal(t, "secret", c.Auth.Token)
	assert.Equal(t, "postgres", c.Storage.Backend)
	assert.Equal(t, "postgres://x", c.Storage.PostgresDSN)
	assert.Equal(t, "debug", c.Log.Level)
	assert.Equal(t, "json", c.Log.Format)
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("WMAPI_SERVER__PORT", "12345")
	t.Setenv("WMAPI_AUTH__TOKEN", "from-env")
	t.Setenv("WMAPI_LOG__FORMAT", "json")

	c, err := config.Load("")
	require.NoError(t, err)

	assert.Equal(t, 12345, c.Server.Port)
	assert.Equal(t, "from-env", c.Auth.Token)
	assert.Equal(t, "json", c.Log.Format)
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{
			name:    "ok defaults",
			mutate:  func(c *config.Config) {},
			wantErr: "",
		},
		{
			name:    "non-localhost bind without token rejected",
			mutate:  func(c *config.Config) { c.Server.Bind = "0.0.0.0" },
			wantErr: "auth.token is required when server.bind is not a loopback address",
		},
		{
			name:    "ipv6 loopback ::1 without token allowed",
			mutate:  func(c *config.Config) { c.Server.Bind = "::1" },
			wantErr: "",
		},
		{
			name:    "127.0.0.2 in loopback range without token allowed",
			mutate:  func(c *config.Config) { c.Server.Bind = "127.0.0.2" },
			wantErr: "",
		},
		{
			name:    "hostname 'localhost' without token rejected",
			mutate:  func(c *config.Config) { c.Server.Bind = "localhost" },
			wantErr: "auth.token is required when server.bind is not a loopback address",
		},
		{
			name: "non-localhost bind with token allowed",
			mutate: func(c *config.Config) {
				c.Server.Bind = "0.0.0.0"
				c.Auth.Token = "x"
			},
			wantErr: "",
		},
		{
			name:    "unknown storage backend rejected",
			mutate:  func(c *config.Config) { c.Storage.Backend = "redis" },
			wantErr: `storage.backend must be "sqlite" or "postgres"`,
		},
		{
			name:    "postgres backend requires DSN",
			mutate:  func(c *config.Config) { c.Storage.Backend = "postgres" },
			wantErr: `storage.postgres_dsn is required when storage.backend is "postgres"`,
		},
		{
			name:    "invalid log level rejected",
			mutate:  func(c *config.Config) { c.Log.Level = "trace" },
			wantErr: `log.level must be one of debug, info, warn, error`,
		},
		{
			name:    "invalid log format rejected",
			mutate:  func(c *config.Config) { c.Log.Format = "xml" },
			wantErr: `log.format must be "text" or "json"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := config.Load("")
			require.NoError(t, err)
			tc.mutate(&c)
			err = c.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}
