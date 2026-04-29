package main

import (
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

func TestResolveObjectiveFromListUsesLatestAliases(t *testing.T) {
	objectives := []domain.Objective{
		{ID: "objective-new", CreatedAt: time.Unix(20, 0)},
		{ID: "objective-old", CreatedAt: time.Unix(10, 0)},
	}
	for _, ref := range []string{"", "latest", "current"} {
		objective, err := resolveObjectiveFromList(objectives, ref)
		if err != nil {
			t.Fatalf("resolve %q: %v", ref, err)
		}
		if objective.ID != "objective-new" {
			t.Fatalf("resolve %q = %q", ref, objective.ID)
		}
	}
}

func TestResolveObjectiveFromListMatchesUnambiguousShortID(t *testing.T) {
	objectives := []domain.Objective{{ID: "abcd1111-full"}, {ID: "efgh2222-full"}}
	objective, err := resolveObjectiveFromList(objectives, "abcd")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if objective.ID != "abcd1111-full" {
		t.Fatalf("objective.ID = %q", objective.ID)
	}
}

func TestResolveObjectiveFromListRejectsAmbiguousShortID(t *testing.T) {
	_, err := resolveObjectiveFromList([]domain.Objective{{ID: "abcd1111"}, {ID: "abcd2222"}}, "abcd")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v", err)
	}
}
