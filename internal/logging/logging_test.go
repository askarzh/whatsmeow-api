package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/config"
	"github.com/askarzh/whatsmeow-api/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJSON(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(config.LogConfig{Level: "debug", Format: "json"}, &buf)
	require.NoError(t, err)

	logger.Info("hello", slog.String("k", "v"))

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "hello", rec["msg"])
	assert.Equal(t, "v", rec["k"])
}

func TestNewText(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(config.LogConfig{Level: "info", Format: "text"}, &buf)
	require.NoError(t, err)

	logger.Info("hello")
	assert.Contains(t, buf.String(), "hello")
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(config.LogConfig{Level: "warn", Format: "text"}, &buf)
	require.NoError(t, err)

	logger.Info("ignored")
	logger.Warn("kept")

	out := buf.String()
	assert.NotContains(t, out, "ignored")
	assert.True(t, strings.Contains(out, "kept"))
}

func TestRejectsUnknownFormat(t *testing.T) {
	_, err := logging.New(config.LogConfig{Level: "info", Format: "xml"}, &bytes.Buffer{})
	assert.Error(t, err)
}
