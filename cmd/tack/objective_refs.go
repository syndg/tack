package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/domain"
)

func resolveObjectiveRef(cmd *cobra.Command, c *client.Client, raw string) (*domain.Objective, error) {
	objectives, err := c.ListObjectives(cmd.Context())
	if err != nil {
		return nil, err
	}
	return resolveObjectiveFromList(objectives, raw)
}

func resolveObjectiveFromList(objectives []domain.Objective, raw string) (*domain.Objective, error) {
	if len(objectives) == 0 {
		return nil, fmt.Errorf("no objectives found for this project")
	}
	ref := strings.TrimSpace(raw)
	if ref == "" || ref == "latest" || ref == "current" {
		return &objectives[0], nil
	}
	for i := range objectives {
		if objectives[i].ID == ref {
			return &objectives[i], nil
		}
	}
	var matches []*domain.Objective
	for i := range objectives {
		if strings.HasPrefix(objectives[i].ID, ref) {
			matches = append(matches, &objectives[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no objective matches %q", ref)
	case 1:
		return matches[0], nil
	default:
		display := make([]string, 0, min(3, len(matches)))
		for _, objective := range matches[:min(3, len(matches))] {
			display = append(display, truncateID(objective.ID))
		}
		return nil, fmt.Errorf("objective reference %q is ambiguous; matches objectives %s", ref, strings.Join(display, ", "))
	}
}
