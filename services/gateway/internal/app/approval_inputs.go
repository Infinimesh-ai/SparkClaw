package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const ApprovalInputSchemaVersion = 1
const ApprovalInputMaxBytes = 32 << 10

type ApprovalInputType string

const (
	ApprovalInputText       ApprovalInputType = "text"
	ApprovalInputNumber     ApprovalInputType = "number"
	ApprovalInputSelect     ApprovalInputType = "select"
	ApprovalInputDateOrNone ApprovalInputType = "date_or_none"
)

type ApprovalInputOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type ApprovalInputField struct {
	Name      string                `json:"name"`
	Label     string                `json:"label"`
	Type      ApprovalInputType     `json:"type"`
	Required  bool                  `json:"required"`
	Options   []ApprovalInputOption `json:"options,omitempty"`
	Min       *float64              `json:"min,omitempty"`
	Max       *float64              `json:"max,omitempty"`
	MaxLength int                   `json:"max_length,omitempty"`
}
type ApprovalInputEntry struct {
	ID      string               `json:"id"`
	Label   string               `json:"label"`
	Summary string               `json:"summary"`
	Fields  []ApprovalInputField `json:"fields"`
}
type ApprovalInputForm struct {
	SchemaVersion int                  `json:"schema_version"`
	Entries       []ApprovalInputEntry `json:"entries"`
}
type ApprovalInputValues map[string]map[string]any
type ApprovalInputSubmission struct {
	Digest        string              `json:"digest"`
	SchemaVersion int                 `json:"input_schema_version,omitempty"`
	Inputs        ApprovalInputValues `json:"inputs,omitempty"`
}

var approvalFieldKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,79}$`)

func ValidateApprovalInputForm(form *ApprovalInputForm) error {
	if form == nil {
		return errors.New("approval input form is missing")
	}
	raw, err := json.Marshal(form)
	if err != nil || len(raw) > ApprovalInputMaxBytes || form.SchemaVersion != ApprovalInputSchemaVersion || len(form.Entries) == 0 || len(form.Entries) > 20 {
		return errors.New("unsupported approval input form")
	}
	entries := map[string]bool{}
	count := 0
	for _, entry := range form.Entries {
		if !approvalFieldKey.MatchString(entry.ID) || entries[entry.ID] || strings.TrimSpace(entry.Label) == "" || utf8.RuneCountInString(entry.Label) > 160 || utf8.RuneCountInString(entry.Summary) > 2000 || len(entry.Fields) > 20 {
			return errors.New("invalid approval input entry")
		}
		entries[entry.ID] = true
		fields := map[string]bool{}
		for _, field := range entry.Fields {
			count++
			if !approvalFieldKey.MatchString(field.Name) || fields[field.Name] || strings.TrimSpace(field.Label) == "" || utf8.RuneCountInString(field.Label) > 160 || field.MaxLength < 0 || field.MaxLength > 500 {
				return errors.New("invalid approval input field")
			}
			fields[field.Name] = true
			if field.Min != nil && (math.IsNaN(*field.Min) || math.IsInf(*field.Min, 0)) || field.Max != nil && (math.IsNaN(*field.Max) || math.IsInf(*field.Max, 0)) || field.Min != nil && field.Max != nil && *field.Min > *field.Max {
				return errors.New("invalid approval numeric range")
			}
			switch field.Type {
			case ApprovalInputText, ApprovalInputNumber, ApprovalInputDateOrNone:
				if len(field.Options) > 0 {
					return errors.New("unexpected approval options")
				}
			case ApprovalInputSelect:
				if len(field.Options) == 0 || len(field.Options) > 2000 {
					return errors.New("invalid approval options")
				}
				options := map[string]bool{}
				for _, option := range field.Options {
					if option.Value == "" || len(option.Value) > 256 || options[option.Value] || strings.TrimSpace(option.Label) == "" || utf8.RuneCountInString(option.Label) > 500 {
						return errors.New("invalid approval option")
					}
					options[option.Value] = true
				}
			default:
				return errors.New("unsupported approval input type")
			}
		}
	}
	if count == 0 {
		return errors.New("approval input form has no fields")
	}
	return nil
}

func ValidateApprovalInputs(form *ApprovalInputForm, values ApprovalInputValues) error {
	if err := ValidateApprovalInputForm(form); err != nil {
		return err
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > ApprovalInputMaxBytes {
		return errors.New("approval inputs exceed capacity")
	}
	entries := map[string]ApprovalInputEntry{}
	for _, entry := range form.Entries {
		entries[entry.ID] = entry
	}
	for id, fields := range values {
		entry, ok := entries[id]
		if !ok {
			return errors.New("unexpected approval input entry")
		}
		allowed := map[string]bool{}
		for _, field := range entry.Fields {
			allowed[field.Name] = true
		}
		for name := range fields {
			if !allowed[name] {
				return errors.New("unexpected approval input field")
			}
		}
	}
	for _, entry := range form.Entries {
		for _, field := range entry.Fields {
			value, exists := values[entry.ID][field.Name]
			if !exists || value == nil || value == "" {
				if field.Required {
					return fmt.Errorf("%s: %s is required", entry.Label, field.Label)
				}
				continue
			}
			if err := validateApprovalValue(field, value); err != nil {
				return fmt.Errorf("%s: %s: %w", entry.Label, field.Label, err)
			}
		}
	}
	return nil
}
func validateApprovalValue(field ApprovalInputField, value any) error {
	if field.Type == ApprovalInputNumber {
		raw, err := json.Marshal(value)
		if err != nil {
			return errors.New("invalid number")
		}
		var number float64
		if json.Unmarshal(raw, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) || field.Min != nil && number < *field.Min || field.Max != nil && number > *field.Max {
			return errors.New("number outside allowed range")
		}
		return nil
	}
	str, ok := value.(string)
	if !ok {
		return errors.New("text value required")
	}
	limit := field.MaxLength
	if limit == 0 {
		limit = 500
	}
	if !utf8.ValidString(str) || utf8.RuneCountInString(str) > limit || strings.TrimSpace(str) == "" {
		return errors.New("invalid text length")
	}
	for _, r := range str {
		if (r < 32 && r != '\n') || r == 127 {
			return errors.New("invalid text character")
		}
	}
	switch field.Type {
	case ApprovalInputSelect:
		for _, opt := range field.Options {
			if str == opt.Value {
				return nil
			}
		}
		return errors.New("choose a listed option")
	case ApprovalInputDateOrNone:
		if str == "no_expiry" {
			return nil
		}
		parsed, err := time.Parse("2006-01-02", str)
		if err != nil || parsed.Format("2006-01-02") != str {
			return errors.New("choose a date or no expiry")
		}
	}
	return nil
}
func ApprovalInputsDigest(values ApprovalInputValues) (string, error) {
	return approvalJSONDigest(values)
}
func ApprovalFormDigest(approval Approval) (string, error) {
	return approvalJSONDigest(struct {
		ID        string             `json:"id"`
		Session   string             `json:"session_id"`
		Run       string             `json:"run_id"`
		Call      string             `json:"tool_call_id"`
		Tool      string             `json:"tool"`
		Arguments map[string]any     `json:"arguments"`
		Form      *ApprovalInputForm `json:"input_form"`
	}{approval.ID, approval.SessionID, approval.RunID, approval.ToolCallID, approval.Tool, approval.Arguments, approval.InputForm})
}
func approvalJSONDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
