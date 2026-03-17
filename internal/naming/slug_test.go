package naming

import "testing"

func TestStreamSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"CORS Middleware", "cors-middleware"},
		{"Health Check DB Verification", "health-check-db-verification"},
		{"simple", "simple"},
		{"ALLCAPS", "allcaps"},
		{"  spaces  around  ", "spaces-around"},
		{"special!@#chars$%^here", "special-chars-here"},
		{"a--b---c", "a-b-c"},
		{"---leading-trailing---", "leading-trailing"},
		{"", "stream"},
		{"!!!###", "stream"},
		{"a", "a"},
		// Truncation: 31+ chars should be cut to 30 max.
		{"abcdefghijklmnopqrstuvwxyz12345extra", "abcdefghijklmnopqrstuvwxyz1234"},
		// Truncation should not leave trailing hyphens.
		{"abcdefghijklmnopqrstuvwxyz1234-extra", "abcdefghijklmnopqrstuvwxyz1234"},
		// Unicode → hyphens.
		{"日本語タイトル", "stream"},
		{"hello世界world", "hello-world"},
		// Digits only.
		{"12345", "12345"},
		// Mixed case preserved as lowercase.
		{"CamelCaseTitle", "camelcasetitle"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := StreamSlug(tt.input)
			if got != tt.want {
				t.Errorf("StreamSlug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStreamSlug_Deterministic(t *testing.T) {
	// Same input must always produce the same slug (branch naming depends on this).
	title := "Auth Middleware Implementation"
	first := StreamSlug(title)
	for i := 0; i < 100; i++ {
		if got := StreamSlug(title); got != first {
			t.Fatalf("iteration %d: StreamSlug(%q) = %q, want %q", i, title, got, first)
		}
	}
}

func TestStreamSlug_MaxLength(t *testing.T) {
	// No output should ever exceed 30 characters.
	long := "this is a very long stream title that should definitely be truncated to thirty characters or fewer"
	got := StreamSlug(long)
	if len(got) > 30 {
		t.Errorf("StreamSlug output %q has length %d, want <= 30", got, len(got))
	}
}
