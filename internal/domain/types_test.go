package domain

import "testing"

func TestIsValidStreamTransition(t *testing.T) {
	valid := []struct {
		from, to StreamStatus
	}{
		{StreamStatusPending, StreamStatusExecuting},
		{StreamStatusPending, StreamStatusFailed},
		{StreamStatusExecuting, StreamStatusCompleted},
		{StreamStatusExecuting, StreamStatusFailed},
		{StreamStatusExecuting, StreamStatusMergeReady},
		{StreamStatusCompleted, StreamStatusMergeReady},
		{StreamStatusCompleted, StreamStatusFailed},
		{StreamStatusMergeReady, StreamStatusMerging},
		{StreamStatusMergeReady, StreamStatusFailed},
		{StreamStatusMerging, StreamStatusMerged},
		{StreamStatusMerging, StreamStatusFailed},
		{StreamStatusMerging, StreamStatusMergeReady},
		{StreamStatusFailed, StreamStatusPending},
	}
	for _, tc := range valid {
		if !IsValidStreamTransition(tc.from, tc.to) {
			t.Errorf("expected %s → %s to be valid", tc.from, tc.to)
		}
	}

	invalid := []struct {
		from, to StreamStatus
	}{
		{StreamStatusPending, StreamStatusCompleted},
		{StreamStatusPending, StreamStatusMergeReady},
		{StreamStatusPending, StreamStatusMerged},
		{StreamStatusExecuting, StreamStatusPending},
		{StreamStatusCompleted, StreamStatusExecuting},
		{StreamStatusCompleted, StreamStatusPending},
		{StreamStatusMerged, StreamStatusPending},
		{StreamStatusMerged, StreamStatusFailed},
		{StreamStatusMerged, StreamStatusMergeReady},
		{StreamStatusFailed, StreamStatusCompleted},
		{StreamStatusFailed, StreamStatusExecuting},
	}
	for _, tc := range invalid {
		if IsValidStreamTransition(tc.from, tc.to) {
			t.Errorf("expected %s → %s to be invalid", tc.from, tc.to)
		}
	}
}
