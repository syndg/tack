package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syndg/deck/internal/harness/blueprint"
)

const (
	MetadataKeyCommitMessage = "commit_message"
	MetadataKeyPRTitle       = "pr_title"
	MetadataKeyPRBody        = "pr_body"
	MessageOutputPrefix      = "DECK_MESSAGES:"
)

// GeneratedMessages are agent-authored delivery messages consumed by later steps.
type GeneratedMessages struct {
	CommitMessage string `json:"commit_message,omitempty"`
	PRTitle       string `json:"pr_title,omitempty"`
	PRBody        string `json:"pr_body,omitempty"`
}

// ParseGeneratedMessages parses an agent-authored JSON payload.
func ParseGeneratedMessages(data []byte) (GeneratedMessages, error) {
	var msgs GeneratedMessages
	if err := json.Unmarshal(data, &msgs); err != nil {
		return GeneratedMessages{}, fmt.Errorf("parsing generated messages: %w", err)
	}
	return msgs, nil
}

// ExtractGeneratedMessages looks for a trailing DECK_MESSAGES JSON line in an
// agent summary/output, returning the cleaned summary and any parsed messages.
func ExtractGeneratedMessages(output string) (cleaned string, msgs GeneratedMessages, found bool, err error) {
	trimmedOutput := strings.TrimSpace(output)
	if trimmedOutput == "" {
		return "", GeneratedMessages{}, false, nil
	}

	lines := strings.Split(trimmedOutput, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, MessageOutputPrefix) {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, MessageOutputPrefix))
		msgs, err := ParseGeneratedMessages([]byte(payload))
		if err != nil {
			return trimmedOutput, GeneratedMessages{}, true, err
		}

		cleanLines := append([]string{}, lines[:i]...)
		cleanLines = append(cleanLines, lines[i+1:]...)
		return strings.TrimSpace(strings.Join(cleanLines, "\n")), msgs, true, nil
	}

	return trimmedOutput, GeneratedMessages{}, false, nil
}

// ToMetadata converts generated messages to blueprint step metadata.
func (m GeneratedMessages) ToMetadata() map[string]string {
	metadata := map[string]string{}
	if m.CommitMessage != "" {
		metadata[MetadataKeyCommitMessage] = m.CommitMessage
	}
	if m.PRTitle != "" {
		metadata[MetadataKeyPRTitle] = m.PRTitle
	}
	if m.PRBody != "" {
		metadata[MetadataKeyPRBody] = m.PRBody
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

// GeneratedMessagesFromMetadata reconstructs generated messages from step metadata.
func GeneratedMessagesFromMetadata(metadata map[string]string) GeneratedMessages {
	if len(metadata) == 0 {
		return GeneratedMessages{}
	}
	return GeneratedMessages{
		CommitMessage: metadata[MetadataKeyCommitMessage],
		PRTitle:       metadata[MetadataKeyPRTitle],
		PRBody:        metadata[MetadataKeyPRBody],
	}
}

// RequestedMessageFieldNames returns the JSON field names expected from the agent.
func RequestedMessageFieldNames(req *blueprint.MessageRequests) []string {
	if req == nil {
		return nil
	}
	fields := make([]string, 0, 3)
	if req.Commit {
		fields = append(fields, MetadataKeyCommitMessage)
	}
	if req.PR {
		fields = append(fields, MetadataKeyPRTitle, MetadataKeyPRBody)
	}
	return fields
}
