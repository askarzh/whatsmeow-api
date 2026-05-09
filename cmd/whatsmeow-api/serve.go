package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/askarzh/whatsmeow-api/internal/config"
	"github.com/askarzh/whatsmeow-api/internal/logging"
	"github.com/askarzh/whatsmeow-api/internal/mediastore"
	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	postgresstore "github.com/askarzh/whatsmeow-api/internal/store/postgres"
	sqlitestore "github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("validate config: %w", err)
			}

			logger, err := logging.New(cfg.Log, os.Stdout)
			if err != nil {
				return fmt.Errorf("init logger: %w", err)
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
				return fmt.Errorf("create data_dir %q: %w", cfg.DataDir, err)
			}

			var (
				container interface{ Close() error }
				wa        *waclient.Adapter
			)
			switch cfg.Storage.Backend {
			case "sqlite":
				path := filepath.Join(cfg.DataDir, "whatsmeow-session.db")
				c, err := waclient.OpenSQLite(ctx, path, logger)
				if err != nil {
					return fmt.Errorf("open sqlite session store: %w", err)
				}
				container = c
				wa = waclient.NewAdapter(c, logger)
			case "postgres":
				c, err := waclient.OpenPostgres(ctx, cfg.Storage.PostgresDSN, logger)
				if err != nil {
					return fmt.Errorf("open postgres session store: %w", err)
				}
				container = c
				wa = waclient.NewAdapter(c, logger)
			default:
				return fmt.Errorf("unsupported storage backend %q", cfg.Storage.Backend)
			}
			defer func() {
				_ = wa.Close()
				_ = container.Close()
			}()

			var (
				appStore interface{ Close() error }
				bundle   store.Bundle
			)
			switch cfg.Storage.Backend {
			case "sqlite":
				appPath := filepath.Join(cfg.DataDir, "whatsmeow-app.db")
				s, err := sqlitestore.New(ctx, appPath)
				if err != nil {
					return fmt.Errorf("open sqlite app store: %w", err)
				}
				appStore = s
				bundle = s.Bundle()
			case "postgres":
				s, err := postgresstore.New(ctx, cfg.Storage.PostgresDSN)
				if err != nil {
					return fmt.Errorf("open postgres app store: %w", err)
				}
				appStore = s
				bundle = s.Bundle()
			default:
				// Validation in config rejects this; defensive only.
				return fmt.Errorf("unsupported storage.backend: %q", cfg.Storage.Backend)
			}
			defer func() { _ = appStore.Close() }()
			logger.Info("app store opened", "backend", cfg.Storage.Backend)

			mediaDir := filepath.Join(cfg.DataDir, "media")
			mediaSt := mediastore.New(mediaDir)
			broadcaster := sse.New(cfg.HTTP.SSESubscriberBuffer)
			svc := service.New(wa, bundle, mediaSt, broadcaster, logger)

			if err := wa.Resume(ctx); err != nil {
				logger.Warn("session resume failed; awaiting /v1/login/*", "err", err)
			}

			srv := httpapi.NewServer(httpapi.Deps{
				Config:      cfg,
				Logger:      logger,
				Service:     svc,
				Store:       bundle,
				Broadcaster: broadcaster,
			})

			logger.Info("server starting", "bind", cfg.Server.Bind, "port", cfg.Server.Port)
			if err := srv.Run(ctx); err != nil {
				return fmt.Errorf("server: %w", err)
			}
			logger.Info("server stopped")
			return nil
		},
	}
}
