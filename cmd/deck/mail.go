package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/deck/internal/client"
	"github.com/syndg/deck/internal/domain"
)

var mailObjective string

var mailCmd = &cobra.Command{
	Use:   "mail [agent-name]",
	Short: "View unread mail for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		msgs, err := c.ListMail(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if len(msgs) == 0 {
			fmt.Printf("No unread mail for agent %q.\n", args[0])
			return nil
		}

		fmt.Printf("Unread mail for agent %q:\n\n", args[0])
		for i, msg := range msgs {
			fmt.Printf("#%-3d FROM: %-20s TYPE: %-12s TIME: %s\n",
				i+1, msg.From, msg.Type, timeAgo(msg.CreatedAt))
			fmt.Printf("    %s\n\n", msg.Payload)
		}
		return nil
	},
}

var sendMailCmd = &cobra.Command{
	Use:   "send [to] [type] [payload]",
	Short: "Send mail to an agent or broadcast group",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		msg := &domain.MailMessage{
			From:      "@human",
			To:        args[0],
			Type:      args[1],
			Payload:   args[2],
			Objective: mailObjective,
		}
		err := c.SendMail(cmd.Context(), msg)
		if err != nil {
			return err
		}
		fmt.Printf("Message sent to %s.\n", args[0])
		return nil
	},
}

func init() {
	sendMailCmd.Flags().StringVar(&mailObjective, "objective", "", "objective ID (required)")
	sendMailCmd.MarkFlagRequired("objective")
	mailCmd.AddCommand(sendMailCmd)
	rootCmd.AddCommand(mailCmd)
}
