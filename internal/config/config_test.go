package config_test

import (
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
