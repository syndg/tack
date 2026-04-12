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
	if len(bps) != 4 {
		t.Fatalf("expected 4 workflows, got %d", len(bps))
	}
	for id, bp := range bps {
		t.Logf("%s: %d steps, default=%v", id, len(bp.Steps), bp.Default)
	}
}
