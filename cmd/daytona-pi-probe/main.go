// Quick probe: can Pi run inside a Daytona sandbox?
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("1. Creating sandbox...")
	sb, err := client.Create(ctx, types.SnapshotParams{
		SandboxBaseParams: types.SandboxBaseParams{
			Name:      "deck-pi-probe",
			Ephemeral: true,
		},
	})
	if err != nil {
		log.Fatalf("Create: %v", err)
	}
	defer func() {
		fmt.Println("\nCleaning up...")
		sb.Delete(context.Background())
	}()

	fmt.Println("2. Installing Pi...")
	resp, err := sb.Process.ExecuteCommand(ctx, "npm install -g @mariozechner/pi-coding-agent", options.WithExecuteTimeout(120))
	if err != nil {
		log.Fatalf("Install: %v", err)
	}
	fmt.Printf("   exit=%d\n", resp.ExitCode)

	fmt.Println("3. Checking Pi version...")
	resp, err = sb.Process.ExecuteCommand(ctx, "pi --version")
	if err != nil {
		log.Fatalf("Version: %v", err)
	}
	fmt.Printf("   Pi version: %s (exit=%d)\n", resp.Result, resp.ExitCode)

	fmt.Println("4. Testing Pi RPC with a simple prompt...")
	// Run Pi in RPC mode, pipe a prompt to stdin, capture stdout
	// This tests if Pi can call the Anthropic API from inside the sandbox
	piCmd := `echo '{"type":"prompt","message":"Say hello in exactly 3 words"}' | pi --mode rpc --provider anthropic --model claude-sonnet-4-20250514 --thinking low 2>/dev/null | head -5`
	resp, err = sb.Process.ExecuteCommand(ctx, piCmd,
		options.WithExecuteTimeout(120),
	)
	if err != nil {
		fmt.Printf("   ERROR: %v\n", err)
	} else {
		fmt.Printf("   exit=%d\n   output: %s\n", resp.ExitCode, resp.Result)
	}

	fmt.Println("5. Testing with ANTHROPIC_API_KEY env var...")
	// Check if the env var is set (from credential injection)
	resp, _ = sb.Process.ExecuteCommand(ctx, "echo ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY:+SET}")
	fmt.Printf("   %s\n", resp.Result)

	fmt.Println("\nDone.")
}
