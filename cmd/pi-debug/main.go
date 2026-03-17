// Debug: run Pi in Daytona via PTY and capture ALL output
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/options"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY not set")
	}

	client, err := daytona.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("1. Creating sandbox...")
	sb, err := client.Create(ctx, types.SnapshotParams{
		SandboxBaseParams: types.SandboxBaseParams{
			Name:      "pi-debug",
			Ephemeral: true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Delete(context.Background())

	fmt.Println("2. Installing Pi...")
	resp, _ := sb.Process.ExecuteCommand(ctx, "npm install -g @mariozechner/pi-coding-agent", options.WithExecuteTimeout(120))
	fmt.Printf("   Pi install exit=%d\n", resp.ExitCode)

	fmt.Println("3. Cloning repo...")
	sb.Git.Clone(ctx, "https://github.com/syndg/ledger.git", "/home/daytona/project",
		options.WithUsername("x-access-token"),
		options.WithPassword(os.Getenv("GITHUB_TOKEN")),
	)

	fmt.Println("4. Running Pi directly via ExecuteCommand (not PTY)...")
	// Run Pi with a simple echo test first
	piCmd := fmt.Sprintf(
		`cd /home/daytona/project && echo '{"type":"prompt","message":"Say hello"}' | ANTHROPIC_API_KEY=%s pi --mode rpc --provider anthropic --model claude-sonnet-4-20250514 --thinking low 2>&1 | head -20`,
		apiKey,
	)
	resp, err = sb.Process.ExecuteCommand(ctx, "sh -c '"+piCmd+"'", options.WithExecuteTimeout(120))
	if err != nil {
		fmt.Printf("   ERROR: %v\n", err)
	} else {
		fmt.Printf("   exit=%d\n   output:\n%s\n", resp.ExitCode, resp.Result)
	}

	fmt.Println("\n5. Testing PTY approach (what deck uses)...")
	pty, err := sb.Process.CreatePty(ctx, "pi-test",
		options.WithCreatePtyEnv(map[string]string{
			"ANTHROPIC_API_KEY": apiKey,
		}),
	)
	if err != nil {
		log.Fatalf("CreatePty: %v", err)
	}
	if err := pty.WaitForConnection(ctx); err != nil {
		log.Fatalf("WaitForConnection: %v", err)
	}

	// Send the same command deck uses
	cmd := `export PS1="" && stty -echo 2>/dev/null && cd /home/daytona/project && exec pi --mode rpc --provider anthropic --model claude-sonnet-4-20250514 --thinking low` + "\n"
	if err := pty.SendInput([]byte(cmd)); err != nil {
		log.Fatalf("SendInput: %v", err)
	}

	// Read PTY output for 30 seconds
	fmt.Println("   Reading PTY output for 30s...")
	deadline := time.After(30 * time.Second)
	for {
		select {
		case data, ok := <-pty.DataChan():
			if !ok {
				fmt.Println("   PTY DataChan closed")
				goto done
			}
			fmt.Printf("   PTY: %q\n", string(data))
		case <-deadline:
			fmt.Println("   30s timeout reached")
			goto done
		}
	}
done:

	// Send a prompt to Pi
	fmt.Println("\n6. Sending RPC prompt to Pi via PTY...")
	prompt := `{"type":"prompt","message":"Say hello in 3 words"}` + "\n"
	if err := pty.SendInput([]byte(prompt)); err != nil {
		fmt.Printf("   SendInput error: %v\n", err)
	}

	// Read more output
	deadline2 := time.After(30 * time.Second)
	for {
		select {
		case data, ok := <-pty.DataChan():
			if !ok {
				fmt.Println("   PTY DataChan closed")
				goto done2
			}
			fmt.Printf("   PTY: %q\n", string(data))
		case <-deadline2:
			fmt.Println("   30s timeout reached")
			goto done2
		}
	}
done2:
	pty.Disconnect()
	fmt.Println("\nDone.")
}
