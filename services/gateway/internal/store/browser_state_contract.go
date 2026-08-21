package store

import (
	"errors"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var errBrowserLoginBlockJSONDecode = errors.New("decode browser login block JSON")

func cloneBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord {
	record.ExpiresAt = cloneTimePointer(record.ExpiresAt)
	record.RevokedAt = cloneTimePointer(record.RevokedAt)
	return record
}

func cloneBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	block.ResumeArgs = cloneMCPJSONMap(block.ResumeArgs)
	block.TransitionLeaseUntil = cloneTimePointer(block.TransitionLeaseUntil)
	block.ResolvedAt = cloneTimePointer(block.ResolvedAt)
	if block.VisibleEvidence != nil {
		evidence := *block.VisibleEvidence
		evidence.GoalEvidenceRefs = slices.Clone(evidence.GoalEvidenceRefs)
		evidence.SourceToolCallIDs = slices.Clone(evidence.SourceToolCallIDs)
		block.VisibleEvidence = &evidence
	}
	return block
}

func cloneBrowserAuthRecordMap(values map[string]app.BrowserAuthRecord) map[string]app.BrowserAuthRecord {
	out := make(map[string]app.BrowserAuthRecord, len(values))
	for id, record := range values {
		out[id] = cloneBrowserAuthRecord(record)
	}
	return out
}

func cloneBrowserLoginBlockMap(values map[string]app.BrowserLoginBlock) map[string]app.BrowserLoginBlock {
	out := make(map[string]app.BrowserLoginBlock, len(values))
	for id, block := range values {
		out[id] = cloneBrowserLoginBlock(block)
	}
	return out
}
