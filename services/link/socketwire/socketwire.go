// Package socketwire is the framing between the ISCP connector and the
// Gateway on the private Unix socket: one connection per device session,
// an attach exchange, then link frames, each prefixed by a 4-byte
// big-endian length.
package socketwire

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// Attach is the first message on a connector socket connection.
type Attach struct {
	DeviceID  string `json:"device_id"`
	Transport string `json:"transport"`
	SessionID string `json:"session_id,omitempty"`
}

// AttachResult answers the attach.
type AttachResult struct {
	OK        bool                `json:"ok"`
	Code      string              `json:"code,omitempty"`
	Message   string              `json:"message,omitempty"`
	Principal *contract.Principal `json:"principal,omitempty"`
}

// Stream frames messages with a 4-byte big-endian length.
type Stream struct {
	c  net.Conn
	mu sync.Mutex
	rm sync.Mutex
}

// NewStream wraps a connection.
func NewStream(c net.Conn) *Stream { return &Stream{c: c} }

// MaxFrame bounds one socket frame.
const MaxFrame = contract.MaxFrameBytes + 64*1024

func (s *Stream) ReadFrame(ctx context.Context) ([]byte, error) {
	s.rm.Lock()
	defer s.rm.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		_ = s.c.SetReadDeadline(dl)
		defer s.c.SetReadDeadline(time.Time{})
	}
	stop := context.AfterFunc(ctx, func() { _ = s.c.SetReadDeadline(time.Now()) })
	defer stop()
	var hdr [4]byte
	if _, err := io.ReadFull(s.c, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrame {
		return nil, errors.New("socket frame too large")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(s.c, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Stream) WriteFrame(ctx context.Context, b []byte) error {
	if len(b) > MaxFrame {
		return errors.New("socket frame too large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dl := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = s.c.SetWriteDeadline(dl)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := s.c.Write(hdr[:]); err != nil {
		return err
	}
	_, err := s.c.Write(b)
	return err
}

func (s *Stream) Close() error { return s.c.Close() }
