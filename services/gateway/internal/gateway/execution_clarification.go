package gateway

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

// Freeze the owner-supplied metadata before temporary paths or application IDs
// are introduced. Input files belong to the final user message in this snapshot.
func executionClarificationSnapshot(e execution.Envelope) ([]clarification.SnapshotMessage, error) {
	if len(e.Messages) == 0 || len(e.Messages) > execution.ContextMessages {
		return nil, clarification.ErrInvalid
	}
	messages := make([]clarification.SnapshotMessage, 0, len(e.Messages))
	for _, message := range e.Messages {
		source := "conversation_attachment"
		if message.Role == "assistant" {
			source = "conversation_result"
		}
		files := make([]clarification.SnapshotFile, 0, len(message.Files))
		for _, file := range message.Files {
			files = append(files, clarification.SnapshotFile{ID: file.ID, Name: file.Name, Size: file.Size, SHA256: file.SHA256, Source: source})
		}
		messages = append(messages, clarification.SnapshotMessage{ID: message.ID, Role: message.Role, Content: message.Content, Files: files, FilesUnavailable: message.FilesUnavailable})
	}
	current := &messages[len(messages)-1]
	if current.Role != "user" {
		return nil, clarification.ErrInvalid
	}
	byID := make(map[string]clarification.SnapshotFile, len(current.Files))
	for _, file := range current.Files {
		byID[file.ID] = file
	}
	for _, file := range e.InputFiles {
		item := clarification.SnapshotFile{ID: file.ID, Name: file.Name, Size: file.Size, SHA256: file.SHA256, Source: "conversation_attachment"}
		if prior, exists := byID[file.ID]; exists {
			if prior != item {
				return nil, clarification.ErrInvalid
			}
			continue
		}
		current.Files = append(current.Files, item)
		byID[file.ID] = item
	}
	return messages, nil
}
