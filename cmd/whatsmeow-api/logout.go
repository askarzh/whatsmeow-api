package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/spf13/cobra"
)

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log the daemon's WhatsApp session out",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := newDaemonClient(cmd)
			err := c.Logout(context.Background())
			switch {
			case err == nil:
				fmt.Println("logged out")
				return nil
			case errors.Is(err, client.ErrNotLoggedIn):
				fmt.Println("not logged in")
				return nil
			default:
				return fmt.Errorf("logout: %w", err)
			}
		},
	}
}
