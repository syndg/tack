package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/options"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

func main() {
	client, err := daytona.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create sandbox from default snapshot
	fmt.Println("Creating sandbox from default snapshot...")
	sb, err := client.Create(ctx, types.SnapshotParams{
		SandboxBaseParams: types.SandboxBaseParams{
			Name:      "tack-probe",
			Ephemeral: true,
		},
	})
	if err != nil {
		log.Fatalf("Create: %v", err)
	}
	fmt.Printf("Created: %s\n\n", sb.ID)

	defer func() {
		fmt.Println("\nCleaning up...")
		sb.Delete(context.Background())
	}()

	// Check what tools are available
	cmds := []string{
		"which git && git --version",
		"which bun && bun --version",
		"which node && node --version",
		"which gh && gh --version",
		"which curl && curl --version | head -1",
		"cat /etc/os-release | head -2",
		"echo $PATH",
		"whoami",
		"pwd",
	}

	for _, cmd := range cmds {
		resp, err := sb.Process.ExecuteCommand(ctx, cmd, options.WithCwd("/"))
		if err != nil {
			fmt.Printf("$ %s\n  ERROR: %v\n\n", cmd, err)
			continue
		}
		fmt.Printf("$ %s\n  exit=%d  %s\n\n", cmd, resp.ExitCode, resp.Result)
	}
}
