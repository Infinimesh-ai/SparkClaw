package emaildiagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
)

var errSourceBytes = errors.New("source_byte_limit")
var errSourceJSON = errors.New("source_malformed")
var errSourceSchema = errors.New("source_schema_unsupported")
var errSourceRows = errors.New("source_row_limit")

type sourceInput struct {
	ctx    context.Context
	r      io.Reader
	result *ReadResult
	max    int64
}

func (r *sourceInput) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	left := r.max - r.result.Bytes
	if left <= 0 {
		return 0, errSourceBytes
	}
	if int64(len(p)) > left {
		p = p[:left]
	}
	n, err := r.r.Read(p)
	r.result.Bytes += int64(n)
	return n, err
}

type sourceCapture struct {
	raw   []byte
	limit int
	large bool
}

func (c *sourceCapture) add(b byte) {
	if c == nil {
		return
	}
	if len(c.raw) < c.limit {
		c.raw = append(c.raw, b)
	} else {
		c.large = true
	}
}

type sourceStream struct{ r *bufio.Reader }

func (s *sourceStream) peek() (byte, error) {
	p, e := s.r.Peek(1)
	if e != nil {
		return 0, e
	}
	return p[0], nil
}
func (s *sourceStream) take(c *sourceCapture) (byte, error) {
	b, e := s.r.ReadByte()
	if e == nil {
		c.add(b)
	}
	return b, e
}
func (s *sourceStream) space(c *sourceCapture) error {
	for {
		b, e := s.peek()
		if e != nil {
			return e
		}
		if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
			return nil
		}
		_, _ = s.take(c)
	}
}
func (s *sourceStream) expect(w byte, c *sourceCapture) error {
	b, e := s.take(c)
	if e != nil {
		return e
	}
	if b != w {
		return errSourceJSON
	}
	return nil
}
func (s *sourceStream) str(c *sourceCapture) error {
	if e := s.expect('"', c); e != nil {
		return e
	}
	for {
		b, e := s.take(c)
		if e != nil {
			return e
		}
		if b == '"' {
			return nil
		}
		if b < 32 {
			return errSourceJSON
		}
		if b != '\\' {
			continue
		}
		b, e = s.take(c)
		if e != nil {
			return e
		}
		switch b {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			for i := 0; i < 4; i++ {
				b, e = s.take(c)
				if e != nil {
					return e
				}
				if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F') {
					return errSourceJSON
				}
			}
		default:
			return errSourceJSON
		}
	}
}
func (s *sourceStream) key() (string, error) {
	c := &sourceCapture{limit: 4096}
	if e := s.str(c); e != nil {
		return "", e
	}
	if c.large {
		return "", errSourceSchema
	}
	var key string
	if json.Unmarshal(c.raw, &key) != nil {
		return "", errSourceJSON
	}
	return key, nil
}

// value validates skipped JSON without retaining long strings, arrays, or
// unrelated objects. A capture never grows past the per-record bound.
func (s *sourceStream) value(c *sourceCapture, depth int) error {
	if depth > 128 {
		return errSourceSchema
	}
	if e := s.space(c); e != nil {
		return e
	}
	b, e := s.peek()
	if e != nil {
		return e
	}
	switch b {
	case '"':
		return s.str(c)
	case '{', '[':
		_, _ = s.take(c)
		end := byte(']')
		if b == '{' {
			end = '}'
		}
		if e = s.space(c); e != nil {
			return e
		}
		next, e := s.peek()
		if e != nil {
			return e
		}
		if next == end {
			_, e = s.take(c)
			return e
		}
		for {
			if b == '{' {
				if e = s.str(c); e != nil {
					return e
				}
				if e = s.space(c); e != nil {
					return e
				}
				if e = s.expect(':', c); e != nil {
					return e
				}
			}
			if e = s.value(c, depth+1); e != nil {
				return e
			}
			if e = s.space(c); e != nil {
				return e
			}
			next, e = s.take(c)
			if e != nil {
				return e
			}
			if next == end {
				return nil
			}
			if next != ',' {
				return errSourceJSON
			}
			if e = s.space(c); e != nil {
				return e
			}
		}
	case 't', 'f', 'n':
		word := "null"
		if b == 't' {
			word = "true"
		} else if b == 'f' {
			word = "false"
		}
		for i := range word {
			if e = s.expect(word[i], c); e != nil {
				return e
			}
		}
		return nil
	default:
		// The number grammar is checked incrementally, with no scalar buffer.
		if b == '-' {
			_, _ = s.take(c)
			b, e = s.peek()
			if e != nil {
				return e
			}
		}
		if b == '0' {
			_, _ = s.take(c)
		} else if b >= '1' && b <= '9' {
			for {
				_, _ = s.take(c)
				b, e = s.peek()
				if e != nil {
					if e == io.EOF {
						return nil
					}
					return e
				}
				if b < '0' || b > '9' {
					break
				}
			}
		} else {
			return errSourceJSON
		}
		b, e = s.peek()
		if e != nil {
			if e == io.EOF {
				return nil
			}
			return e
		}
		if b == '.' {
			_, _ = s.take(c)
			if e = s.digits(c); e != nil {
				return e
			}
		}
		b, e = s.peek()
		if e != nil {
			if e == io.EOF {
				return nil
			}
			return e
		}
		if b == 'e' || b == 'E' {
			_, _ = s.take(c)
			b, e = s.peek()
			if e != nil {
				return e
			}
			if b == '+' || b == '-' {
				_, _ = s.take(c)
			}
			if e = s.digits(c); e != nil {
				return e
			}
		}
		return nil
	}
}
func (s *sourceStream) digits(c *sourceCapture) error {
	b, e := s.peek()
	if e != nil {
		return e
	}
	if b < '0' || b > '9' {
		return errSourceJSON
	}
	for {
		_, _ = s.take(c)
		b, e = s.peek()
		if e != nil {
			if e == io.EOF {
				return nil
			}
			return e
		}
		if b < '0' || b > '9' {
			return nil
		}
	}
}

func sourceErrorGap(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "source_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "source_timeout"
	case errors.Is(err, errSourceBytes):
		return "source_byte_limit"
	case errors.Is(err, errSourceSchema):
		return "source_schema_unsupported"
	case errors.Is(err, errSourceRows):
		return "source_row_limit"
	default:
		return "source_malformed"
	}
}
