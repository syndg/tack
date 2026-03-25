package blueprint_test

import (
	"github.com/syndg/tack/internal/harness/blueprint"
	"testing"
)

func TestDefaultsLoad(t *testing.T) {
	bps, err := blueprint.LoadDir("defaults")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(bps) != 3 {
		t.Fatalf("expected 3 blueprints, got %d", len(bps))
	}
	for name, bp := range bps {
		t.Logf("%s: %d steps, trigger=%q", name, len(bp.Steps), bp.Trigger)
	}
}
