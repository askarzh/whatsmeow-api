package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/askar/whatsmeow-api/internal/config"
	"github.com/askar/whatsmeow-api/internal/logging"
	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
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

			srv := httpapi.NewServer(httpapi.Deps{Config: cfg, Logger: logger})

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			logger.Info("server starting", "bind", cfg.Server.Bind, "port", cfg.Server.Port)
			if err := srv.Run(ctx); err != nil {
				return fmt.Errorf("server: %w", err)
			}
			logger.Info("server stopped")
			return nil
		},
	}
}
