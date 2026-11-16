package mcdintegration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

// Canonical implements mcd-json-v1, including Python's short control escapes.
// DecodeStrict rejects duplicate keys, invalid UTF-8 and unpaired surrogates.
func Canonical(raw []byte) ([]byte, error) {
	if len(raw) > 256<<10 || appstream.DecodeStrict(raw, new(json.RawMessage)) != nil {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return nil, ErrInvalid
	}
	var out bytes.Buffer
	if err := canonicalValue(&out, value, 0); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func canonicalValue(out *bytes.Buffer, v any, depth int) error {
	if depth > 32 {
		return ErrInvalid
	}
	switch x := v.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(x))
	case json.Number:
		n, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil || n < -(1<<53-1) || n > 1<<53-1 {
			return ErrInvalid
		}
		out.WriteString(strconv.FormatInt(n, 10))
	case string:
		canonicalString(out, x)
	case []any:
		if len(x) > 4096 {
			return ErrInvalid
		}
		out.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := canonicalValue(out, item, depth+1); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		if len(x) > 512 {
			return ErrInvalid
		}
		keys := make([]string, 0, len(x))
		for key := range x {
			if !ascii(key, 128) {
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
			if err := canonicalValue(out, x[key], depth+1); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return ErrInvalid
	}
	return nil
}

func canonicalString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 32 {
				fmt.Fprintf(out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}

func digest(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	raw, err = Canonical(raw)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ascii(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, c := range s {
		if c < 32 || c >= 127 {
			return false
		}
	}
	return true
}
