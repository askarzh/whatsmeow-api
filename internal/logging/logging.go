// Package logging builds a *slog.Logger from the daemon's log config.
package logging

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/askarzh/whatsmeow-api/internal/config"
)

func New(cfg config.LogConfig, out io.Writer) (*slog.Logger, error) {
	var lvl slog.Level
	switch cfg.Level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", cfg.Level)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	switch cfg.Format {
	case "json":
		h = slog.NewJSONHandler(out, opts)
	case "text":
		h = slog.NewTextHandler(out, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q", cfg.Format)
	}
	return slog.New(h), nil
}
