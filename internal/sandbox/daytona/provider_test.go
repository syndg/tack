package daytona

import (
	"context"
	"testing"

	"github.com/syndg/tack/internal/sandbox"
)

func TestMatchesLabels(t *testing.T) {
	tests := []struct {
		name   string
		target map[string]string
		filter map[string]string
		want   bool
	}{
		{"empty filter matches all", map[string]string{"a": "1"}, nil, true},
		{"exact match", map[string]string{"a": "1"}, map[string]string{"a": "1"}, true},
		{"subset match", map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "1"}, true},
		{"mismatch", map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
		{"missing key", map[string]string{"a": "1"}, map[string]string{"b": "1"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesLabels(tt.target, tt.filter)
			if got != tt.want {
				t.Errorf("matchesLabels(%v, %v) = %v, want %v", tt.target, tt.filter, got, tt.want)
			}
		})
	}
}

func TestDaytonaSandbox_Status(t *testing.T) {
	sb := &DaytonaSandbox{}
	if sb.Status() != sandbox.SandboxStatusRunning {
		t.Errorf("Status() = %v, want running", sb.Status())
	}
}

func TestAnsiEscapeStripping(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"\x1b[31mhello\x1b[0m", "hello"},
		{"plain text", "plain text"},
		{"\x1b[1;32mgreen bold\x1b[0m text", "green bold text"},
		{"no escapes here", "no escapes here"},
	}

	for _, tt := range tests {
		got := ansiEscape.ReplaceAllString(tt.input, "")
		if got != tt.want {
			t.Errorf("strip(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProvider_ListFiltering(t *testing.T) {
	p := &Provider{
		sandboxes: map[string]*DaytonaSandbox{
			"sb-1": {labels: map[string]string{"tack.role": "builder", "tack.objective": "obj-1"}},
			"sb-2": {labels: map[string]string{"tack.role": "lead", "tack.objective": "obj-1"}},
			"sb-3": {labels: map[string]string{"tack.role": "builder", "tack.objective": "obj-2"}},
		},
	}

	ctx := context.Background()

	// Filter by role
	result, err := p.List(ctx, map[string]string{"tack.role": "builder"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 builder sandboxes, got %d", len(result))
	}

	// Filter by role + objective
	result, err = p.List(ctx, map[string]string{"tack.role": "builder", "tack.objective": "obj-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 sandbox, got %d", len(result))
	}

	// No match
	result, err = p.List(ctx, map[string]string{"tack.role": "reviewer"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 sandboxes, got %d", len(result))
	}
}

func TestProvider_DeleteNotFound(t *testing.T) {
	p := &Provider{
		sandboxes: make(map[string]*DaytonaSandbox),
	}
	err := p.Delete(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for deleting nonexistent sandbox")
	}
}

func TestProvider_GetFromLocalMap(t *testing.T) {
	sb := &DaytonaSandbox{
		labels: map[string]string{"tack.role": "builder"},
	}
	p := &Provider{
		sandboxes: map[string]*DaytonaSandbox{"sb-1": sb},
	}
	got, err := p.Get(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != sb {
		t.Error("expected same sandbox instance")
	}
}
