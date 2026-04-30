package config_test

import (
	"os"
	"testing"

	"github.com/askar/whatsmeow-api/internal/config"
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
