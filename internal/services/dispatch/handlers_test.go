package dispatch

import (
	"testing"

	"github.com/syndg/deck/internal/harness/blueprint"
)

func TestGeneratedMessagesFromSource(t *testing.T) {
	exec := &blueprint.Execution{
		StepStates: map[string]*blueprint.StepState{
			"fix": {
				StepID: "fix",
				Metadata: map[string]string{
					"pr_title": "Fix flaky dispatch flow",
					"pr_body":  "## Summary\n- stabilize dispatch flow",
				},
			},
		},
	}

	msgs := generatedMessagesFromSource(exec, "fix")
	if msgs.PRTitle != "Fix flaky dispatch flow" {
		t.Fatalf("PRTitle = %q, want generated title", msgs.PRTitle)
	}
	if msgs.PRBody == "" {
		t.Fatal("expected PRBody to be populated")
	}
}
