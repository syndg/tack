package agents

import "testing"

func TestExtractGeneratedMessages(t *testing.T) {
	output := "Implemented the fix and validated it.\nDECK_MESSAGES:{\"pr_title\":\"Fix dispatch race\",\"pr_body\":\"## Summary\\n- stabilize dispatch\\n\",\"commit_message\":\"fix: stabilize dispatch race\"}"

	clean, msgs, found, err := ExtractGeneratedMessages(output)
	if err != nil {
		t.Fatalf("ExtractGeneratedMessages: %v", err)
	}
	if !found {
		t.Fatal("expected generated messages to be found")
	}
	if clean != "Implemented the fix and validated it." {
		t.Fatalf("clean = %q", clean)
	}
	if msgs.PRTitle != "Fix dispatch race" {
		t.Fatalf("PRTitle = %q", msgs.PRTitle)
	}
	if msgs.CommitMessage != "fix: stabilize dispatch race" {
		t.Fatalf("CommitMessage = %q", msgs.CommitMessage)
	}
}

func TestExtractGeneratedMessages_NoMarker(t *testing.T) {
	clean, msgs, found, err := ExtractGeneratedMessages("just a normal summary")
	if err != nil {
		t.Fatalf("ExtractGeneratedMessages: %v", err)
	}
	if found {
		t.Fatal("did not expect generated messages to be found")
	}
	if clean != "just a normal summary" {
		t.Fatalf("clean = %q", clean)
	}
	if msgs != (GeneratedMessages{}) {
		t.Fatalf("msgs = %#v, want empty", msgs)
	}
}
