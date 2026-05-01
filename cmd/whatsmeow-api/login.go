package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/spf13/cobra"
)

func loginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Pair the daemon with a WhatsApp account",
	}
	cmd.AddCommand(loginPhoneCmd())
	return cmd
}

func loginPhoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "phone <number>",
		Short: "Pair via phone number (8-character link code)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			number := args[0]
			if !waclient.IsValidPhoneNumber(number) {
				return fmt.Errorf("invalid phone number %q (must be E.164, e.g. +27821234567)", number)
			}

			c := newDaemonClient(cmd)
			ch, err := c.LoginPhone(context.Background(), number)
			if err != nil {
				switch {
				case errors.Is(err, client.ErrAlreadyLoggedIn):
					fmt.Fprintln(os.Stderr, "already logged in")
					os.Exit(1)
				case errors.Is(err, client.ErrLoginInProgress):
					fmt.Fprintln(os.Stderr, "another login is already in progress")
					os.Exit(1)
				}
				return fmt.Errorf("login phone: %w", err)
			}

			for evt := range ch {
				if evt.Terminal {
					if evt.Outcome == "success" {
						fmt.Println("logged in")
						return nil
					}
					return fmt.Errorf("pairing failed: %s", evt.Outcome)
				}
				fmt.Printf("Pair code: %s\n  Open WhatsApp → Settings → Linked Devices → Link with phone number → enter the code (expires in ~2 min)\n", evt.Code)
			}
			return fmt.Errorf("pairing stream closed without terminal event")
		},
	}
}
