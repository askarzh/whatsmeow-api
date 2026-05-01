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
	"github.com/askarzh/whatsmeow-api/internal/service"
	sqlitestore "github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
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

			appPath := filepath.Join(cfg.DataDir, "whatsmeow-app.db")
			appDB, err := sqlitestore.New(ctx, appPath)
			if err != nil {
				return fmt.Errorf("open app store: %w", err)
			}
			defer func() { _ = appDB.Close() }()
			logger.Info("app store opened", "path", appPath)

			svc := service.New(wa, appDB.Bundle(), logger)

			if err := wa.Resume(ctx); err != nil {
				logger.Warn("session resume failed; awaiting /v1/login/*", "err", err)
			}

			srv := httpapi.NewServer(httpapi.Deps{
				Config:  cfg,
				Logger:  logger,
				Service: svc,
				Store:   appDB.Bundle(),
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
