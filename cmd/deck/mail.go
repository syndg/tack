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
			priorityTag := ""
			if msg.Priority != "" && msg.Priority != "normal" {
				priorityTag = fmt.Sprintf(" [%s]", msg.Priority)
			}
			fmt.Printf("#%-3d FROM: %-20s TYPE: %-12s%s  %s\n",
				i+1, msg.From, msg.Type, priorityTag, timeAgo(msg.CreatedAt))
			if msg.Subject != "" {
				fmt.Printf("     Subject: %s\n", msg.Subject)
			}
			if msg.Body != "" {
				fmt.Printf("     %s\n", msg.Body)
			}
			if msg.Payload != "" {
				fmt.Printf("     Payload: %s\n", msg.Payload)
			}
			fmt.Println()
		}
		return nil
	},
}

var sendMailCmd = &cobra.Command{
	Use:   "send [to] [subject] [body]",
	Short: "Send mail to an agent or broadcast group",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		msg := &domain.MailMessage{
			From:      "@human",
			To:        args[0],
			Subject:   args[1],
			Body:      args[2],
			Type:      "message",
			Priority:  "normal",
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
	if err := sendMailCmd.MarkFlagRequired("objective"); err != nil {
		panic(err)
	}
	mailCmd.AddCommand(sendMailCmd)
	rootCmd.AddCommand(mailCmd)
}
