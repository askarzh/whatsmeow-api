package main

import (
	"context"
	"fmt"
	"os"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the daemon's WhatsApp connection state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := newDaemonClient(cmd)
			st, err := c.Status(context.Background())
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			if !st.WAConnected {
				fmt.Println("not connected")
				return nil
			}
			fmt.Printf("connected as %s (%s) since %s\n", st.JID, st.PushName, st.Since)
			return nil
		},
	}
	return cmd
}

// newDaemonClient resolves the daemon URL and bearer token from --url/--token
// flags, falling back to WMAPI_URL/WMAPI_TOKEN env vars and finally to
// http://127.0.0.1:8080 with no token.
func newDaemonClient(cmd *cobra.Command) *client.Client {
	url, _ := cmd.Flags().GetString("url")
	if url == "" {
		url = os.Getenv("WMAPI_URL")
	}
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	token, _ := cmd.Flags().GetString("token")
	if token == "" {
		token = os.Getenv("WMAPI_TOKEN")
	}
	return client.New(url, token)
}
