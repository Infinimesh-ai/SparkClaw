// Package clarification signs bounded, content-free owner selections.
package clarification

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

const SchemaVersion = 2
const Lifetime = 24 * time.Hour

var ErrInvalid = errors.New("invalid clarification record")
var ErrStale = errors.New("clarification revisions changed")
var ErrExpired = errors.New("clarification expired")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)
var candidatePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}#[a-z][a-z0-9_]{0,47}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Binding struct {
	OwnerID        string `json:"owner_id"`
	ClientID       string `json:"client_id"`
	InstallationID string `json:"installation_id"`
	ConversationID string `json:"conversation_id"`
}
type Option struct {
	Number        int    `json:"n"`
	Kind          string `json:"kind"`
	CandidateID   string `json:"candidate_id,omitempty"`
	FileID        string `json:"file_id,omitempty"`
	DeliveryMode  string `json:"delivery_mode,omitempty"`
	BindingSHA256 string `json:"binding_sha256"`
}
type Record struct {
	SchemaVersion       int      `json:"schema_version"`
	ID                  string   `json:"id"`
	RequestID           string   `json:"request_id"`
	SourceMessageID     string   `json:"source_message_id"`
	SourceTextSHA256    string   `json:"source_text_sha256"`
	ContextSHA256       string   `json:"context_sha256"`
	PromptSHA256        string   `json:"prompt_sha256"`
	IssuedAt            string   `json:"issued_at"`
	ExpiresAt           string   `json:"expires_at"`
	GraphRevision       string   `json:"graph_revision"`
	CatalogRevision     string   `json:"catalog_revision"`
	CalibrationRevision string   `json:"calibration_revision"`
	Options             []Option `json:"options"`
	MAC                 string   `json:"mac,omitempty"`
}
type Revisions struct{ Graph, Catalog, Calibration string }

func ValidBinding(binding Binding) bool {
	return idPattern.MatchString(binding.OwnerID) && idPattern.MatchString(binding.ClientID) && idPattern.MatchString(binding.InstallationID) && idPattern.MatchString(binding.ConversationID)
}
func Valid(record Record) bool {
	if record.SchemaVersion != SchemaVersion || !idPattern.MatchString(record.ID) || !idPattern.MatchString(record.RequestID) || !idPattern.MatchString(record.SourceMessageID) || !digestPattern.MatchString(record.SourceTextSHA256) || !digestPattern.MatchString(record.ContextSHA256) || !digestPattern.MatchString(record.PromptSHA256) || !idPattern.MatchString(record.GraphRevision) || !idPattern.MatchString(record.CatalogRevision) || !idPattern.MatchString(record.CalibrationRevision) || len(record.Options) < 1 || len(record.Options) > 2 {
		return false
	}
	issued, err := time.Parse(time.RFC3339, record.IssuedAt)
	if err != nil || issued.Format("2006-01-02T15:04:05Z") != record.IssuedAt {
		return false
	}
	expires, err := time.Parse(time.RFC3339, record.ExpiresAt)
	if err != nil || expires.Format("2006-01-02T15:04:05Z") != record.ExpiresAt || !expires.After(issued) || expires.Sub(issued) > Lifetime {
		return false
	}
	for i, o := range record.Options {
		if o.Number != i+1 || o.Kind != record.Options[0].Kind || !digestPattern.MatchString(o.BindingSHA256) {
			return false
		}
		switch o.Kind {
		case "route":
			if !candidatePattern.MatchString(o.CandidateID) || o.DeliveryMode != "" || (o.FileID != "" && !idPattern.MatchString(o.FileID)) {
				return false
			}
		case "file":
			if !idPattern.MatchString(o.FileID) || o.CandidateID != "" || o.DeliveryMode != "" {
				return false
			}
		case "delivery":
			if o.CandidateID != "" || o.FileID != "" || (o.DeliveryMode != "current_route" && o.DeliveryMode != "explicit_external") {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func mac(key []byte, binding Binding, record Record) (string, error) {
	if len(key) != 32 || !ValidBinding(binding) || !Valid(record) {
		return "", ErrInvalid
	}
	record.MAC = ""
	raw, err := Canonical(struct {
		Domain  string  `json:"domain"`
		Binding Binding `json:"binding"`
		Record  Record  `json:"record"`
	}{"sparkclaw.clarification.v2", binding, record})
	if err != nil {
		return "", ErrInvalid
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(raw)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func Sign(key []byte, binding Binding, record Record) (Record, error) {
	value, err := mac(key, binding, record)
	if err != nil {
		return Record{}, err
	}
	record.MAC = value
	return record, nil
}
func Verify(key []byte, binding Binding, record Record, revisions Revisions) error {
	return VerifyAt(key, binding, record, revisions, time.Now().UTC())
}
func VerifyAt(key []byte, binding Binding, record Record, revisions Revisions, now time.Time) error {
	if !digestPattern.MatchString(record.MAC) {
		return ErrInvalid
	}
	expected, err := mac(key, binding, record)
	if err != nil || subtle.ConstantTimeCompare([]byte(expected), []byte(record.MAC)) != 1 {
		return ErrInvalid
	}
	if record.GraphRevision != revisions.Graph || record.CatalogRevision != revisions.Catalog || record.CalibrationRevision != revisions.Calibration {
		return ErrStale
	}
	issued, _ := time.Parse(time.RFC3339, record.IssuedAt)
	expires, _ := time.Parse(time.RFC3339, record.ExpiresAt)
	if now.Before(issued) || !now.Before(expires) {
		return ErrExpired
	}
	return nil
}
