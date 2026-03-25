// One-off script to create a Daytona snapshot for the ledger project.
// Usage: DAYTONA_API_KEY=dtn_... go run ./cmd/snapshot-create/
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

func main() {
	client, err := daytona.NewClient()
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Build image: ubuntu + git + bun + gh CLI + node (bun needs it for some packages)
	image := daytona.Base("ubuntu:22.04").
		AptGet([]string{"git", "curl", "unzip", "ca-certificates"}).
		// Install Bun
		Run("curl -fsSL https://bun.sh/install | bash").
		// Install GitHub CLI
		Run("curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg && " +
			"echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main' > /etc/apt/sources.list.d/github-cli.list && " +
			"apt-get update && apt-get install -y gh && rm -rf /var/lib/apt/lists/*").
		// Make bun available in PATH for all users
		Run("ln -sf /root/.bun/bin/bun /usr/local/bin/bun && ln -sf /root/.bun/bin/bunx /usr/local/bin/bunx").
		Env("BUN_INSTALL", "/root/.bun").
		Env("PATH", "/root/.bun/bin:/usr/local/bin:/usr/bin:/bin").
		Workdir("/home/daytona/project")

	log.Println("Creating snapshot 'tack-ledger'...")
	log.Println("This will take a few minutes to build the image.")

	snapshot, logChan, err := client.Snapshot.Create(ctx, &types.CreateSnapshotParams{
		Name:  "tack-ledger",
		Image: image,
		Resources: &types.Resources{
			CPU:    2,
			Memory: 2048,
			Disk:   10,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create snapshot: %v", err)
	}

	// Stream build logs
	for line := range logChan {
		fmt.Println(line)
	}

	log.Printf("Snapshot created: %s (ID: %s, State: %s)\n", snapshot.Name, snapshot.ID, snapshot.State)
	log.Printf("\nAdd this to your .tack/config.yaml:")
	log.Printf("  sandbox:\n    daytona:\n      snapshot: %s", snapshot.Name)
}
