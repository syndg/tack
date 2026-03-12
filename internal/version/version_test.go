package version

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"seconds only", 45 * time.Second, "45s"},
		{"minutes only", 10 * time.Minute, "10m"},
		{"minutes and seconds", 2*time.Minute + 30*time.Second, "2m 30s"},
		{"hours only", 3 * time.Hour, "3h"},
		{"hours and minutes", 2*time.Hour + 5*time.Minute, "2h 5m"},
		{"days only", 7 * 24 * time.Hour, "7d"},
		{"days and hours", 3*24*time.Hour + 1*time.Hour, "3d 1h"},
		{"one second", time.Second, "1s"},
		{"one minute", time.Minute, "1m"},
		{"one hour", time.Hour, "1h"},
		{"one day", 24 * time.Hour, "1d"},
		{"negative duration", -45 * time.Second, "-45s"},
		{"negative complex", -(2*time.Hour + 5*time.Minute), "-2h 5m"},
		{"days truncate lower", 3*24*time.Hour + 2*time.Minute, "3d"},
		{"hours truncate seconds", 1*time.Hour + 30*time.Second, "1h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatDuration(tt.d); got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
