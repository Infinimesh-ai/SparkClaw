package clarification

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

var integerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var fieldPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Canonical is the shared clarification v2 JSON encoding. It never normalizes
// owner strings, permits no floats/nulls, and does not use Go's HTML escaping.
func Canonical(value any) ([]byte, error) {
	if !validStrings(reflect.ValueOf(value), 0) {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	if err = appstream.DecodeStrict(raw, new(json.RawMessage)); err != nil {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err = decoder.Decode(&decoded); err != nil {
		return nil, ErrInvalid
	}
	var out bytes.Buffer
	if err = appendCanonical(&out, decoded); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func validStrings(v reflect.Value, depth int) bool {
	if depth > 64 {
		return false
	}
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		return v.IsNil() || validStrings(v.Elem(), depth+1)
	case reflect.String:
		return utf8.ValidString(v.String())
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !validStrings(v.Index(i), depth+1) {
				return false
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !validStrings(v.Field(i), depth+1) {
				return false
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if !validStrings(it.Key(), depth+1) || !validStrings(it.Value(), depth+1) {
				return false
			}
		}
	}
	return true
}
func appendCanonical(out *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case bool:
		if v {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		canonicalString(out, v)
	case json.Number:
		integer, parseErr := strconv.ParseUint(v.String(), 10, 53)
		if parseErr != nil || integer > 9007199254740991 || !integerPattern.MatchString(v.String()) {
			return ErrInvalid
		}
		out.WriteString(v.String())
	case []any:
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := appendCanonical(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			if !fieldPattern.MatchString(key) {
				return ErrInvalid
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			canonicalString(out, key)
			out.WriteByte(':')
			if err := appendCanonical(out, v[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return ErrInvalid
	}
	return nil
}
func canonicalString(out *bytes.Buffer, value string) {
	out.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"' || r == '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case r < 0x20:
			fmt.Fprintf(out, "\\u%04x", r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
}
func TextDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func Digest(value any) (string, error) {
	raw, err := Canonical(value)
	if err != nil {
		return "", err
	}
	return TextDigest(string(raw)), nil
}
func BindingDigest(value any) (string, error) { return Digest(value) }

type SnapshotFile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
	Source string `json:"source"`
}
type SnapshotMessage struct {
	ID               string
	Role             string
	Content          string
	Files            []SnapshotFile
	FilesUnavailable bool
}

// ContextDigest hashes only admitted input. It excludes clarification metadata
// and normalizes original current files to their message's attachment source.
func ContextDigest(messages []SnapshotMessage, source string) (string, error) {
	if len(messages) == 0 || len(messages) > 32 || source == "" {
		return "", ErrInvalid
	}
	type message struct {
		ID               string         `json:"id"`
		Role             string         `json:"role"`
		TextSHA256       string         `json:"text_sha256"`
		Files            []SnapshotFile `json:"files"`
		FilesUnavailable bool           `json:"files_unavailable"`
	}
	normalized := make([]message, 0, len(messages))
	seen := map[string]bool{}
	for _, m := range messages {
		if !idPattern.MatchString(m.ID) || seen[m.ID] || (m.Role != "user" && m.Role != "assistant") || !utf8.ValidString(m.Content) || len(m.Files) > 32 {
			return "", ErrInvalid
		}
		seen[m.ID] = true
		files := make([]SnapshotFile, 0, len(m.Files))
		ids := map[string]bool{}
		for _, f := range m.Files {
			if !idPattern.MatchString(f.ID) || ids[f.ID] || f.Name == "" || strings.ContainsAny(f.Name, "/\\\x00") || f.Size < 0 || !digestPattern.MatchString(f.SHA256) || (f.Source != "conversation_attachment" && f.Source != "conversation_result") {
				return "", ErrInvalid
			}
			ids[f.ID] = true
			files = append(files, f)
		}
		sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
		normalized = append(normalized, message{m.ID, m.Role, TextDigest(m.Content), files, m.FilesUnavailable})
	}
	return Digest(struct {
		Source   string    `json:"source_kind"`
		Messages []message `json:"messages"`
	}{source, normalized})
}
