package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "whatsmeow-api",
		Short:         "HTTP/SSE API daemon wrapping whatsmeow",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to config TOML (optional)")
	root.PersistentFlags().String("url", "", "daemon URL (default $WMAPI_URL or http://127.0.0.1:8080)")
	root.PersistentFlags().String("token", "", "daemon bearer token (default $WMAPI_TOKEN)")
	root.AddCommand(serveCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(logoutCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
