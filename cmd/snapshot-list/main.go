package main

import (
	"context"
	"fmt"
	"log"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

func main() {
	client, err := daytona.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// List existing snapshots
	snaps, err := client.Snapshot.List(ctx, nil, nil)
	if err != nil {
		fmt.Println("List snapshots error:", err)
	} else {
		fmt.Printf("Snapshots available: %d\n", snaps.Total)
		for _, s := range snaps.Items {
			fmt.Printf("  - %s (ID: %s, State: %s)\n", s.Name, s.ID, s.State)
		}
	}

	// Try creating a sandbox to verify API key works for that
	fmt.Println("\nTesting sandbox creation...")
	sb, err := client.Create(ctx, types.SnapshotParams{
		SandboxBaseParams: types.SandboxBaseParams{
			Name:      "deck-api-test",
			Ephemeral: true,
		},
	})
	if err != nil {
		fmt.Println("Sandbox create error:", err)
		return
	}
	fmt.Printf("Sandbox created: %s (ID: %s)\n", sb.Name, sb.ID)
	_ = sb.Delete(ctx)
	fmt.Println("Sandbox deleted.")
}
