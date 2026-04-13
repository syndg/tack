package domain

import (
	"reflect"
	"testing"
)

func TestStreamDescriptionPayloadRoundTrip(t *testing.T) {
	description := "Validate focused command log navigation"
	criteria := []string{
		"prove pageUp/pageDown move the command log when focused",
		"prove the same keys do nothing when the command log is unfocused",
	}
	payload := StreamDescriptionPayload(description, criteria)
	decodedDescription, decodedCriteria := ParseStreamDescriptionPayload(payload)
	if decodedDescription != description {
		t.Fatalf("decoded description = %q, want %q", decodedDescription, description)
	}
	if !reflect.DeepEqual(decodedCriteria, criteria) {
		t.Fatalf("decoded criteria = %v, want %v", decodedCriteria, criteria)
	}
}
