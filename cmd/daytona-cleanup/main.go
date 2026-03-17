package main

import (
	"context"
	"fmt"
	"log"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
)

func main() {
	client, err := daytona.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	result, err := client.List(ctx, nil, nil, nil)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Found %d sandboxes\n", result.Total)
	for _, sb := range result.Items {
		fmt.Printf("  deleting %s (%s)...\n", sb.Name, sb.ID)
		if err := sb.Delete(ctx); err != nil {
			fmt.Printf("    ERROR: %v\n", err)
		}
	}
	fmt.Println("Done.")
}
